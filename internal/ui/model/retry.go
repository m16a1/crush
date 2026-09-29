package model

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/crush/internal/ui/util"
)

// retryLastPrompt re-runs the newest prompt of the current session,
// discarding the previous attempt's output so the answer is regenerated
// instead of duplicating the user message. It is the error-recovery
// path: pressing ctrl+r after a failed turn repeats it without retyping
// anything. The workspace call is a round trip, so it runs as a command
// and any error comes back as a message.
func (m *UI) retryLastPrompt() tea.Cmd {
	if !m.hasSession() {
		return util.ReportWarn("No session to retry a prompt in")
	}
	if m.isAgentBusy() {
		return util.ReportWarn("Agent is busy, please wait before retrying a prompt...")
	}

	sessionID := m.session.ID
	return func() tea.Msg {
		if err := m.com.Workspace.AgentRetry(context.Background(), sessionID); err != nil {
			return util.NewErrorMsg(err)
		}
		return nil
	}
}
