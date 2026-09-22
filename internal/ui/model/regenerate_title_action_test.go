package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

// titleWorkspace records title regeneration requests.
type titleWorkspace struct {
	countingWorkspace
	cfg   config.Config
	err   error
	calls []string
}

func newTitleWorkspace(err error) *titleWorkspace {
	return &titleWorkspace{
		cfg: config.Config{Options: &config.Options{TUI: &config.TUIOptions{}}},
		err: err,
	}
}

func (w *titleWorkspace) Config() *config.Config { return &w.cfg }

func (w *titleWorkspace) AgentRegenerateTitle(_ context.Context, sessionID string) error {
	w.calls = append(w.calls, sessionID)
	return w.err
}

// pickCommand opens the command palette, filters it down to the named command
// and confirms it, returning the command the action produced.
func pickCommand(t *testing.T, m *UI, filter string) tea.Cmd {
	t.Helper()

	// The initial command (a Docker MCP availability probe) is left unrun:
	// the palette is open either way and the test has no use for it.
	_ = m.openCommandsDialog()
	require.True(t, m.dialog.HasDialogs(), "the command palette is open")

	for _, r := range filter {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return cmd
}

// TestRegenerateTitleCommandAsksTheWorkspace: picking the command reaches the
// workspace for the session the palette was opened on.
func TestRegenerateTitleCommandAsksTheWorkspace(t *testing.T) {
	ws := newTitleWorkspace(nil)
	m := newBusyUI(ws)
	m.session = &session.Session{ID: "s1"}

	cmd := pickCommand(t, m, "regenerate")
	require.NotNil(t, cmd)
	runCmds(m, cmd)

	require.Equal(t, []string{"s1"}, ws.calls)
}

// TestRegenerateTitleCommandReportsFailure: a regeneration that fails says why
// instead of silently leaving the title as it was.
func TestRegenerateTitleCommandReportsFailure(t *testing.T) {
	ws := newTitleWorkspace(errors.New("session has no prompt to base a title on"))
	m := newBusyUI(ws)
	m.session = &session.Session{ID: "s1"}

	cmd := pickCommand(t, m, "regenerate")
	require.NotNil(t, cmd)

	info := firstInfoMsg(t, cmd)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "no prompt to base a title on")
}

// firstInfoMsg runs a command tree and returns the first notification it
// produced, ignoring the cache refreshes batched alongside it.
func firstInfoMsg(t *testing.T, cmd tea.Cmd) util.InfoMsg {
	t.Helper()

	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if info, ok := c().(util.InfoMsg); ok {
				return info
			}
		}
	}
	info, ok := msg.(util.InfoMsg)
	require.Truef(t, ok, "command produced %T, want a notification", msg)
	return info
}
