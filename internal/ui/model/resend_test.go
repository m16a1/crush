package model

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/attachments"
	"github.com/charmbracelet/crush/internal/ui/util"
)

// resendRun records one AgentRun call so tests can assert what a resend sent.
type resendRun struct {
	sessionID   string
	prompt      string
	attachments []message.Attachment
}

// resendWorkspace is a workspace stub that serves a fixed user-message list and
// records the prompts the agent is asked to run.
type resendWorkspace struct {
	countingWorkspace

	messages     []message.Message
	runs         []resendRun
	historyCalls int
}

func (w *resendWorkspace) ListUserMessages(context.Context, string) ([]message.Message, error) {
	w.historyCalls++
	return w.messages, nil
}

func (w *resendWorkspace) AgentRun(_ context.Context, sessionID, prompt string, attachments ...message.Attachment) error {
	w.runs = append(w.runs, resendRun{sessionID: sessionID, prompt: prompt, attachments: attachments})
	return nil
}

func userMessage(text string, parts ...message.ContentPart) message.Message {
	all := []message.ContentPart{message.TextContent{Text: text}}
	return message.Message{
		ID:        "u-" + text,
		SessionID: "s1",
		Role:      message.User,
		Parts:     append(all, parts...),
	}
}

// pressResend drives the resend hotkey the way the Bubble Tea runtime would.
// The key handler returns the history lookup; its result is the message Update
// turns into a submitted prompt.
func pressResend(t *testing.T, m *UI) {
	t.Helper()

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	require.NotNil(t, cmd, "the hotkey must not be ignored")
	require.True(t, driveResend(m, cmd), "the hotkey must look up the last prompt")
}

// driveResend runs a command tree produced by the resend hotkey, feeding the
// lookup result and any cache refreshes back into Update the way the runtime
// does. It reports whether the lookup ran.
func driveResend(m *UI, cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		found := false
		for _, c := range msg {
			found = driveResend(m, c) || found
		}
		return found
	case resendPromptMsg:
		_, next := m.Update(msg)
		driveResend(m, next)
		return true
	case busyStateMsg, promptQueueMsg, agentRunSubmittedMsg, lspStatesMsg, agentModelChangedMsg:
		_, next := m.Update(msg)
		driveResend(m, next)
	}
	return false
}

// TestResendPromptKeyResendsLastPrompt drives the hotkey: the newest prompt of
// the session, with the files it was sent with, is submitted again.
func TestResendPromptKeyResendsLastPrompt(t *testing.T) {
	ws := &resendWorkspace{
		countingWorkspace: countingWorkspace{ready: true},
		messages: []message.Message{
			userMessage("latest", message.BinaryContent{
				Path:     "/tmp/shot.png",
				MIMEType: "image/png",
				Data:     []byte("png"),
			}),
			userMessage("older"),
		},
	}
	m := newBusyUI(ws)

	pressResend(t, m)

	require.Len(t, ws.runs, 1)
	require.Equal(t, "s1", ws.runs[0].sessionID)
	require.Equal(t, "latest", ws.runs[0].prompt)
	require.Len(t, ws.runs[0].attachments, 1, "the resent prompt carries the original attachments")
	require.Equal(t, "/tmp/shot.png", ws.runs[0].attachments[0].FilePath)
	require.Equal(t, "shot.png", ws.runs[0].attachments[0].FileName)
	require.Equal(t, []byte("png"), ws.runs[0].attachments[0].Content)
}

// TestResendPromptKeyWaitsForAnIdleAgent makes sure the hotkey cannot pile a
// duplicate prompt onto a run that is already in flight.
func TestResendPromptKeyWaitsForAnIdleAgent(t *testing.T) {
	ws := &resendWorkspace{
		countingWorkspace: countingWorkspace{ready: true},
		messages:          []message.Message{userMessage("latest")},
	}
	m := newBusyUI(ws)
	m.agentBusyCache.set(true)

	// The hotkey is still accepted, but the prompt is never looked up.
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	runCmds(m, cmd)

	require.Empty(t, ws.runs)
	require.Zero(t, ws.historyCalls, "a busy agent is not asked for history")
}

// TestResendPromptKeyIsIgnoredOutsideChat makes sure the hotkey has no effect
// before a session exists, so it cannot start one by accident.
func TestResendPromptKeyIsIgnoredOutsideChat(t *testing.T) {
	ws := &resendWorkspace{messages: []message.Message{userMessage("latest")}}
	m := newBusyUI(ws)
	m.state = uiLanding
	m.session = nil

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	runCmds(m, cmd)

	require.Empty(t, ws.runs)
	require.Zero(t, ws.historyCalls, "no prompt is looked up without a session")
}

// TestResendPromptIsInHelp keeps the hotkey discoverable: it is listed in the
// full help whenever a session can be resent in.
func TestResendPromptIsInHelp(t *testing.T) {
	m := newBusyUI(&resendWorkspace{})

	require.Contains(t, helpKeys(m), "ctrl+r")

	m.session = nil
	require.NotContains(t, helpKeys(m), "ctrl+r",
		"with no session there is nothing to resend")
}

// TestResendPromptKeyIsNotClaimedByAttachments pins the key split: the editor
// only starts an attachment deletion on its own key, so ctrl+r reaches the
// resend binding even while attachments are queued.
func TestResendPromptKeyIsNotClaimedByAttachments(t *testing.T) {
	ws := &resendWorkspace{
		countingWorkspace: countingWorkspace{ready: true},
		messages:          []message.Message{userMessage("latest")},
	}
	m := newBusyUI(ws)
	// Wire the editor keymap the way the real UI does.
	km := DefaultKeyMap()
	m.attachments = attachments.New(nil, attachments.Keymap{
		DeleteMode: km.Editor.AttachmentDeleteMode,
		DeleteAll:  km.Editor.DeleteAllAttachments,
		Escape:     km.Editor.Escape,
	})
	m.attachments.Update(message.Attachment{FilePath: "/tmp/a.txt", MimeType: "text/plain", Content: []byte("a")})

	resend := tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	require.False(t, m.attachments.Update(resend), "the resend key must not arm delete mode")

	pressResend(t, m)
	require.Len(t, ws.runs, 1)
	require.Equal(t, "latest", ws.runs[0].prompt)

	require.True(t, m.attachments.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}),
		"attachment deletion keeps a key of its own")
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

// TestResendPromptWarnsWithoutPrompt covers a session whose only user messages
// carry no prompt: a bang command and a generated continuation.
func TestResendPromptWarnsWithoutPrompt(t *testing.T) {
	hidden := userMessage("continue")
	hidden.Parts = []message.ContentPart{message.TextContent{Text: "continue", Hidden: true}}
	ws := &resendWorkspace{
		messages: []message.Message{
			{ID: "u-bang", SessionID: "s1", Role: message.User, Parts: []message.ContentPart{
				message.ShellCommand{Command: "ls", Output: "crush", ExitCode: 0},
			}},
			hidden,
		},
	}
	m := newBusyUI(ws)

	msg := m.resendLastPrompt()()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an info message, got %T", msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "No previous prompt")
}

// TestResendPromptRefusesWhileBusy keeps a resend from piling a duplicate
// prompt onto a run that is already in flight.
func TestResendPromptRefusesWhileBusy(t *testing.T) {
	ws := &resendWorkspace{messages: []message.Message{userMessage("latest")}}
	m := newBusyUI(ws)
	m.agentBusyCache.set(true)

	msg := m.resendLastPrompt()()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an info message, got %T", msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Zero(t, ws.historyCalls, "a busy agent is not asked for history")
}

func TestLastResendablePrompt(t *testing.T) {
	t.Parallel()

	t.Run("newest prompt wins", func(t *testing.T) {
		t.Parallel()

		prompt, attachments := lastResendablePrompt([]message.Message{
			userMessage("newest"),
			userMessage("older"),
		})
		require.Equal(t, "newest", prompt)
		require.Empty(t, attachments)
	})

	t.Run("generated continuations are skipped", func(t *testing.T) {
		t.Parallel()

		continuation := userMessage("")
		continuation.Parts = []message.ContentPart{message.TextContent{Text: "continue", Hidden: true}}

		prompt, _ := lastResendablePrompt([]message.Message{continuation, userMessage("real prompt")})
		require.Equal(t, "real prompt", prompt)
	})

	t.Run("messages with no prompt of their own are skipped", func(t *testing.T) {
		t.Parallel()

		bang := message.Message{ID: "u-bang", Role: message.User, Parts: []message.ContentPart{
			message.ShellCommand{Command: "ls", Output: "crush"},
		}}
		summary := userMessage("a summary")
		summary.IsSummaryMessage = true

		prompt, _ := lastResendablePrompt([]message.Message{bang, summary, userMessage("real prompt")})
		require.Equal(t, "real prompt", prompt)
	})

	t.Run("assistant messages are never resent", func(t *testing.T) {
		t.Parallel()

		prompt, attachments := lastResendablePrompt([]message.Message{
			{ID: "a1", Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "hello"}}},
		})
		require.Empty(t, prompt)
		require.Empty(t, attachments)
	})

	t.Run("attachments come from the binary parts", func(t *testing.T) {
		t.Parallel()

		prompt, attachments := lastResendablePrompt([]message.Message{
			userMessage("see this", message.BinaryContent{
				Path:     "/tmp/report.md",
				MIMEType: "text/markdown",
				Data:     []byte("# report"),
			}),
		})
		require.Equal(t, "see this", prompt)
		require.Len(t, attachments, 1)
		require.Equal(t, "report.md", attachments[0].FileName)
		require.Equal(t, "text/markdown", attachments[0].MimeType)
	})

	t.Run("an attachment-only prompt is resendable", func(t *testing.T) {
		t.Parallel()

		prompt, attachments := lastResendablePrompt([]message.Message{
			userMessage("", message.BinaryContent{Path: "/tmp/a.txt", MIMEType: "text/plain", Data: []byte("a")}),
		})
		require.Empty(t, prompt)
		require.Len(t, attachments, 1)
	})

	t.Run("nothing to resend", func(t *testing.T) {
		t.Parallel()

		prompt, attachments := lastResendablePrompt(nil)
		require.Empty(t, prompt)
		require.Empty(t, attachments)
	})
}
