package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

// TestBackgroundBashJobIsNotifiable verifies that a command deliberately left
// running in the background is marked for completion reporting and carries a
// real PID, so the conversation is told when it finishes.
func TestBackgroundBashJobIsNotifiable(t *testing.T) {
	workingDir := t.TempDir()
	tool, _ := newBashToolWithRecordingPerms(workingDir, true)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:     "long running",
		Command:         "/bin/sleep 30",
		RunInBackground: true,
	})
	require.False(t, resp.IsError, resp.Content)

	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.True(t, meta.Background)
	require.NotEmpty(t, meta.ShellID)

	bg, ok := shell.GetBackgroundShellManager().Get(meta.ShellID)
	require.True(t, ok, "background job must stay tracked")
	defer shell.GetBackgroundShellManager().Kill(meta.ShellID)

	require.True(t, bg.NotifyOnDone(), "backgrounded job must be reported on completion")
	require.Eventually(t, func() bool { return bg.PID() > 0 }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, bg.PID(), meta.PID)
}

// TestSynchronousBashJobIsNotNotifiable verifies that a command which merely
// finishes synchronously is removed and never reported as a background job,
// so the agent is not woken for output it already received inline.
func TestSynchronousBashJobIsNotNotifiable(t *testing.T) {
	workingDir := t.TempDir()
	tool, _ := newBashToolWithRecordingPerms(workingDir, true)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "quick",
		Command:     "echo hi",
	})
	require.False(t, resp.IsError, resp.Content)

	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.False(t, meta.Background)
	require.Empty(t, meta.ShellID, "a synchronously finished command has no background job to report")
}
