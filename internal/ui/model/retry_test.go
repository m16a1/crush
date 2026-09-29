package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/attachments"
	"github.com/charmbracelet/crush/internal/ui/util"
)

// retryWorkspace records the sessions the retry hotkey asked to retry.
type retryWorkspace struct {
	countingWorkspace

	retries  []string
	retryErr error
}

func (w *retryWorkspace) AgentRetry(_ context.Context, sessionID string) error {
	w.retries = append(w.retries, sessionID)
	return w.retryErr
}

// pressRetry drives the retry hotkey the way the Bubble Tea runtime
// would, executing the command it returns so the workspace call runs.
func pressRetry(t *testing.T, m *UI) {
	t.Helper()

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	require.NotNil(t, cmd, "the hotkey must not be ignored")
	runCmds(m, cmd)
}

// TestRetryPromptKeyRetriesLastPrompt drives the hotkey: ctrl+r asks the
// workspace to re-run the newest prompt of the active session.
func TestRetryPromptKeyRetriesLastPrompt(t *testing.T) {
	ws := &retryWorkspace{countingWorkspace: countingWorkspace{ready: true}}
	m := newBusyUI(ws)

	pressRetry(t, m)

	require.Equal(t, []string{"s1"}, ws.retries)
}

// TestRetryPromptKeyWaitsForAnIdleAgent makes sure the hotkey cannot retry
// onto a run that is already in flight.
func TestRetryPromptKeyWaitsForAnIdleAgent(t *testing.T) {
	ws := &retryWorkspace{countingWorkspace: countingWorkspace{ready: true}}
	m := newBusyUI(ws)
	m.agentBusyCache.set(true)

	pressRetry(t, m)

	require.Empty(t, ws.retries, "a busy agent must not be retried")
}

// TestRetryPromptKeyIsIgnoredOutsideChat makes sure the hotkey has no effect
// before a session exists, so it cannot start one by accident.
func TestRetryPromptKeyIsIgnoredOutsideChat(t *testing.T) {
	ws := &retryWorkspace{}
	m := newBusyUI(ws)
	m.state = uiLanding
	m.session = nil

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	runCmds(m, cmd)

	require.Empty(t, ws.retries, "no retry is attempted without a session")
}

// TestRetryPromptIsInHelp keeps the hotkey discoverable: it is listed in the
// full help whenever a session can be retried in.
func TestRetryPromptIsInHelp(t *testing.T) {
	m := newBusyUI(&retryWorkspace{})

	require.Contains(t, helpKeys(m), "ctrl+r")

	m.session = nil
	require.NotContains(t, helpKeys(m), "ctrl+r",
		"with no session there is nothing to retry")
}

// TestRetryPromptKeyIsNotClaimedByAttachments pins the key split: the editor
// only starts an attachment deletion on its own key, so ctrl+r reaches the
// retry binding even while attachments are queued.
func TestRetryPromptKeyIsNotClaimedByAttachments(t *testing.T) {
	ws := &retryWorkspace{countingWorkspace: countingWorkspace{ready: true}}
	m := newBusyUI(ws)
	// Wire the editor keymap the way the real UI does.
	km := DefaultKeyMap()
	m.attachments = attachments.New(nil, attachments.Keymap{
		DeleteMode: km.Editor.AttachmentDeleteMode,
		DeleteAll:  km.Editor.DeleteAllAttachments,
		Escape:     km.Editor.Escape,
	})
	m.attachments.Update(message.Attachment{FilePath: "/tmp/a.txt", MimeType: "text/plain", Content: []byte("a")})

	retry := tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	require.False(t, m.attachments.Update(retry), "the retry key must not arm delete mode")

	pressRetry(t, m)
	require.Equal(t, []string{"s1"}, ws.retries)

	require.True(t, m.attachments.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}),
		"attachment deletion keeps a key of its own")
}

// TestRetryPromptReportsWorkspaceError surfaces a failure from the retry
// call so the user learns why nothing ran.
func TestRetryPromptReportsWorkspaceError(t *testing.T) {
	ws := &retryWorkspace{
		countingWorkspace: countingWorkspace{ready: true},
		retryErr:          errors.New("no previous prompt to retry"),
	}
	m := newBusyUI(ws)

	msg := m.retryLastPrompt()()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an info message, got %T", msg)
	require.Equal(t, util.InfoTypeError, info.Type)
}

// helpKeys flattens every key listed in the full help view.
func helpKeys(m *UI) []string {
	var keys []string
	for _, group := range m.FullHelp() {
		for _, binding := range group {
			keys = append(keys, binding.Keys()...)
		}
	}
	return keys
}
