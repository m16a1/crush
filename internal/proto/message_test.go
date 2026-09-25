package proto

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// unknownPart implements ContentPart without being one of the known wire
// types, so the marshaler's default branch can be exercised.
type unknownPart struct{}

func (unknownPart) isPart() {}

func TestMarshalPartsRoundTripsEveryKnownType(t *testing.T) {
	t.Parallel()

	src := []ContentPart{
		ReasoningContent{Thinking: "t", Signature: "s", StartedAt: 1, FinishedAt: 2},
		TextContent{Text: "hello", Hidden: true},
		ImageURLContent{URL: "https://example.com/a.png", Detail: "high"},
		BinaryContent{Path: "p", MIMEType: "image/png", Data: []byte{1, 2, 3}},
		ToolCall{ID: "1", Name: "bash", Input: "{}", Type: "function", Finished: true},
		ToolResult{ToolCallID: "1", Name: "bash", Content: "ok", Metadata: "{}", IsError: true},
		Finish{Reason: FinishReasonEndTurn, Time: 42, Message: "m", Details: "d"},
		ShellCommand{Command: "ls", Output: "x", ExitCode: 1},
	}

	raw, err := MarshalParts(src)
	require.NoError(t, err)

	got, err := UnmarshalParts(raw)
	require.NoError(t, err)
	require.Equal(t, src, got)
}

func TestMarshalPartsEmpty(t *testing.T) {
	t.Parallel()

	raw, err := MarshalParts(nil)
	require.NoError(t, err)
	require.JSONEq(t, "[]", string(raw))

	got, err := UnmarshalParts(raw)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestMarshalPartsRejectsUnknownType(t *testing.T) {
	t.Parallel()

	_, err := MarshalParts([]ContentPart{unknownPart{}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown part type")
}

func TestUnmarshalPartsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
	}{
		{"invalid json", `{`},
		{"unknown wire type", `[{"type":"bogus","data":{}}]`},
		{"malformed wrapper", `[{"type":`},
		{"bad part payload", `[{"type":"text","data":"not-an-object"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := UnmarshalParts([]byte(tt.data))
			require.Error(t, err)
		})
	}
}

func TestMessageJSONRoundTrip(t *testing.T) {
	t.Parallel()

	src := Message{
		ID:        "m1",
		Role:      Assistant,
		SessionID: "s1",
		Parts: []ContentPart{
			ReasoningContent{Thinking: "why"},
			TextContent{Text: "answer"},
			ToolCall{ID: "t1", Name: "grep"},
		},
		Model:            "model",
		Provider:         "provider",
		CreatedAt:        1,
		UpdatedAt:        2,
		IsSummaryMessage: true,
	}

	raw, err := json.Marshal(src)
	require.NoError(t, err)

	var got Message
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, src, got)
}

func TestMessageUnmarshalJSONErrors(t *testing.T) {
	t.Parallel()

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		var m Message
		require.Error(t, json.Unmarshal([]byte(`{`), &m))
	})

	t.Run("invalid parts", func(t *testing.T) {
		t.Parallel()
		var m Message
		require.Error(t, json.Unmarshal([]byte(`{"parts":[{"type":"bogus"}]}`), &m))
	})
}

func TestMessageAccessors(t *testing.T) {
	t.Parallel()

	m := Message{
		Parts: []ContentPart{
			TextContent{Text: "visible"},
			ReasoningContent{Thinking: "hidden"},
			TextContent{Text: "second"},
			ImageURLContent{URL: "u1"},
			ImageURLContent{URL: "u2"},
			BinaryContent{Data: []byte{9}},
			ToolCall{ID: "a"},
			ToolCall{ID: "b"},
			ToolResult{ToolCallID: "a"},
			Finish{Reason: FinishReasonToolUse},
		},
	}

	require.Equal(t, "visible", m.Content().Text)
	require.Equal(t, "hidden", m.ReasoningContent().Thinking)
	require.Len(t, m.ImageURLContent(), 2)
	require.Len(t, m.BinaryContent(), 1)
	require.Len(t, m.ToolCalls(), 2)
	require.Len(t, m.ToolResults(), 1)
	require.True(t, m.IsFinished())
	require.NotNil(t, m.FinishPart())
	require.Equal(t, FinishReasonToolUse, m.FinishReason())
}

func TestMessageAccessorsEmpty(t *testing.T) {
	t.Parallel()

	m := Message{}
	require.Equal(t, TextContent{}, m.Content())
	require.Equal(t, ReasoningContent{}, m.ReasoningContent())
	require.Empty(t, m.ImageURLContent())
	require.Empty(t, m.BinaryContent())
	require.Empty(t, m.ToolCalls())
	require.Empty(t, m.ToolResults())
	require.False(t, m.IsFinished())
	require.Nil(t, m.FinishPart())
	require.Equal(t, FinishReason(""), m.FinishReason())
}

func TestMessageIsThinking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  Message
		want bool
	}{
		{"reasoning only", Message{Parts: []ContentPart{ReasoningContent{Thinking: "x"}}}, true},
		{"reasoning then text", Message{Parts: []ContentPart{ReasoningContent{Thinking: "x"}, TextContent{Text: "y"}}}, false},
		{"no reasoning", Message{Parts: []ContentPart{TextContent{Text: "y"}}}, false},
		{"finished", Message{Parts: []ContentPart{ReasoningContent{Thinking: "x"}, Finish{}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.msg.IsThinking())
		})
	}
}

func TestMessageAppendContent(t *testing.T) {
	t.Parallel()

	t.Run("creates text part", func(t *testing.T) {
		t.Parallel()
		var m Message
		m.AppendContent("a")
		m.AppendContent("b")
		require.Len(t, m.Parts, 1)
		require.Equal(t, "ab", m.Content().Text)
	})

	t.Run("preserves hidden flag", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{TextContent{Text: "a", Hidden: true}}}
		m.AppendContent("b")
		require.True(t, m.Content().Hidden)
	})
}

func TestMessageAppendReasoningContent(t *testing.T) {
	t.Parallel()

	var m Message
	m.AppendReasoningContent("a")
	require.NotZero(t, m.ReasoningContent().StartedAt)
	m.AppendReasoningContent("b")
	require.Equal(t, "ab", m.ReasoningContent().Thinking)
	require.Len(t, m.Parts, 1)
}

func TestMessageAppendReasoningSignature(t *testing.T) {
	t.Parallel()

	t.Run("appends to existing part", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{ReasoningContent{Thinking: "t", Signature: "a"}}}
		m.AppendReasoningSignature("b")
		require.Equal(t, "ab", m.ReasoningContent().Signature)
		require.Equal(t, "t", m.ReasoningContent().Thinking)
	})

	t.Run("creates part when absent", func(t *testing.T) {
		t.Parallel()
		var m Message
		m.AppendReasoningSignature("sig")
		require.Equal(t, "sig", m.ReasoningContent().Signature)
	})
}

func TestMessageFinishThinking(t *testing.T) {
	t.Parallel()

	t.Run("stamps finish time once", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{ReasoningContent{Thinking: "t", StartedAt: 1}}}
		m.FinishThinking()
		first := m.ReasoningContent().FinishedAt
		require.NotZero(t, first)

		m.FinishThinking()
		require.Equal(t, first, m.ReasoningContent().FinishedAt)
	})

	t.Run("no reasoning part is a no-op", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{TextContent{Text: "x"}}}
		m.FinishThinking()
		require.Len(t, m.Parts, 1)
	})
}

func TestMessageThinkingDuration(t *testing.T) {
	t.Parallel()

	t.Run("zero when never started", func(t *testing.T) {
		t.Parallel()
		m := Message{}
		require.Zero(t, m.ThinkingDuration())
	})

	t.Run("uses finished time", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{ReasoningContent{StartedAt: 100, FinishedAt: 105}}}
		require.Equal(t, 5*time.Second, m.ThinkingDuration())
	})

	t.Run("falls back to now", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{ReasoningContent{StartedAt: time.Now().Unix() - 3}}}
		require.GreaterOrEqual(t, m.ThinkingDuration(), 2*time.Second)
	})
}

func TestMessageToolCallMutations(t *testing.T) {
	t.Parallel()

	t.Run("finish by id", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{ToolCall{ID: "a"}, ToolCall{ID: "b"}}}
		m.FinishToolCall("b")
		calls := m.ToolCalls()
		require.False(t, calls[0].Finished)
		require.True(t, calls[1].Finished)
	})

	t.Run("finish unknown id is a no-op", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{ToolCall{ID: "a"}}}
		m.FinishToolCall("zzz")
		require.False(t, m.ToolCalls()[0].Finished)
	})

	t.Run("append input by id", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{ToolCall{ID: "a", Input: "{", Name: "n"}}}
		m.AppendToolCallInput("a", "}")
		require.Equal(t, "{}", m.ToolCalls()[0].Input)
		require.Equal(t, "n", m.ToolCalls()[0].Name)
	})

	t.Run("append input unknown id is a no-op", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{ToolCall{ID: "a", Input: "x"}}}
		m.AppendToolCallInput("zzz", "!")
		require.Equal(t, "x", m.ToolCalls()[0].Input)
	})

	t.Run("add replaces existing id and appends new", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{ToolCall{ID: "a", Name: "old"}}}
		m.AddToolCall(ToolCall{ID: "a", Name: "new"})
		require.Len(t, m.ToolCalls(), 1)
		require.Equal(t, "new", m.ToolCalls()[0].Name)

		m.AddToolCall(ToolCall{ID: "b"})
		require.Len(t, m.ToolCalls(), 2)
	})

	t.Run("set replaces all calls, keeps other parts", func(t *testing.T) {
		t.Parallel()
		m := Message{Parts: []ContentPart{TextContent{Text: "keep"}, ToolCall{ID: "a"}}}
		m.SetToolCalls([]ToolCall{{ID: "b"}, {ID: "c"}})
		require.Len(t, m.Parts, 3)
		require.Equal(t, "keep", m.Content().Text)
		require.Len(t, m.ToolCalls(), 2)
	})

	t.Run("add and set results", func(t *testing.T) {
		t.Parallel()
		var m Message
		m.AddToolResult(ToolResult{ToolCallID: "a"})
		m.SetToolResults([]ToolResult{{ToolCallID: "b"}, {ToolCallID: "c"}})
		require.Len(t, m.ToolResults(), 3)
	})
}

func TestMessageAddFinishReplacesExisting(t *testing.T) {
	t.Parallel()

	m := Message{}
	m.AddFinish(FinishReasonMaxTokens, "first", "")
	m.AddFinish(FinishReasonEndTurn, "second", "details")

	require.True(t, m.IsFinished())
	finish := m.FinishPart()
	require.Equal(t, FinishReasonEndTurn, finish.Reason)
	require.Equal(t, "second", finish.Message)
	require.Equal(t, "details", finish.Details)
	require.Len(t, m.Parts, 1)
}

func TestMessageAddImageAndBinary(t *testing.T) {
	t.Parallel()

	var m Message
	m.AddImageURL("https://example.com", "low")
	m.AddBinary("image/png", []byte{7, 8})

	require.Equal(t, "https://example.com", m.ImageURLContent()[0].URL)
	require.Equal(t, "low", m.ImageURLContent()[0].Detail)
	require.Equal(t, []byte{7, 8}, m.BinaryContent()[0].Data)
}

func TestBinaryContentString(t *testing.T) {
	t.Parallel()

	bc := BinaryContent{MIMEType: "image/png", Data: []byte("data")}

	openAI := bc.String(catwalk.InferenceProviderOpenAI)
	require.Equal(t, "data:image/png;base64,ZGF0YQ==", openAI)

	other := bc.String(catwalk.InferenceProviderAnthropic)
	require.Equal(t, "ZGF0YQ==", other)
}

func TestAttachmentJSONRoundTrip(t *testing.T) {
	t.Parallel()

	src := Attachment{
		FilePath: "/tmp/a",
		FileName: "a",
		MimeType: "text/plain",
		Content:  []byte{0, 1, 2, 255},
	}

	raw, err := json.Marshal(src)
	require.NoError(t, err)

	var got Attachment
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, src, got)
}

func TestAttachmentUnmarshalRejectsBadBase64(t *testing.T) {
	t.Parallel()

	var a Attachment
	require.Error(t, json.Unmarshal([]byte(`{"content":"!!!"}`), &a))
}

func TestAttachmentConversions(t *testing.T) {
	t.Parallel()

	msg := message.Attachment{FilePath: "p", FileName: "n", MimeType: "m", Content: []byte{1}}
	p := AttachmentFromMessage(msg)
	require.Equal(t, Attachment{FilePath: "p", FileName: "n", MimeType: "m", Content: []byte{1}}, p)
	require.Equal(t, msg, p.ToMessage())

	roundTripped := AttachmentsToMessage(AttachmentsFromMessage([]message.Attachment{msg}))
	require.Equal(t, []message.Attachment{msg}, roundTripped)
}

func TestAgentEventJSONErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("nil error omits field", func(t *testing.T) {
		t.Parallel()
		raw, err := json.Marshal(AgentEvent{Type: AgentEventTypeResponse})
		require.NoError(t, err)
		require.NotContains(t, string(raw), `"error"`)

		var got AgentEvent
		require.NoError(t, json.Unmarshal(raw, &got))
		require.NoError(t, got.Error)
	})

	t.Run("error round trips as string", func(t *testing.T) {
		t.Parallel()
		raw, err := json.Marshal(AgentEvent{Type: AgentEventTypeError, Error: errors.New("boom")})
		require.NoError(t, err)

		var got AgentEvent
		require.NoError(t, json.Unmarshal(raw, &got))
		require.EqualError(t, got.Error, "boom")
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		var got AgentEvent
		require.Error(t, json.Unmarshal([]byte(`{`), &got))
	})
}

func TestLSPEventJSONErrorHandling(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(LSPEvent{Type: LSPEventStateChanged, Name: "gopls", Error: errors.New("down")})
	require.NoError(t, err)

	var got LSPEvent
	require.NoError(t, json.Unmarshal(raw, &got))
	require.EqualError(t, got.Error, "down")
	require.Equal(t, "gopls", got.Name)

	var none LSPEvent
	require.NoError(t, json.Unmarshal([]byte(`{"type":"state_changed"}`), &none))
	require.NoError(t, none.Error)

	var bad LSPEvent
	require.Error(t, json.Unmarshal([]byte(`{`), &bad))
}

func TestLSPClientInfoJSONErrorHandling(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(LSPClientInfo{Name: "gopls", Error: errors.New("down")})
	require.NoError(t, err)

	var got LSPClientInfo
	require.NoError(t, json.Unmarshal(raw, &got))
	require.EqualError(t, got.Error, "down")

	var none LSPClientInfo
	require.NoError(t, json.Unmarshal([]byte(`{"name":"gopls"}`), &none))
	require.NoError(t, none.Error)

	var bad LSPClientInfo
	require.Error(t, json.Unmarshal([]byte(`{`), &bad))
}

func TestTextMarshalers(t *testing.T) {
	t.Parallel()

	role := Assistant
	out, err := role.MarshalText()
	require.NoError(t, err)
	require.Equal(t, "assistant", string(out))

	var roleBack MessageRole
	require.NoError(t, roleBack.UnmarshalText([]byte("user")))
	require.Equal(t, User, roleBack)

	reason := FinishReasonToolUse
	out, err = reason.MarshalText()
	require.NoError(t, err)
	require.Equal(t, "tool_use", string(out))

	var reasonBack FinishReason
	require.NoError(t, reasonBack.UnmarshalText([]byte("max_tokens")))
	require.Equal(t, FinishReasonMaxTokens, reasonBack)

	evt := AgentEventTypeSummarize
	out, err = evt.MarshalText()
	require.NoError(t, err)
	require.Equal(t, "summarize", string(out))

	var evtBack AgentEventType
	require.NoError(t, evtBack.UnmarshalText([]byte("error")))
	require.Equal(t, AgentEventTypeError, evtBack)

	action := PermissionAllow
	out, err = action.MarshalText()
	require.NoError(t, err)
	require.Equal(t, "allow", string(out))

	var actionBack PermissionAction
	require.NoError(t, actionBack.UnmarshalText([]byte("deny")))
	require.Equal(t, PermissionDeny, actionBack)

	lspType := LSPEventDiagnosticsChanged
	out, err = lspType.MarshalText()
	require.NoError(t, err)
	require.Equal(t, "diagnostics_changed", string(out))

	var lspBack LSPEventType
	require.NoError(t, lspBack.UnmarshalText([]byte("state_changed")))
	require.Equal(t, LSPEventStateChanged, lspBack)
}

func TestZeroChecks(t *testing.T) {
	t.Parallel()

	require.True(t, AgentInfo{}.IsZero())
	require.False(t, AgentInfo{IsBusy: true}.IsZero())
	require.False(t, AgentInfo{IsReady: true}.IsZero())
	require.False(t, AgentInfo{Model: catwalk.Model{ID: "m"}}.IsZero())

	require.True(t, AgentSession{}.IsZero())
	require.False(t, AgentSession{IsBusy: true}.IsZero())
	require.False(t, AgentSession{Session: Session{ID: "s"}}.IsZero())
}

func TestContentPartStringers(t *testing.T) {
	t.Parallel()

	require.Equal(t, "thinking", ReasoningContent{Thinking: "thinking"}.String())
	require.Equal(t, "text", TextContent{Text: "text"}.String())
	require.Equal(t, "https://x", ImageURLContent{URL: "https://x"}.String())
}
