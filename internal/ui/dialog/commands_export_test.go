package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// TestCommandsOfferExportSession: the current session can be written to a
// Markdown file from the command palette, and the command carries the session
// it belongs to.
func TestCommandsOfferExportSession(t *testing.T) {
	t.Parallel()

	dialog := newCommandsDialog(t, "session-1", true)

	index := -1
	for i, item := range dialog.list.FilteredItems() {
		if ci, ok := item.(*CommandItem); ok && ci.ID() == "export_session" {
			index = i
		}
	}
	require.NotEqual(t, -1, index, "an active session can be exported")

	dialog.list.SetSelected(index)
	action := dialog.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	export, ok := action.(ActionExportSession)
	require.True(t, ok, "enter on the command yields ActionExportSession")
	require.Equal(t, "session-1", export.SessionID)
}

// TestCommandsHideExportSessionWithoutASession: there is nothing to export
// before a session exists.
func TestCommandsHideExportSessionWithoutASession(t *testing.T) {
	t.Parallel()

	dialog := newCommandsDialog(t, "", false)

	ids := make([]string, 0, len(dialog.list.FilteredItems()))
	for _, item := range dialog.list.FilteredItems() {
		if ci, ok := item.(*CommandItem); ok {
			ids = append(ids, ci.ID())
		}
	}
	require.NotContains(t, ids, "export_session")
}
