package chat

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func renderJob(t *testing.T, ctx ToolRenderer, tc message.ToolCall, res *message.ToolResult) string {
	t.Helper()
	sty := styles.CharmtonePantera()
	return ansi.Strip(ctx.RenderTool(&sty, 120, &ToolRenderOpts{
		ToolCall: tc,
		Result:   res,
		Status:   ToolStatusSuccess,
	}))
}

// TestJobOutputShowsPIDStateAndCommand verifies the job_output header shows
// the real PID and state, and the body always shows the exact command even
// when the model supplied a short description.
func TestJobOutputShowsPIDStateAndCommand(t *testing.T) {
	t.Parallel()

	out := renderJob(t,
		&JobOutputToolRenderContext{},
		message.ToolCall{ID: "o1", Name: "job_output", Input: `{"shell_id":"202"}`, Finished: true},
		&message.ToolResult{
			ToolCallID: "o1",
			Content:    "Status: running\n\nlistening on :3000",
			Metadata:   `{"shell_id":"202","command":"npm run dev","description":"dev server","done":false,"pid":5142}`,
		},
	)
	require.Contains(t, out, "PID 5142")
	require.Contains(t, out, "running")
	require.Contains(t, out, "Command: npm run dev", "exact command must be visible")
	require.Contains(t, out, "dev server")
}

// TestJobOutputExitState verifies a finished job reports its exit code.
func TestJobOutputExitState(t *testing.T) {
	t.Parallel()

	out := renderJob(t,
		&JobOutputToolRenderContext{},
		message.ToolCall{ID: "o2", Name: "job_output", Input: `{"shell_id":"202"}`, Finished: true},
		&message.ToolResult{
			ToolCallID: "o2",
			Content:    "Status: completed\n\nboom",
			Metadata:   `{"shell_id":"202","command":"make build","done":true,"exit_code":2,"pid":5150}`,
		},
	)
	require.Contains(t, out, "exit 2")
	require.Contains(t, out, "Command: make build")
}

// TestJobOutputWithoutPIDShowsNoIdentifier verifies builtin-only jobs, which
// have no OS process, show no identifier at all.
func TestJobOutputWithoutPIDShowsNoIdentifier(t *testing.T) {
	t.Parallel()

	out := renderJob(t,
		&JobOutputToolRenderContext{},
		message.ToolCall{ID: "o3", Name: "job_output", Input: `{"shell_id":"202"}`, Finished: true},
		&message.ToolResult{
			ToolCallID: "o3",
			Content:    "Status: running\n\nout",
			Metadata:   `{"shell_id":"202","command":"echo hi","done":false}`,
		},
	)
	require.NotContains(t, out, "PID")
	require.Contains(t, out, "Command: echo hi")
	require.Contains(t, out, "running")
}

// TestJobOutputNotFoundStillIdentifiesCommandLocation verifies the not-found
// error path no longer collapses to a bare "Job (Output)" line: the body
// carries the error text.
func TestJobOutputNotFoundStillIdentifiesCommandLocation(t *testing.T) {
	t.Parallel()

	out := renderJob(t,
		&JobOutputToolRenderContext{},
		message.ToolCall{ID: "o4", Name: "job_output", Input: `{"shell_id":"202"}`, Finished: true},
		&message.ToolResult{ToolCallID: "o4", Content: "background shell not found: 202", IsError: true},
	)
	require.NotContains(t, out, "PID")
	require.Contains(t, out, "background shell not found: 202")
}

// TestBashBackgroundStartShowsPIDAndCommand verifies the start line for a
// backgrounded bash command shows the PID, state, and command.
func TestBashBackgroundStartShowsPIDAndCommand(t *testing.T) {
	t.Parallel()

	out := renderJob(t,
		&BashToolRenderContext{},
		message.ToolCall{ID: "b1", Name: "bash", Input: `{"command":"npm run dev","description":"dev server","run_in_background":true}`, Finished: true},
		&message.ToolResult{
			ToolCallID: "b1",
			Content:    "Background shell started with ID: 202",
			Metadata:   `{"description":"dev server","background":true,"shell_id":"202","pid":5142}`,
		},
	)
	require.Contains(t, out, "PID 5142")
	require.Contains(t, out, "running")
	require.Contains(t, out, "Command: npm run dev")
}
