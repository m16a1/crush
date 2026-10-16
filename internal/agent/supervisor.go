package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultMaxRestarts is how many automatic restarts a node gets when
// its worker run dies (provider error, stream stall, watchdog timeout)
// before the node stalls for mastermind adjudication.
const DefaultMaxRestarts = 1

// defaultMaxParallelSubagents caps concurrent nodes when neither the
// config option nor the plan says otherwise.
const defaultMaxParallelSubagents = 2

// defaultWakeDebounce coalesces Supervisor events that arrive within a
// short window into a single mastermind wake-up turn. Tests shorten it.
const defaultWakeDebounce = 3 * time.Second

var planNodeIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// PlanNodeLauncher starts and verifies plan nodes. The coordinator
// implements it over real child sessions; tests inject a fake so the
// Supervisor is fully testable without any provider.
type PlanNodeLauncher interface {
	// LaunchNode starts a worker for the node. It must not block the
	// caller and reports back via Supervisor.NodeDone.
	LaunchNode(sup *Supervisor, node *PlanNode, mainSessionID string)
	// VerifyNode runs the read-only verifier child for a node whose
	// worker claimed completion and whose verify_cmd passed (or is
	// absent). It reports back via Supervisor.ReportVerdict.
	VerifyNode(sup *Supervisor, node *PlanNode, mainSessionID string, evidence VerifyEvidence)
}

// supervisorHooks wire the Supervisor to the outside world.
type supervisorHooks struct {
	launcher    PlanNodeLauncher
	wake        func(report string)
	spent       func() float64
	maxParallel func() int
}

// nodeRun tracks a live node launch so the watchdog and kill paths can
// cancel it.
type nodeRun struct {
	cancel context.CancelFunc
	timer  *time.Timer
}

// Supervisor is the pure-algorithm orchestration actor: it owns the
// plan DAG, the slot table, restart budgets, and verification. No LLM
// lives here. The mastermind (the main coder session) influences it by
// editing the plan through the plan tool; the Supervisor decides when
// and what to launch from the plan metadata alone.
//
// Concurrency: one mutex, methods callable from tool goroutines,
// worker goroutines, and timers. Every state change is persisted
// before the corresponding launch is dispatched.
type Supervisor struct {
	mu       sync.Mutex
	plan     *Plan
	store    *planStore
	hooks    supervisorHooks
	running  map[string]*nodeRun
	wakeEvts []string
	wakeTimr *time.Timer
	debounce time.Duration
	now      func() time.Time
	// deaths counts consecutive mastermind turn deaths.
	deaths int
	// deadlockEscalated guards the single deadlock wake-up until the
	// plan changes again.
	deadlockEscalated bool
}

// newSupervisor restores a supervisor from persisted state. Nodes left
// running or verifying by a previous process are impossible now (their
// child runs died with it), so they return to pending.
func newSupervisor(plan *Plan, store *planStore, hooks supervisorHooks) *Supervisor {
	for _, n := range plan.Nodes {
		if n.State == NodeRunning || n.State == NodeVerifying {
			n.State = NodePending
		}
	}
	return &Supervisor{
		plan:     plan,
		store:    store,
		hooks:    hooks,
		running:  make(map[string]*nodeRun),
		debounce: defaultWakeDebounce,
		now:      time.Now,
	}
}

// AddNodes appends new nodes to the plan, validates the DAG, and
// schedules whatever is immediately ready.
func (s *Supervisor) AddNodes(inputs []PlanNodeInput) error {
	if len(inputs) == 0 {
		return fmt.Errorf("add requires at least one node")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, in := range inputs {
		if !planNodeIDPattern.MatchString(in.ID) {
			return fmt.Errorf("invalid node id %q (expected lowercase alnum, start with alnum, max 32 chars)", in.ID)
		}
		if s.find(in.ID) != nil {
			return fmt.Errorf("node %q already exists; use update or retry", in.ID)
		}
		if strings.TrimSpace(in.Title) == "" {
			return fmt.Errorf("node %q requires a title", in.ID)
		}
		if strings.TrimSpace(in.Prompt) == "" {
			return fmt.Errorf("node %q requires a prompt", in.ID)
		}
		if in.Agent != "" && in.Agent != "coder" && in.Agent != "task" {
			return fmt.Errorf("node %q has unknown agent %q (expected coder or task)", in.ID, in.Agent)
		}
	}
	// Dependencies may reference existing nodes or other nodes in this
	// same batch.
	for _, in := range inputs {
		for _, dep := range in.DependsOn {
			if dep == in.ID {
				return fmt.Errorf("node %q depends on itself", in.ID)
			}
			if s.find(dep) == nil && !slices.ContainsFunc(inputs, func(p PlanNodeInput) bool { return p.ID == dep }) {
				return fmt.Errorf("node %q depends on unknown node %q", in.ID, dep)
			}
		}
	}

	for _, in := range inputs {
		node := &PlanNode{
			ID:             in.ID,
			Title:          in.Title,
			Prompt:         in.Prompt,
			Agent:          in.Agent,
			DependsOn:      slices.Clone(in.DependsOn),
			Priority:       in.Priority,
			VerifyCmd:      in.VerifyCmd,
			VerifyCriteria: in.VerifyCriteria,
			Files:          slices.Clone(in.Files),
			TimeoutSecs:    in.Timeout,
			State:          NodePending,
			UpdatedAt:      s.now(),
		}
		if in.MaxRestarts != nil {
			mr := *in.MaxRestarts
			node.MaxRestarts = &mr
		}
		s.plan.Nodes = append(s.plan.Nodes, node)
	}

	pending := s.plan.Nodes[:len(s.plan.Nodes)-len(inputs)]
	if s.findCycleLocked() != "" {
		s.plan.Nodes = append([]*PlanNode{}, pending...)
		return fmt.Errorf("adding these nodes would create a dependency cycle")
	}

	s.plan.Paused = false
	s.plan.CompletedNotified = false
	s.plan.DeathEscalated = false
	s.deadlockEscalated = false
	s.saveLocked()
	s.scheduleLocked()
	s.flushWakesSoonLocked()
	return nil
}

// UpdateNodes patches pending or stalled nodes: prompt, priority,
// dependencies, verification, files. Running and verified nodes are
// rejected so the mastermind must retry (or remove) instead of mutating
// history.
func (s *Supervisor) UpdateNodes(inputs []PlanNodeInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, in := range inputs {
		n := s.find(in.ID)
		if n == nil {
			return fmt.Errorf("node %q not found", in.ID)
		}
		if n.State != NodePending && n.State != NodeStalled {
			return fmt.Errorf("node %q is %s; only pending or stalled nodes can be updated", n.ID, n.State)
		}
	}
	for _, in := range inputs {
		for _, dep := range in.DependsOn {
			if dep == in.ID {
				return fmt.Errorf("node %q depends on itself", in.ID)
			}
			if s.find(dep) == nil {
				return fmt.Errorf("node %q depends on unknown node %q", in.ID, dep)
			}
		}
	}
	before := make([]*PlanNode, len(inputs))
	for i, in := range inputs {
		before[i] = s.find(in.ID).Clone()
	}
	for _, in := range inputs {
		n := s.find(in.ID)
		if strings.TrimSpace(in.Title) != "" {
			n.Title = in.Title
		}
		if strings.TrimSpace(in.Prompt) != "" {
			n.Prompt = in.Prompt
		}
		if in.Agent == "coder" || in.Agent == "task" {
			n.Agent = in.Agent
		}
		if in.DependsOn != nil {
			n.DependsOn = slices.Clone(in.DependsOn)
		}
		if in.Priority != 0 {
			n.Priority = in.Priority
		}
		if in.VerifyCmd != "" {
			n.VerifyCmd = in.VerifyCmd
		}
		if in.VerifyCriteria != "" {
			n.VerifyCriteria = in.VerifyCriteria
		}
		if in.Files != nil {
			n.Files = slices.Clone(in.Files)
		}
		if in.Timeout != 0 {
			n.TimeoutSecs = in.Timeout
		}
		if in.MaxRestarts != nil {
			mr := *in.MaxRestarts
			n.MaxRestarts = &mr
		}
		if n.State == NodeStalled {
			n.State = NodePending
			n.Restarts = 0
		}
		n.UpdatedAt = s.now()
	}
	if cyc := s.findCycleLocked(); cyc != "" {
		for _, snap := range before {
			if n := s.find(snap.ID); n != nil {
				*n = *snap
			}
		}
		return fmt.Errorf("the update would create a dependency cycle involving %s", cyc)
	}
	s.plan.DeathEscalated = false
	s.deadlockEscalated = false
	s.saveLocked()
	s.scheduleLocked()
	s.flushWakesSoonLocked()
	return nil
}

// RetryNodes returns stalled (or removed-edge leftovers) to pending
// with a fresh restart budget.
func (s *Supervisor) RetryNodes(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		n := s.find(id)
		if n == nil || (n.State != NodeStalled && n.State != NodeVerified) {
			continue
		}
		n.State = NodePending
		n.Restarts = 0
		n.LastEvidence = ""
		n.UpdatedAt = s.now()
	}
	s.plan.DeathEscalated = false
	s.deadlockEscalated = false
	s.saveLocked()
	s.scheduleLocked()
}

// RemoveNodes drops nodes. Running workers are killed. Dependents of
// removed nodes stay pending; the deadlock escalation will explain that
// they are blocked on a missing dependency.
func (s *Supervisor) RemoveNodes(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) == 0 {
		return fmt.Errorf("remove requires node_ids")
	}
	for _, id := range ids {
		if s.find(id) == nil {
			return fmt.Errorf("node %q not found", id)
		}
	}
	s.plan.Nodes = slices.DeleteFunc(s.plan.Nodes, func(n *PlanNode) bool {
		if !slices.Contains(ids, n.ID) {
			return false
		}
		s.killLocked(n)
		return true
	})
	s.deadlockEscalated = false
	s.saveLocked()
	s.scheduleLocked()
	return nil
}

// Pause stops scheduling and kills running workers; nodes return to
// pending and resume picks them up again.
func (s *Supervisor) Pause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plan.Paused = true
	for _, n := range s.plan.Nodes {
		if n.State == NodeRunning || n.State == NodeVerifying {
			s.killLocked(n)
			n.State = NodePending
			n.UpdatedAt = s.now()
		}
	}
	s.saveLocked()
}

// Resume re-enables scheduling.
func (s *Supervisor) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plan.Paused = false
	s.plan.CompletedNotified = false
	s.plan.DeathEscalated = false
	s.deadlockEscalated = false
	s.saveLocked()
	s.scheduleLocked()
}

// SetLimits lowers the parallel cap (clamped by config) and sets a
// total spend budget in USD for the mastermind session. Zero values
// mean "leave unchanged".
func (s *Supervisor) SetLimits(maxParallel int, budgetUSD float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxParallel > 0 {
		s.plan.MaxParallel = maxParallel
	}
	if budgetUSD > 0 {
		s.plan.BudgetUSD = budgetUSD
	}
	s.saveLocked()
	s.scheduleLocked()
}

// Status renders the full supervisor state, the same rendering that
// rides along on wake-up reports.
func (s *Supervisor) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.plan.Nodes) == 0 {
		return "No plan for this session yet. Create one with action=add."
	}
	return s.renderLocked()
}

// NodeDone reports a worker launch's outcome for the given attempt.
// Stale reports (killed or restarted nodes) are ignored.
func (s *Supervisor) NodeDone(nodeID string, attempt int, claim string, runErr error, evidence VerifyEvidence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.find(nodeID)
	if n == nil || n.State != NodeRunning || n.Attempts != attempt {
		return
	}
	s.stopRunLocked(nodeID)
	n.UpdatedAt = s.now()

	switch {
	case runErr != nil:
		s.failLocked(n, runErr.Error())
	case n.VerifyCmd != "" && !evidence.Passed():
		n.LastResult = claim
		n.State = NodeStalled
		n.LastEvidence = fmt.Sprintf("verify_cmd %q exited %d:\n%s", evidence.Cmd, evidence.ExitCode, evidence.Output)
		s.enqueueWakeLocked(fmt.Sprintf("Node %q claimed completion but verify_cmd failed (exit %d). Adjudicate: update the node, retry it, or remove it.", n.ID, evidence.ExitCode))
	case n.VerifyCriteria != "":
		n.LastResult = claim
		n.State = NodeVerifying
		n.LastEvidence = evidence.Output
		s.saveLocked()
		s.scheduleLocked() // the claim freed a slot
		s.hooks.launcher.VerifyNode(s, n.Clone(), s.plan.SessionID, evidence)
	default:
		n.LastResult = claim
		n.State = NodeVerified
		n.UpdatedAt = s.now()
		s.enqueueWakeLocked(fmt.Sprintf("Node %q finished and verified (no evidence configured beyond the claim).", n.ID))
	}

	s.saveLocked()
	s.scheduleLocked()
	s.flushWakesSoonLocked()
}

// ReportVerdict finalizes verification for a node in NodeVerifying.
func (s *Supervisor) ReportVerdict(nodeID string, attempt int, verified bool, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.find(nodeID)
	if n == nil || n.State != NodeVerifying || n.Attempts != attempt {
		return
	}
	n.UpdatedAt = s.now()
	if verified {
		n.State = NodeVerified
		s.enqueueWakeLocked(fmt.Sprintf("Node %q verified by the verifier: %s", n.ID, firstLine(reason)))
	} else {
		n.State = NodeStalled
		n.LastEvidence = strings.TrimSpace(n.LastEvidence + "\nverifier verdict: " + reason)
		s.enqueueWakeLocked(fmt.Sprintf("Node %q stalled: the verifier rejected it (%s). Adjudicate: update, retry, or remove.", n.ID, firstLine(reason)))
	}
	s.saveLocked()
	s.scheduleLocked()
	s.flushWakesSoonLocked()
}

// NoteTurnEnded is called by the coordinator whenever the mastermind's
// own turn ends. A clean end clears the death streak; an errored end
// (a dead provider stream killing the turn) schedules a recovery
// wake-up so the plan continues even though the LLM died mid-epic.
func (s *Supervisor) NoteTurnEnded(errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if errText == "" {
		s.deaths = 0
		s.plan.DeathEscalated = false
		return
	}
	if s.plan.Paused || s.allDoneLocked() {
		return
	}
	s.deaths++
	if s.deaths <= maxMastermindDeaths {
		s.enqueueWakeLocked(fmt.Sprintf("Your previous turn ended with an error: %s. The Supervisor kept the plan alive; continue from the state above.", firstLine(errText)))
		s.flushWakesSoonLocked()
		return
	}
	if !s.plan.DeathEscalated {
		s.plan.DeathEscalated = true
		s.enqueueWakeLocked(fmt.Sprintf("The mastermind turn has died %d times in a row (last error: %s). The Supervisor is escalating once and will stop restarting the turn until you interact.", s.deaths, firstLine(errText)))
		s.saveLocked()
		s.flushWakesSoonLocked()
	}
}

// maxMastermindDeaths bounds automatic recovery wakes for the
// mastermind's own turn before the Supervisor gives up and waits for
// the user.
const maxMastermindDeaths = 3

// --- scheduling internals ---

func (s *Supervisor) find(id string) *PlanNode {
	for _, n := range s.plan.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// saveLocked persists the plan; a failed write is logged, never fatal:
// in-memory state stays authoritative for the live session.
func (s *Supervisor) saveLocked() {
	if s.store == nil {
		return
	}
	if err := s.store.save(s.plan); err != nil {
		slog.Error("Failed to persist plan state", "session", s.plan.SessionID, "error", err)
	}
}

func (s *Supervisor) limitLocked() int {
	max := defaultMaxParallelSubagents
	if s.hooks.maxParallel != nil {
		max = s.hooks.maxParallel()
	}
	if max < 1 {
		max = defaultMaxParallelSubagents
	}
	if s.plan.MaxParallel > 0 && s.plan.MaxParallel < max {
		max = s.plan.MaxParallel
	}
	return max
}

func (s *Supervisor) readyLocked(n *PlanNode) bool {
	if n.State != NodePending {
		return false
	}
	for _, dep := range n.DependsOn {
		d := s.find(dep)
		if d == nil || d.State != NodeVerified {
			return false
		}
	}
	return s.filesFreeLocked(n)
}

// filesFreeLocked enforces the disjoint-files rule: two concurrently
// running writable nodes may not declare overlapping paths.
func (s *Supervisor) filesFreeLocked(n *PlanNode) bool {
	if len(n.Files) == 0 || n.Agent == "task" {
		return true
	}
	for _, r := range s.plan.Nodes {
		if r.State != NodeRunning || r.Agent == "task" || len(r.Files) == 0 {
			continue
		}
		if pathsOverlap(n.Files, r.Files) {
			return false
		}
	}
	return true
}

func pathsOverlap(a, b []string) bool {
	for _, pa := range a {
		ca := filepath.Clean(pa)
		for _, pb := range b {
			cb := filepath.Clean(pb)
			if ca == cb ||
				strings.HasPrefix(ca, cb+string(filepath.Separator)) ||
				strings.HasPrefix(cb, ca+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// scheduleLocked launches ready nodes (highest priority first) while
// slots remain, then runs the invariant checks: budget and deadlock.
func (s *Supervisor) scheduleLocked() {
	if !s.plan.Paused {
		limit := s.limitLocked()
		for len(s.running) < limit {
			var next *PlanNode
			for _, n := range s.plan.Nodes {
				if !s.readyLocked(n) {
					continue
				}
				if next == nil || n.Priority > next.Priority {
					next = n
				}
			}
			if next == nil {
				break
			}
			s.launchLocked(next)
		}
	}
	s.checkBudgetLocked()
	s.checkDeadlockLocked()
	s.checkCompleteLocked()
}

func (s *Supervisor) launchLocked(n *PlanNode) {
	n.Attempts++
	n.State = NodeRunning
	n.ChildSessionID = planChildID(s.plan.SessionID, n.ID, n.Attempts)
	n.UpdatedAt = s.now()
	s.running[n.ID] = &nodeRun{}
	s.saveLocked()
	s.hooks.launcher.LaunchNode(s, n.Clone(), s.plan.SessionID)
	if n.TimeoutSecs > 0 {
		if run, ok := s.running[n.ID]; ok {
			id, attempt := n.ID, n.Attempts
			run.timer = time.AfterFunc(time.Duration(n.TimeoutSecs)*time.Second, func() {
				s.onTimeout(id, attempt)
			})
		}
	}
}

// AttachCancel lets the launcher register the context canceller for its
// in-flight node so kills and pauses can stop it.
func (s *Supervisor) AttachCancel(nodeID string, attempt int, cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.find(nodeID)
	if n == nil || n.State != NodeRunning || n.Attempts != attempt {
		return
	}
	if run, ok := s.running[nodeID]; ok {
		run.cancel = cancel
	}
}

func (s *Supervisor) onTimeout(nodeID string, attempt int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.find(nodeID)
	if n == nil || n.State != NodeRunning || n.Attempts != attempt {
		return
	}
	reason := fmt.Sprintf("watchdog timeout after %ds", n.TimeoutSecs)
	s.stopRunLocked(nodeID)
	s.failLocked(n, reason)
	s.saveLocked()
	s.scheduleLocked()
	s.flushWakesSoonLocked()
}

// killLocked cancels a node's live run without touching its state.
func (s *Supervisor) killLocked(n *PlanNode) {
	s.stopRunLocked(n.ID)
}

func (s *Supervisor) stopRunLocked(nodeID string) {
	if run, ok := s.running[nodeID]; ok {
		if run.timer != nil {
			run.timer.Stop()
		}
		if run.cancel != nil {
			run.cancel()
		}
		delete(s.running, nodeID)
	}
}

func (s *Supervisor) failLocked(n *PlanNode, reason string) {
	n.UpdatedAt = s.now()
	if n.Restarts < n.MaxRestartsOrDefault() {
		n.Restarts++
		n.State = NodePending
		s.enqueueWakeLocked(fmt.Sprintf("Node %q failed (%s); auto-restarting (restart %d/%d).", n.ID, firstLine(reason), n.Restarts, n.MaxRestartsOrDefault()))
		return
	}
	n.State = NodeStalled
	n.LastEvidence = strings.TrimSpace(n.LastEvidence + "\n" + reason)
	s.enqueueWakeLocked(fmt.Sprintf("Node %q failed (%s) and its restart budget is exhausted. Adjudicate: update, retry, or remove it.", n.ID, firstLine(reason)))
}

func (s *Supervisor) checkBudgetLocked() {
	if s.plan.BudgetUSD <= 0 || s.hooks.spent == nil || s.plan.Paused {
		return
	}
	if s.hooks.spent() <= s.plan.BudgetUSD {
		return
	}
	s.plan.Paused = true
	for _, n := range s.plan.Nodes {
		if n.State == NodeRunning || n.State == NodeVerifying {
			s.killLocked(n)
			n.State = NodePending
		}
	}
	s.enqueueWakeLocked(fmt.Sprintf("Spend budget exhausted ($%.2f of $%.2f). The plan is paused: raise the budget with set_limit, resume it, or mark it done.", s.hooks.spent(), s.plan.BudgetUSD))
}

func (s *Supervisor) checkDeadlockLocked() {
	if s.plan.Paused || s.allDoneLocked() {
		return
	}
	if len(s.running) > 0 {
		return
	}
	var blocked []string
	pending := 0
	for _, n := range s.plan.Nodes {
		if n.State != NodePending {
			continue
		}
		pending++
		if !s.readyLocked(n) {
			blocked = append(blocked, fmt.Sprintf("%s (%s)", n.ID, s.blockedWhyLocked(n)))
		}
	}
	if pending == 0 || len(blocked) != pending {
		return
	}
	if s.deadlockEscalated {
		return
	}
	s.deadlockEscalated = true

	s.enqueueWakeLocked(fmt.Sprintf("Plan deadlocked: every remaining node is blocked: %s. Rewire the plan (update dependencies, retry a stalled node, or add the missing prerequisite).", strings.Join(blocked, ", ")))
}

func (s *Supervisor) blockedWhyLocked(n *PlanNode) string {
	for _, dep := range n.DependsOn {
		d := s.find(dep)
		if d == nil {
			return fmt.Sprintf("missing dependency %s", dep)
		}
		if d.State != NodeVerified {
			return fmt.Sprintf("dependency %s is %s", dep, d.State)
		}
	}
	if !s.filesFreeLocked(n) {
		return "waiting for a running node with overlapping files"
	}
	return "waiting for a slot"
}

func (s *Supervisor) checkCompleteLocked() {
	if !s.allDoneLocked() || s.plan.CompletedNotified {
		return
	}
	s.plan.CompletedNotified = true
	s.enqueueWakeLocked(fmt.Sprintf("Plan complete: all %d nodes verified.", len(s.plan.Nodes)))
}

func (s *Supervisor) allDoneLocked() bool {
	if len(s.plan.Nodes) == 0 {
		return false
	}
	for _, n := range s.plan.Nodes {
		if n.State != NodeVerified {
			return false
		}
	}
	return true
}

// findCycleLocked returns any node ID inside a dependency cycle, or "".
func (s *Supervisor) findCycleLocked() string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var cyc string
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = gray
		n := s.find(id)
		if n != nil {
			for _, dep := range n.DependsOn {
				switch color[dep] {
				case gray:
					cyc = dep
					color[id] = black
					return true
				case white:
					if visit(dep) {
						color[id] = black
						return true
					}
				}
			}
		}
		color[id] = black
		return false
	}
	for _, n := range s.plan.Nodes {
		if color[n.ID] == white {
			if visit(n.ID) {
				return cyc
			}
		}
	}
	return ""
}

// --- wake coalescing ---

func (s *Supervisor) enqueueWakeLocked(text string) {
	s.wakeEvts = append(s.wakeEvts, text)
}

// flushWakesSoonLocked schedules a debounced delivery of all pending
// events as one report turn.
func (s *Supervisor) flushWakesSoonLocked() {
	if s.wakeTimr != nil || len(s.wakeEvts) == 0 {
		return
	}
	s.wakeTimr = time.AfterFunc(s.debounce, s.FlushWakes)
}

// FlushWakes delivers the pending events to the mastermind immediately.
// Exported so tests (and the coordinator's shutdown path) can force it.
func (s *Supervisor) FlushWakes() {
	s.mu.Lock()
	if s.wakeTimr != nil {
		s.wakeTimr.Stop()
		s.wakeTimr = nil
	}
	if len(s.wakeEvts) == 0 {
		s.mu.Unlock()
		return
	}
	report := s.renderLocked() + "\n\nSupervisor events:\n- " + strings.Join(s.wakeEvts, "\n- ") + "\n\nAct only where judgment is needed; the scheduler handles the rest."
	s.wakeEvts = nil
	s.mu.Unlock()
	if s.hooks.wake != nil {
		s.hooks.wake(report)
	}
}

// --- rendering ---

func (s *Supervisor) renderLocked() string {
	var sb strings.Builder
	spent := 0.0
	if s.hooks.spent != nil {
		spent = s.hooks.spent()
	}
	fmt.Fprintf(&sb, "Plan (max_parallel %d, budget $%.2f, spent $%.2f)", s.limitLocked(), s.plan.BudgetUSD, spent)
	if s.plan.Paused {
		sb.WriteString(" [PAUSED]")
	}
	sb.WriteString("\n")
	var ready, stalled, verified []string
	for _, n := range s.plan.Nodes {
		line := fmt.Sprintf("  [%s] %-10s %s", n.ID, n.State, n.Title)
		switch n.State {
		case NodeRunning:
			fmt.Fprintf(&sb, "%s (attempt %d, restarts %d/%d, child %s)\n", line, n.Attempts, n.Restarts, n.MaxRestartsOrDefault(), n.ChildSessionID)
		case NodeVerifying:
			fmt.Fprintf(&sb, "%s (verifying)\n", line)
		case NodeStalled:
			stalled = append(stalled, n.ID)
			fmt.Fprintf(&sb, "%s -- STALLED: %s\n", line, firstLine(n.LastEvidence))
		case NodeVerified:
			verified = append(verified, n.ID)
			sb.WriteString(line + "\n")
		default:
			if s.readyLocked(n) {
				ready = append(ready, n.ID)
				sb.WriteString(line + " (ready)\n")
			} else {
				fmt.Fprintf(&sb, "%s -- blocked: %s\n", line, s.blockedWhyLocked(n))
			}
		}
	}
	fmt.Fprintf(&sb, "Slots: running %d/%d [%s]; ready [%s]; verified %d; stalled [%s]",
		len(s.running), s.limitLocked(),
		strings.Join(s.runningIDs(), ", "),
		strings.Join(ready, ", "),
		len(verified),
		strings.Join(stalled, ", "),
	)
	return sb.String()
}

func (s *Supervisor) runningIDs() []string {
	var ids []string
	for _, n := range s.plan.Nodes {
		if n.State == NodeRunning {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

func firstLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
