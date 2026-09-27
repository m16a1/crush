package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

// TestJobOutputBoundedWaitReturnsWhileRunning verifies that wait=true with
// a timeout returns the current output once the timeout elapses instead of
// blocking until the shell exits. Without a bounded wait the only way to
// wait for a long-running command is a separate `sleep` plus polling, which
// is what callers should no longer need.
func TestJobOutputBoundedWaitReturnsWhileRunning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, t.TempDir(), nil, "sleep 60", "")
	require.NoError(t, err)
	defer bgManager.Kill(bgShell.ID)

	tool := NewJobOutputTool(t.TempDir())
	start := time.Now()
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{"shell_id":"` + bgShell.ID + `","wait":true,"timeout_ms":300}`})
	require.NoError(t, err)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 20*time.Second, "bounded wait must not block until the shell exits")
	require.Contains(t, textOf(t, resp), "Status: running")
}

// TestJobOutputWaitFalseReturnsImmediately verifies the non-blocking path
// still returns promptly.
func TestJobOutputWaitFalseReturnsImmediately(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, t.TempDir(), nil, "sleep 60", "")
	require.NoError(t, err)
	defer bgManager.Kill(bgShell.ID)

	tool := NewJobOutputTool(t.TempDir())
	start := time.Now()
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{"shell_id":"` + bgShell.ID + `"}`})
	require.NoError(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
	require.Contains(t, textOf(t, resp), "Status: running")
}

// TestJobOutputReportsProcessID verifies job_output metadata carries the
// real OS PID of an externally spawned command so the UI can show it.
func TestJobOutputReportsProcessID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(ctx, t.TempDir(), nil, "/bin/sleep 60", "")
	require.NoError(t, err)
	defer bgManager.Kill(bgShell.ID)
	require.Eventually(t, func() bool { return bgShell.PID() > 0 }, 5*time.Second, 20*time.Millisecond)

	tool := NewJobOutputTool(t.TempDir())
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{"shell_id":"` + bgShell.ID + `"}`})
	require.NoError(t, err)
	require.Contains(t, resp.Metadata, `"pid":`)
}

func textOf(t *testing.T, resp fantasy.ToolResponse) string {
	t.Helper()
	return strings.TrimSpace(resp.Content)
}
