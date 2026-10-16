package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/config"
)

// verifyCmdTimeout bounds a single node verify_cmd run.
const verifyCmdTimeout = 10 * time.Minute

// verifyTimeout bounds a verifier child request.
const verifyTimeout = 5 * time.Minute

// evidenceTailBytes is how much verify_cmd output is kept as evidence.
const evidenceTailBytes = 4096

//go:embed templates/plan_worker_footer.md
var planWorkerFooter string

//go:embed templates/plan_verifier.md
var planVerifierTmpl string

// supervisorFor returns the orchestration supervisor for a mastermind
// session, loading (or first-time creating) its persisted plan. The
// Supervisor is a pure-code actor: it owns scheduling; the coordinator
// only provides the child-session plumbing it drives.
func (c *coordinator) supervisorFor(sessionID string) (*Supervisor, error) {
	if !c.cfg.Config().Options.Orchestration {
		return nil, errors.New("orchestration is disabled: set `option orchestration true` in the config")
	}
	if c.supervisors == nil {
		return nil, errors.New("orchestration is unavailable in this coordinator")
	}
	if sup, ok := c.supervisors.Get(sessionID); ok {
		return sup, nil
	}
	store := &planStore{dir: filepath.Join(c.cfg.Config().Options.DataDirectory, "plans")}
	plan, err := store.load(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load plan: %w", err)
	}
	if plan == nil {
		plan = &Plan{SessionID: sessionID}
	}
	sup := newSupervisor(plan, store, supervisorHooks{
		launcher: &coordinatorLauncher{c: c},
		wake: func(report string) {
			c.deliverPlanReport(sessionID, report)
		},
		spent: func() float64 {
			sess, err := c.sessions.Get(context.WithoutCancel(c.baseCtx), sessionID)
			if err != nil {
				return 0
			}
			return sess.Cost
		},
		maxParallel: func() int {
			if n := c.cfg.Config().Options.MaxParallelSubagents; n > 0 {
				return n
			}
			return defaultMaxParallelSubagents
		},
	})
	c.supervisors.Set(sessionID, sup)
	return sup, nil
}

// deliverPlanReport wakes the mastermind with a Supervisor report,
// reusing the background-job path: the in-flight turn is interrupted
// (or the report queues) and delivered as a new turn on the mastermind
// session.
func (c *coordinator) deliverPlanReport(sessionID, report string) {
	if _, err := c.ReportBackgroundJob(context.WithoutCancel(c.baseCtx), sessionID, report); err != nil {
		slog.Warn("Failed to deliver orchestration report", "session", sessionID, "error", err)
	}
}

// noteMainRunEnd feeds a mastermind turn's outcome to its supervisor so
// a turn killed by a provider error gets restarted from persisted
// state instead of dying with the plan. A user cancel is not a death.
func (c *coordinator) noteMainRunEnd(sessionID string, runErr error) {
	if c.supervisors == nil {
		return
	}
	sup, ok := c.supervisors.Get(sessionID)
	if !ok {
		return
	}
	switch {
	case runErr == nil:
		sup.NoteTurnEnded("")
	case errors.Is(runErr, context.Canceled):
		// The user (or a preemption) ended the turn, not the provider.
	default:
		sup.NoteTurnEnded(runErr.Error())
	}
}

// coordinatorLauncher runs plan nodes as real child sessions, the same
// mechanism the agent tool uses for synchronous subagents, with the
// await removed so the Supervisor keeps scheduling.
type coordinatorLauncher struct {
	c *coordinator
}

func (l *coordinatorLauncher) LaunchNode(sup *Supervisor, node *PlanNode, mainSessionID string) {
	go l.runNode(sup, node, mainSessionID)
}

func (l *coordinatorLauncher) VerifyNode(sup *Supervisor, node *PlanNode, mainSessionID string, evidence VerifyEvidence) {
	go l.runVerifier(sup, node, mainSessionID, evidence)
}

func (l *coordinatorLauncher) runNode(sup *Supervisor, node *PlanNode, mainSessionID string) {
	c := l.c
	ctx, cancel := context.WithCancel(context.WithoutCancel(c.baseCtx))
	defer cancel()
	sup.AttachCancel(node.ID, node.Attempts, cancel)

	var (
		result         *fantasy.AgentResult
		runErr         error
		sessionCreated bool
	)
	worker, werr := c.buildWorkerAgent(ctx, node.Agent)
	if werr != nil {
		runErr = werr
	} else if _, serr := c.sessions.CreateTaskSession(ctx, node.ChildSessionID, mainSessionID, "plan: "+node.Title); serr != nil {
		runErr = fmt.Errorf("create worker session: %w", serr)
	} else {
		sessionCreated = true
		c.trackSubAgentRun(node.ChildSessionID, worker)
		result, runErr = c.runSessionTurn(ctx, worker, node.ChildSessionID, workerPrompt(node), false)
		c.untrackSubAgentRun(node.ChildSessionID)
	}

	if sessionCreated {
		if cerr := c.updateParentSessionCost(context.WithoutCancel(ctx), node.ChildSessionID, mainSessionID); cerr != nil {
			slog.Debug("Failed to update parent session cost", "child_session", node.ChildSessionID, "error", cerr)
		}
	}

	evidence := VerifyEvidence{Cmd: node.VerifyCmd}
	if runErr == nil && node.VerifyCmd != "" {
		evidence = runVerifyCommand(ctx, c.cfg.WorkingDir(), node.VerifyCmd)
	}
	sup.NodeDone(node.ID, node.Attempts, subAgentOutput(result), runErr, evidence)
}

func (l *coordinatorLauncher) runVerifier(sup *Supervisor, node *PlanNode, mainSessionID string, evidence VerifyEvidence) {
	c := l.c
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.baseCtx), verifyTimeout)
	defer cancel()

	childID := node.ChildSessionID + "-verify"
	var (
		result *fantasy.AgentResult
		err    error
	)
	verifier, verr := c.buildTaskAgent(ctx)
	if verr != nil {
		err = verr
	} else if _, serr := c.sessions.CreateTaskSession(ctx, childID, mainSessionID, "verify: "+node.Title); serr != nil {
		err = fmt.Errorf("create verifier session: %w", serr)
	} else {
		c.trackSubAgentRun(childID, verifier)
		result, err = c.runSessionTurn(ctx, verifier, childID, verifierPrompt(node, evidence), false)
		c.untrackSubAgentRun(childID)
	}
	if err != nil {
		sup.ReportVerdict(node.ID, node.Attempts, false, "verifier request failed: "+err.Error())
		return
	}
	verified, reason, perr := parseVerifierVerdict(subAgentOutput(result))
	if perr != nil {
		sup.ReportVerdict(node.ID, node.Attempts, false, perr.Error())
		return
	}
	sup.ReportVerdict(node.ID, node.Attempts, verified, reason)
}

// buildWorkerAgent builds a plan-node worker. Task nodes get the
// read-only task agent; coder nodes get the coder toolset minus the
// delegation tools, so workers cannot re-orchestrate their own plans.
func (c *coordinator) buildWorkerAgent(ctx context.Context, kind string) (SessionAgent, error) {
	if kind == "task" {
		return c.buildTaskAgent(ctx)
	}
	agentCfg, ok := c.cfg.Config().Agents[config.AgentCoder]
	if !ok {
		return nil, errCoderAgentNotConfigured
	}
	allowed := make([]string, 0, len(agentCfg.AllowedTools))
	for _, t := range agentCfg.AllowedTools {
		if t == AgentToolName || t == PlanToolName {
			continue
		}
		allowed = append(allowed, t)
	}
	agentCfg.AllowedTools = allowed
	workerPrompt, err := coderPrompt(prompt.WithWorkingDir(c.cfg.WorkingDir()))
	if err != nil {
		return nil, err
	}
	return c.buildAgent(ctx, workerPrompt, agentCfg, true)
}

// workerPrompt is the node prompt plus a footer framing the claim/
// verification protocol for the worker.
func workerPrompt(node *PlanNode) string {
	return node.Prompt + fmt.Sprintf("\n\n"+planWorkerFooter, node.ID)
}

// verifierPrompt asks a read-only verifier child to judge a node's
// acceptance criteria against the claim and command evidence.
func verifierPrompt(node *PlanNode, evidence VerifyEvidence) string {
	evidenceBlock := "no verify_cmd was configured"
	if evidence.Cmd != "" {
		evidenceBlock = fmt.Sprintf("verify_cmd %q exited %d.\nOutput (tail):\n%s", evidence.Cmd, evidence.ExitCode, evidence.Output)
	}
	return fmt.Sprintf(planVerifierTmpl, node.ID, node.Title, node.VerifyCriteria, node.LastResult, evidenceBlock)
}

// parseVerifierVerdict extracts the verifier's structured verdict from
// a free-form reply, using the same JSON-contract pattern as the
// grammar sidecar.
func parseVerifierVerdict(out string) (bool, string, error) {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end <= start {
		return false, "", errors.New("verifier returned no JSON object")
	}
	var verdict struct {
		Verified bool   `json:"verified"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out[start:end+1]), &verdict); err != nil {
		return false, "", fmt.Errorf("failed to parse verifier verdict: %w", err)
	}
	if strings.TrimSpace(verdict.Reason) == "" {
		verdict.Reason = "no reason given"
	}
	return verdict.Verified, verdict.Reason, nil
}

// runVerifyCommand executes a node's verify_cmd and captures the exit
// code and output tail as evidence.
func runVerifyCommand(ctx context.Context, cwd, command string) VerifyEvidence {
	cctx, cancel := context.WithTimeout(ctx, verifyCmdTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(cctx, "sh", "-c", command)
	}
	cmd.Dir = cwd

	out, err := cmd.CombinedOutput()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	tail := string(out)
	if len(tail) > evidenceTailBytes {
		tail = tail[len(tail)-evidenceTailBytes:]
	}
	return VerifyEvidence{Cmd: command, ExitCode: exit, Output: tail}
}
