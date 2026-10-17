package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
)

//go:embed templates/plan_tool.md
var planToolDescription string

// PlanToolName is the mastermind's command surface onto the Supervisor.
const PlanToolName = "plan"

// PlanNodeInput is one node as authored by the mastermind.
type PlanNodeInput struct {
	ID             string   `json:"id" description:"Short unique node id: lowercase alnum with - and _, max 32 chars, e.g. store-flag"`
	Title          string   `json:"title,omitempty" description:"One-line summary of the node, shown in the plan panel"`
	Prompt         string   `json:"prompt,omitempty" description:"Full self-contained instructions for the worker executing this node"`
	Agent          string   `json:"agent,omitempty" description:"Worker toolset: coder (writable, default), task (read-only research), or supervisor (runs its own sub-plan of simpler nodes; only for genuinely large sub-tasks, max depth 2)"`
	DependsOn      []string `json:"depends_on,omitempty" description:"Node ids that must be verified before this node may start"`
	Priority       int      `json:"priority,omitempty" description:"Higher launches first when several nodes are ready (default 0)"`
	VerifyCmd      string   `json:"verify_cmd,omitempty" description:"Shell command run after the worker finishes; a non-zero exit stalls the node for adjudication"`
	VerifyCriteria string   `json:"verify_criteria,omitempty" description:"Acceptance criteria judged by a read-only verifier child after verify_cmd passes; the worker's report alone never verifies a node"`
	Files          []string `json:"files,omitempty" description:"Paths this node writes; concurrent writable nodes with overlapping paths are serialized"`
	Timeout        int      `json:"timeout,omitempty" description:"Watchdog: kill and fail the node after this many seconds of running (0 = no watchdog)"`
	MaxRestarts    *int     `json:"max_restarts,omitempty" description:"Automatic restarts when the worker dies (default 1) before the node stalls for adjudication"`
	Model          string   `json:"model,omitempty" description:"Model override for the worker: provider/model, or a bare model id on the current provider; empty inherits the agent model"`
	Temperature    *float64 `json:"temperature,omitempty" description:"Sampling temperature override for the worker, 0-1; empty inherits the model config"`
	ContextWindow  int64    `json:"context_window,omitempty" description:"Context window override for the worker's model in tokens, for local models that report no size; 0 inherits"`
}

// PlanParams is the plan tool input.
type PlanParams struct {
	Action      string          `json:"action" description:"One of: add, update, retry, remove, pause, resume, set_limit, status"`
	Nodes       []PlanNodeInput `json:"nodes,omitempty" description:"Nodes for add (full definitions) or update (only the fields to change; node must be pending or stalled)"`
	NodeIDs     []string        `json:"node_ids,omitempty" description:"Node ids for retry or remove"`
	MaxParallel int             `json:"max_parallel,omitempty" description:"For set_limit: new parallel cap; may only lower the configured maximum"`
	BudgetUSD   float64         `json:"budget_usd,omitempty" description:"For set_limit: total spend budget in USD; exceeding it pauses the plan"`
}

func (c *coordinator) planTool() (fantasy.AgentTool, error) {
	return fantasy.NewParallelAgentTool(
		PlanToolName,
		planToolDescription,
		func(ctx context.Context, params PlanParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}
			sup, err := c.supervisorFor(sessionID)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			switch strings.ToLower(strings.TrimSpace(params.Action)) {
			case "add":
				if err := sup.AddNodes(params.Nodes); err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
			case "update":
				if err := sup.UpdateNodes(params.Nodes); err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
			case "retry":
				sup.RetryNodes(params.NodeIDs)
			case "remove":
				if err := sup.RemoveNodes(params.NodeIDs); err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
			case "pause":
				sup.Pause()
			case "resume":
				sup.Resume()
			case "set_limit":
				sup.SetLimits(params.MaxParallel, params.BudgetUSD)
			case "status":
			default:
				return fantasy.NewTextErrorResponse(fmt.Sprintf("unknown action %q (expected add, update, retry, remove, pause, resume, set_limit, or status)", params.Action)), nil
			}
			return fantasy.NewTextResponse(sup.Status()), nil
		},
	), nil
}
