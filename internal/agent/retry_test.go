package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
)

// retryMessageStore records deletions so the retry cleanup can be
// asserted without a real database.
type retryMessageStore struct {
	message.Service

	deleted []string
}

func (s *retryMessageStore) Delete(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func retryMsg(id string, role message.MessageRole, parts ...message.ContentPart) message.Message {
	return message.Message{ID: id, Role: role, Parts: parts}
}

func textMsg(id string, role message.MessageRole, text string) message.Message {
	return retryMsg(id, role, message.TextContent{Text: text})
}

// TestDiscardTrailingTurn checks that a retry drops every message written
// after the newest prompt and keeps history up to it, so the previous
// attempt cannot survive next to its replacement.
func TestDiscardTrailingTurn(t *testing.T) {
	t.Parallel()

	store := &retryMessageStore{}
	a := &sessionAgent{messages: store}
	msgs := []message.Message{
		textMsg("u1", message.User, "first prompt"),
		textMsg("a1", message.Assistant, "first answer"),
		textMsg("u2", message.User, "second prompt"),
		textMsg("a2", message.Assistant, "failed answer"),
		retryMsg("t2", message.Tool, message.ToolResult{ToolCallID: "c1"}),
	}

	trimmed, err := a.discardTrailingTurn(t.Context(), msgs)
	require.NoError(t, err)
	require.Len(t, trimmed, 3)
	require.Equal(t, []string{"a2", "t2"}, store.deleted)
	require.Equal(t, "second prompt", trimmed[len(trimmed)-1].Content().Text)
}

// TestDiscardTrailingTurnSkipsNonPrompts makes sure generated and hidden
// messages are not mistaken for the prompt to retry.
func TestDiscardTrailingTurnSkipsNonPrompts(t *testing.T) {
	t.Parallel()

	store := &retryMessageStore{}
	a := &sessionAgent{messages: store}
	hidden := retryMsg("u-hidden", message.User, message.TextContent{Text: "continue", Hidden: true})
	msgs := []message.Message{
		textMsg("u1", message.User, "real prompt"),
		hidden,
		textMsg("a1", message.Assistant, "failed answer"),
	}

	trimmed, err := a.discardTrailingTurn(t.Context(), msgs)
	require.NoError(t, err)
	require.Len(t, trimmed, 1)
	require.Equal(t, "real prompt", trimmed[0].Content().Text)
	require.Equal(t, []string{"u-hidden", "a1"}, store.deleted)
}

// TestDiscardTrailingTurnWithoutPrompt reports a dedicated error when the
// session has nothing to retry.
func TestDiscardTrailingTurnWithoutPrompt(t *testing.T) {
	t.Parallel()

	a := &sessionAgent{messages: &retryMessageStore{}}
	_, err := a.discardTrailingTurn(t.Context(), []message.Message{
		retryMsg("a1", message.Assistant, message.TextContent{Text: "hi"}),
	})
	require.ErrorIs(t, err, ErrNothingToRetry)
}

func TestIsRetryablePrompt(t *testing.T) {
	t.Parallel()

	summary := textMsg("s1", message.User, "a summary")
	summary.IsSummaryMessage = true

	tests := []struct {
		name string
		msg  message.Message
		want bool
	}{
		{"user text", textMsg("u1", message.User, "hello"), true},
		{"user hidden", retryMsg("u2", message.User, message.TextContent{Text: "go on", Hidden: true}), false},
		{"user binary only", retryMsg("u3", message.User, message.BinaryContent{Path: "/tmp/a.png", MIMEType: "image/png", Data: []byte("x")}), true},
		{"user shell only", retryMsg("u4", message.User, message.ShellCommand{Command: "ls", Output: "x"}), false},
		{"summary", summary, false},
		{"assistant", textMsg("a1", message.Assistant, "hi"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isRetryablePrompt(tt.msg))
		})
	}
}

func TestValidateCallRetry(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateCall(SessionAgentCall{Retry: true, SessionID: "s1"}),
		"a retry needs no prompt")
	require.ErrorIs(t, ValidateCall(SessionAgentCall{Retry: true}), ErrSessionMissing,
		"a retry still needs a session")
	require.ErrorIs(t, ValidateCall(SessionAgentCall{SessionID: "s1"}), ErrEmptyPrompt,
		"a normal call still needs a prompt")
}

func TestRetryContext(t *testing.T) {
	t.Parallel()

	require.False(t, RetryRequested(context.Background()))
	require.True(t, RetryRequested(WithRetry(context.Background())))
}
