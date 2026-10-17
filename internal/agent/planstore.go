package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// PlanNodeState is the lifecycle state of a plan node.
type PlanNodeState string

const (
	// NodePending means dependencies are not all verified yet, or a
	// slot or the disjoint-files rule is holding the node back.
	NodePending PlanNodeState = "pending"
	// NodeRunning means a worker child session is executing the node
	// and it holds a scheduling slot.
	NodeRunning PlanNodeState = "running"
	// NodeVerifying means the worker finished (a claim) and evidence is
	// being collected. The slot is already free: a claim frees a slot,
	// only verification unlocks dependents.
	NodeVerifying PlanNodeState = "verifying"
	// NodeVerified means verified evidence backs the completion.
	NodeVerified PlanNodeState = "verified"
	// NodeStalled means the node finished or died but the evidence
	// disagrees with the claim, or the restart budget ran out. Only the
	// mastermind can adjudicate (update/retry/remove).
	NodeStalled PlanNodeState = "stalled"
)

// Plan is the persisted orchestration state for one mastermind session.
// Depth 0 is the main session; a plan created by a supervisor node
// carries its parent linkage so its completion can close the node that
// spawned it.
type Plan struct {
	SessionID   string      `json:"session_id"`
	MaxParallel int         `json:"max_parallel,omitempty"`
	BudgetUSD   float64     `json:"budget_usd,omitempty"`
	Paused      bool        `json:"paused,omitempty"`
	Nodes       []*PlanNode `json:"nodes"`

	// Depth is this plan's level in the supervision tree. The
	// mastermind's own plan is depth 0; a plan authored by a supervisor
	// node is depth 1, and so on.
	Depth int `json:"depth,omitempty"`
	// Parent linkage, set only for sub-plans (Depth > 0). When the
	// sub-plan completes, the parent supervisor is notified so the
	// supervisor node that owns it is marked done.
	ParentSessionID   string `json:"parent_session_id,omitempty"`
	ParentNodeID      string `json:"parent_node_id,omitempty"`
	ParentNodeAttempt int    `json:"parent_node_attempt,omitempty"`

	// completedNotified guards the single "plan complete" wake-up.
	CompletedNotified bool `json:"completed_notified,omitempty"`
	// deathEscalated guards the single "mastermind keeps dying"
	// escalation so a broken provider cannot loop the wake-up.
	DeathEscalated bool `json:"death_escalated,omitempty"`
}

// PlanNode is one unit of work in the DAG.
type PlanNode struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Prompt         string   `json:"prompt"`
	Agent          string   `json:"agent,omitempty"` // "coder" (default), "task", or "supervisor"
	DependsOn      []string `json:"depends_on,omitempty"`
	Priority       int      `json:"priority,omitempty"`
	VerifyCmd      string   `json:"verify_cmd,omitempty"`
	VerifyCriteria string   `json:"verify_criteria,omitempty"`
	Files          []string `json:"files,omitempty"`
	TimeoutSecs    int      `json:"timeout,omitempty"`
	MaxRestarts    *int     `json:"max_restarts,omitempty"`

	State          PlanNodeState `json:"state"`
	Attempts       int           `json:"attempts,omitempty"`
	Restarts       int           `json:"restarts,omitempty"`
	ChildSessionID string        `json:"child_session_id,omitempty"`
	// LastResult is the worker's completion claim, never a verified
	// fact. LastEvidence is verify_cmd output and/or the verifier's
	// verdict reason.
	LastResult   string    `json:"last_result,omitempty"`
	LastEvidence string    `json:"last_evidence,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Clone deep-copies the node so the launcher goroutine can read it
// while the supervisor keeps mutating the original.
func (n *PlanNode) Clone() *PlanNode {
	cp := *n
	cp.DependsOn = append([]string(nil), n.DependsOn...)
	cp.Files = append([]string(nil), n.Files...)
	return &cp
}

// MaxParallelOrDefault resolves the plan's parallel cap before config
// clamping; 0 means the default.
func (p *Plan) MaxParallelOrDefault() int {
	if p.MaxParallel > 0 {
		return p.MaxParallel
	}
	return defaultMaxParallelSubagents
}

// MaxRestartsOrDefault resolves the node's restart budget.
func (n *PlanNode) MaxRestartsOrDefault() int {
	if n.MaxRestarts == nil {
		return DefaultMaxRestarts
	}
	return *n.MaxRestarts
}

// VerifyEvidence is the outcome of a node's verify_cmd.
type VerifyEvidence struct {
	Cmd      string
	ExitCode int
	Output   string
}

// Passed reports whether the command exited zero. Nodes without
// verify_cmd never collect evidence; the zero value is not meaningful
// for them, so callers check the node's fields first.
func (e VerifyEvidence) Passed() bool { return e.ExitCode == 0 }

// planChildID derives the deterministic child-session ID for one
// attempt of one node, so the chat/session UI can attribute it.
func planChildID(mainSessionID, nodeID string, attempt int) string {
	sum := sha256.Sum256([]byte(mainSessionID))
	return fmt.Sprintf("plan-%x-%s-a%d", sum[:6], nodeID, attempt)
}

// PlansDir returns the directory plan files live in under the data
// directory. Exported so frontends can locate plan state for rendering.
func PlansDir(dataDir string) string {
	return filepath.Join(dataDir, "plans")
}

// LoadPlanFile reads the persisted plan for a session, or returns nil,
// nil when none exists. Exported for frontends that render plan state
// outside the agent package.
func LoadPlanFile(dataDir, sessionID string) (*Plan, error) {
	return (&planStore{dir: PlansDir(dataDir)}).load(sessionID)
}

// planStore persists one Plan JSON document per mastermind session in
// the data directory, the same location background jobs use for their
// spill files. Writes are atomic (temp file + rename).
type planStore struct {
	dir string
}

func (s *planStore) path(sessionID string) string {
	return filepath.Join(s.dir, sessionID+".json")
}

// load returns nil, nil when no plan exists for the session yet.
func (s *planStore) load(sessionID string) (*Plan, error) {
	data, err := os.ReadFile(s.path(sessionID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	return &plan, nil
}

func (s *planStore) save(plan *Plan) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create plans directory: %w", err)
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(plan.SessionID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(plan.SessionID))
}
