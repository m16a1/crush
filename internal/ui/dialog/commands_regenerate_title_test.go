package dialog

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
	"testing"
)

// commandsWorkspace is a workspace stub with the minimal config the commands
// dialog reads while building its item list.
type commandsWorkspace struct {
	workspace.Workspace
	cfg config.Config
}

func (w *commandsWorkspace) Config() *config.Config { return &w.cfg }

func newCommandsDialog(t *testing.T, sessionID string, hasSession bool) *Commands {
	t.Helper()
	sty := styles.CharmtonePantera()
	com := &common.Common{
		Workspace: &commandsWorkspace{cfg: config.Config{Options: &config.Options{TUI: &config.TUIOptions{}}}},
		Styles:    &sty,
	}
	dialog, err := NewCommands(com, sessionID, hasSession, false, false, nil, nil)
	require.NoError(t, err)
	return dialog
}

// TestCommandsOfferRegenerateTitle: the title a session was created with can
// be replaced from the command palette, and the command carries the session it
// belongs to.
func TestCommandsOfferRegenerateTitle(t *testing.T) {
	t.Parallel()

	dialog := newCommandsDialog(t, "session-1", true)

	index := -1
	for i, item := range dialog.list.FilteredItems() {
		if ci, ok := item.(*CommandItem); ok && ci.ID() == "regenerate_title" {
			index = i
		}
	}
	require.NotEqual(t, -1, index, "an active session can regenerate its title")

	dialog.list.SetSelected(index)
	action := dialog.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	regenerate, ok := action.(ActionRegenerateTitle)
	require.True(t, ok, "enter on the command yields ActionRegenerateTitle")
	require.Equal(t, "session-1", regenerate.SessionID)
}

// TestCommandsHideRegenerateTitleWithoutASession: there is no title to
// regenerate before a session exists.
func TestCommandsHideRegenerateTitleWithoutASession(t *testing.T) {
	t.Parallel()

	dialog := newCommandsDialog(t, "", false)

	ids := make([]string, 0, len(dialog.list.FilteredItems()))
	for _, item := range dialog.list.FilteredItems() {
		if ci, ok := item.(*CommandItem); ok {
			ids = append(ids, ci.ID())
		}
	}
	require.NotContains(t, ids, "regenerate_title")
	require.NotContains(t, ids, "summarize")
}
