package model

import (
	"context"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/util"
)

// resendPromptMsg carries the prompt that [UI.resendLastPrompt] selected back
// to the update loop, where sending works exactly like pressing enter.
type resendPromptMsg struct {
	prompt      string
	attachments []message.Attachment
}

// resendLastPrompt resends the most recent prompt of the current session, with
// its attachments, as a new turn. Reading the prompt is a workspace call, so it
// happens off the update goroutine and comes back as a [resendPromptMsg].
func (m *UI) resendLastPrompt() tea.Cmd {
	if !m.hasSession() {
		return util.ReportWarn("No session to resend a prompt in")
	}
	if m.isAgentBusy() {
		return util.ReportWarn("Agent is busy, please wait before resending a prompt...")
	}

	sessionID := m.session.ID
	return func() tea.Msg {
		messages, err := m.com.Workspace.ListUserMessages(context.Background(), sessionID)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		prompt, attachments := lastResendablePrompt(messages)
		if prompt == "" && len(attachments) == 0 {
			return util.NewWarnMsg("No previous prompt to resend")
		}
		return resendPromptMsg{prompt: prompt, attachments: attachments}
	}
}

// lastResendablePrompt returns the newest prompt in messages, which the
// workspace orders newest first, along with its attachments. Messages that
// carry no prompt of their own are skipped: generated continuations only exist
// for the model's history, and bang-mode commands are not agent prompts.
func lastResendablePrompt(messages []message.Message) (string, []message.Attachment) {
	for _, msg := range messages {
		if msg.Role != message.User || msg.IsSummaryMessage {
			continue
		}
		content := msg.Content()
		if content.Hidden {
			continue
		}
		prompt := strings.TrimSpace(content.Text)
		attachments := promptAttachments(msg.BinaryContent())
		if prompt == "" && len(attachments) == 0 {
			continue
		}
		return prompt, attachments
	}
	return "", nil
}

// promptAttachments rebuilds the attachments of a stored user message from the
// binary parts it was created with, so a resent prompt carries the same files
// as the original.
func promptAttachments(contents []message.BinaryContent) []message.Attachment {
	attachments := make([]message.Attachment, 0, len(contents))
	for _, content := range contents {
		attachments = append(attachments, message.Attachment{
			FilePath: content.Path,
			FileName: filepath.Base(content.Path),
			MimeType: content.MIMEType,
			Content:  content.Data,
		})
	}
	return attachments
}
