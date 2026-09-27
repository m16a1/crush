package chat

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newBackgroundJobItem(t *testing.T, shellID string) *BashToolMessageItem {
	t.Helper()
	sty := styles.CharmtonePantera()
	tc := message.ToolCall{
		ID:       "bash-bg",
		Name:     "bash",
		Input:    `{"command":"npm run dev","description":"dev server","run_in_background":true}`,
		Finished: true,
	}
	result := &message.ToolResult{
		ToolCallID: "bash-bg",
		Content:    "Background shell started with ID: " + shellID,
		Metadata:   `{"description":"dev server","background":true,"shell_id":"` + shellID + `","pid":5142}`,
	}
	item, ok := NewBashToolMessageItem(&sty, tc, result, false, "").(*BashToolMessageItem)
	require.True(t, ok)
	return item
}

// TestJobItemStreamsLiveOutput verifies that output pushed after the tool
// result is rendered into the job item, so a running job shows progress
// instead of a static "started" line.
func TestJobItemStreamsLiveOutput(t *testing.T) {
	t.Parallel()

	item := newBackgroundJobItem(t, "202")
	require.Equal(t, "202", item.JobShellID())

	before := ansi.Strip(item.RawRender(120))
	require.NotContains(t, before, "listening on :3000")

	item.AppendJobOutput("listening on :3000\n")

	after := ansi.Strip(item.RawRender(120))
	require.Contains(t, after, "listening on :3000")
	require.Contains(t, after, "running")
}

// TestJobItemDoneEventFlipsState verifies the terminal event closes the job
// out with its exit code.
func TestJobItemDoneEventFlipsState(t *testing.T) {
	t.Parallel()

	item := newBackgroundJobItem(t, "202")
	item.SetJobDone(0)
	done := ansi.Strip(item.RawRender(120))
	require.Contains(t, done, "done")
	require.NotContains(t, done, "running")

	item2 := newBackgroundJobItem(t, "203")
	item2.SetJobDone(7)
	failed := ansi.Strip(item2.RawRender(120))
	require.Contains(t, failed, "exit 7")
}
