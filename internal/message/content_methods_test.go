package message

import (
	"encoding/base64"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy/providers/openai"
	"github.com/stretchr/testify/require"
)

func TestContentString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "hello", TextContent{Text: "hello"}.String())
	require.Equal(t, "thinking", ReasoningContent{Thinking: "thinking"}.String())
	require.Equal(t, "https://example.com", ImageURLContent{URL: "https://example.com"}.String())
}

func TestBinaryContentString(t *testing.T) {
	t.Parallel()

	data := []byte("hello")
	encoded := base64.StdEncoding.EncodeToString(data)
	part := BinaryContent{MIMEType: "image/png", Data: data}

	require.Equal(t, encoded, part.String(catwalk.InferenceProviderAnthropic))
	require.Equal(t, "data:image/png;base64,"+encoded, part.String(catwalk.InferenceProviderOpenAI))
}

func TestMessageHasShellCommand(t *testing.T) {
	t.Parallel()

	msg := &Message{Parts: []ContentPart{TextContent{Text: "hi"}}}
	require.False(t, msg.HasShellCommand())
	require.Empty(t, msg.ShellCommands())

	msg.Parts = append(msg.Parts, ShellCommand{Command: "ls", Output: "out", ExitCode: 0})
	require.True(t, msg.HasShellCommand())
	require.Equal(t, []ShellCommand{{Command: "ls", Output: "out", ExitCode: 0}}, msg.ShellCommands())
}

func TestMessageBinaryContent(t *testing.T) {
	t.Parallel()

	msg := &Message{Parts: []ContentPart{
		TextContent{Text: "hi"},
		BinaryContent{Path: "a", MIMEType: "image/png"},
	}}
	got := msg.BinaryContent()
	require.Len(t, got, 1)
	require.Equal(t, "a", got[0].Path)
}

func TestMessageFinishReason(t *testing.T) {
	t.Parallel()

	require.Equal(t, FinishReason(""), (&Message{}).FinishReason())

	msg := &Message{Parts: []ContentPart{Finish{Reason: FinishReasonMaxTokens}}}
	require.Equal(t, FinishReasonMaxTokens, msg.FinishReason())
}

func TestMessageIsErrorLike(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason FinishReason
		want   bool
	}{
		{FinishReasonError, true},
		{FinishReasonContentFilter, true},
		{FinishReasonEndTurn, false},
		{FinishReasonToolUse, false},
	}

	for _, tt := range tests {
		msg := &Message{Parts: []ContentPart{Finish{Reason: tt.reason}}}
		require.Equal(t, tt.want, msg.IsErrorLike(), "reason %q", tt.reason)
	}
}

func TestMessageIsThinking(t *testing.T) {
	t.Parallel()

	require.True(t, (&Message{Parts: []ContentPart{
		ReasoningContent{Thinking: "hmm"},
	}}).IsThinking())

	require.False(t, (&Message{Parts: []ContentPart{
		ReasoningContent{Thinking: "hmm"},
		TextContent{Text: "answer"},
	}}).IsThinking())

	require.False(t, (&Message{Parts: []ContentPart{
		ReasoningContent{Thinking: "hmm"},
		Finish{Reason: FinishReasonEndTurn},
	}}).IsThinking())
}

func TestMessageAppendThoughtSignature(t *testing.T) {
	t.Parallel()

	t.Run("existing reasoning part", func(t *testing.T) {
		t.Parallel()

		msg := &Message{Parts: []ContentPart{ReasoningContent{Thinking: "t"}}}
		msg.AppendThoughtSignature("sig", "tool")
		msg.AppendThoughtSignature("more", "tool")

		reasoning := msg.ReasoningContent()
		require.Equal(t, "t", reasoning.Thinking)
		require.Equal(t, "sigmore", reasoning.ThoughtSignature)
		require.Equal(t, "tool", reasoning.ToolID)
	})

	t.Run("no reasoning part creates one", func(t *testing.T) {
		t.Parallel()

		msg := &Message{}
		msg.AppendThoughtSignature("sig", "tool")

		reasoning := msg.ReasoningContent()
		require.Equal(t, "sig", reasoning.ThoughtSignature)
	})
}

func TestMessageAppendReasoningSignature(t *testing.T) {
	t.Parallel()

	t.Run("existing reasoning part", func(t *testing.T) {
		t.Parallel()

		msg := &Message{Parts: []ContentPart{ReasoningContent{Thinking: "t"}}}
		msg.AppendReasoningSignature("a")
		msg.AppendReasoningSignature("b")

		require.Equal(t, "ab", msg.ReasoningContent().Signature)
	})

	t.Run("no reasoning part creates one", func(t *testing.T) {
		t.Parallel()

		msg := &Message{}
		msg.AppendReasoningSignature("a")
		require.Equal(t, "a", msg.ReasoningContent().Signature)
	})
}

func TestMessageSetReasoningResponsesData(t *testing.T) {
	t.Parallel()

	data := &openai.ResponsesReasoningMetadata{}
	msg := &Message{Parts: []ContentPart{ReasoningContent{Thinking: "t"}}}
	msg.SetReasoningResponsesData(data)
	require.Same(t, data, msg.ReasoningContent().ResponsesData)

	// No reasoning part: no-op.
	empty := &Message{}
	empty.SetReasoningResponsesData(data)
	require.Empty(t, empty.Parts)
}

func TestMessageThinkingDuration(t *testing.T) {
	t.Parallel()

	require.Equal(t, time.Duration(0), (&Message{}).ThinkingDuration())

	msg := &Message{Parts: []ContentPart{
		ReasoningContent{StartedAt: time.Now().Add(-5 * time.Second).Unix()},
	}}
	require.InDelta(t, float64(5*time.Second), float64(msg.ThinkingDuration()), float64(time.Second))

	msg = &Message{Parts: []ContentPart{
		ReasoningContent{
			StartedAt:  time.Now().Add(-2 * time.Second).Unix(),
			FinishedAt: time.Now().Unix(),
		},
	}}
	require.InDelta(t, float64(2*time.Second), float64(msg.ThinkingDuration()), float64(time.Second))
}

func TestMessageFinishThinkingPreservesProviderMetadata(t *testing.T) {
	t.Parallel()

	data := &openai.ResponsesReasoningMetadata{}
	msg := &Message{Parts: []ContentPart{ReasoningContent{
		Thinking:         "t",
		Signature:        "sig",
		ThoughtSignature: "thought",
		ToolID:           "tool",
		ResponsesData:    data,
		StartedAt:        time.Now().Add(-time.Second).Unix(),
	}}}

	msg.FinishThinking()

	reasoning := msg.ReasoningContent()
	require.NotZero(t, reasoning.FinishedAt)
	require.Equal(t, "t", reasoning.Thinking)
	require.Equal(t, "sig", reasoning.Signature)
	require.Equal(t, "thought", reasoning.ThoughtSignature)
	require.Equal(t, "tool", reasoning.ToolID)
	require.Same(t, data, reasoning.ResponsesData)

	// An already-finished step is left untouched.
	first := reasoning.FinishedAt
	msg.FinishThinking()
	require.Equal(t, first, msg.ReasoningContent().FinishedAt)
}

func TestMessageFinishToolCall(t *testing.T) {
	t.Parallel()

	msg := &Message{Parts: []ContentPart{
		ToolCall{ID: "1", Name: "bash"},
		ToolCall{ID: "2", Name: "view"},
	}}
	msg.FinishToolCall("1")
	msg.FinishToolCall("missing")

	require.True(t, msg.ToolCalls()[0].Finished)
	require.False(t, msg.ToolCalls()[1].Finished)
}

func TestMessageAppendToolCallInput(t *testing.T) {
	t.Parallel()

	msg := &Message{Parts: []ContentPart{ToolCall{ID: "1", Name: "bash"}}}
	msg.AppendToolCallInput("1", "{\"a\"")
	msg.AppendToolCallInput("1", ":\"b\"}")
	msg.AppendToolCallInput("missing", "ignored")

	require.Equal(t, "{\"a\":\"b\"}", msg.ToolCalls()[0].Input)
}

func TestMessageSetToolCalls(t *testing.T) {
	t.Parallel()

	msg := &Message{Parts: []ContentPart{
		TextContent{Text: "keep"},
		ToolCall{ID: "old"},
	}}
	msg.SetToolCalls([]ToolCall{{ID: "new1"}, {ID: "new2"}})

	require.Len(t, msg.Parts, 3)
	require.Equal(t, "keep", msg.Content().Text)
	require.Len(t, msg.ToolCalls(), 2)
	require.Equal(t, "new1", msg.ToolCalls()[0].ID)
}

func TestMessageSetToolResults(t *testing.T) {
	t.Parallel()

	msg := &Message{Parts: []ContentPart{ToolResult{ToolCallID: "old"}}}
	msg.SetToolResults([]ToolResult{{ToolCallID: "1"}, {ToolCallID: "2"}})

	require.Len(t, msg.ToolResults(), 3)
}

func TestMessageSetFinishUsage(t *testing.T) {
	t.Parallel()

	msg := &Message{Parts: []ContentPart{Finish{Reason: FinishReasonEndTurn}}}
	msg.SetFinishUsage(10, 20)

	finish := msg.FinishPart()
	require.EqualValues(t, 10, finish.PromptTokens)
	require.EqualValues(t, 20, finish.CompletionTokens)

	// No finish part: no-op, no panic.
	(&Message{}).SetFinishUsage(1, 2)
}

func TestMessageAddBinary(t *testing.T) {
	t.Parallel()

	msg := &Message{}
	msg.AddBinary("image/png", []byte("data"))

	got := msg.BinaryContent()
	require.Len(t, got, 1)
	require.Equal(t, "image/png", got[0].MIMEType)
	require.Equal(t, []byte("data"), got[0].Data)
}

func TestPromptWithTextAttachments(t *testing.T) {
	t.Parallel()

	t.Run("no attachments", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, "prompt", PromptWithTextAttachments("prompt", nil))
	})

	t.Run("text attachment", func(t *testing.T) {
		t.Parallel()

		out := PromptWithTextAttachments("prompt", []Attachment{
			{FilePath: "/a.txt", MimeType: "text/plain", Content: []byte("contents")},
		})
		require.Contains(t, out, "<file path='/a.txt'>")
		require.Contains(t, out, "contents")
		require.Contains(t, out, "<system_info>")
	})

	t.Run("non-text attachments are skipped", func(t *testing.T) {
		t.Parallel()

		out := PromptWithTextAttachments("prompt", []Attachment{
			{FilePath: "/a.png", MimeType: "image/png", Content: []byte("binary")},
		})
		require.Equal(t, "prompt", out)
	})

	t.Run("attachment without path", func(t *testing.T) {
		t.Parallel()

		out := PromptWithTextAttachments("prompt", []Attachment{
			{MimeType: "text/plain", Content: []byte("contents")},
		})
		require.Contains(t, out, "<file>")
	})
}
