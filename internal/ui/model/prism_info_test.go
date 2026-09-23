package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// prismWorkspace is a minimal [workspace.Workspace] stub providing the
// methods the assistant-info rendering path needs.
type prismWorkspace struct {
	workspace.Workspace
}

func (w *prismWorkspace) Config() *config.Config { return &config.Config{} }
func (w *prismWorkspace) WorkingDir() string     { return "/" }

func newPrismTestUI() *UI {
	com := common.DefaultCommon(&prismWorkspace{})
	return &UI{
		com:     com,
		status:  NewStatus(com, nil),
		chat:    NewChat(com, config.ScrollbarDefault),
		state:   uiChat,
		focus:   uiFocusEditor,
		width:   140,
		height:  45,
		session: &session.Session{ID: "s1"},
		keyMap:  DefaultKeyMap(),
		dialog:  dialog.NewOverlay(),
	}
}

func prismToolTurn(finished bool, prismName string) message.Message {
	parts := []message.ContentPart{
		message.ToolCall{ID: "tc1", Name: "bash", Input: "{}", Finished: true},
	}
	if finished {
		parts = append(parts, message.Finish{Reason: message.FinishReasonToolUse, Time: 1735689600})
	}
	return message.Message{
		ID:             "a1",
		SessionID:      "s1",
		Role:           message.Assistant,
		Model:          "prism-model",
		Provider:       "hyper",
		Parts:          parts,
		PrismModelName: prismName,
	}
}

// TestUpdateSessionMessage_PrismInfoBetweenTurns reproduces the between-turn
// flow: an assistant message streams without a finish part, then the turn
// finishes as a tool-use turn with a Prism-routed model name. The info item
// must appear once the routed name is available.
func TestUpdateSessionMessage_PrismInfoBetweenTurns(t *testing.T) {
	m := newPrismTestUI()

	// Turn streams: no finish part, no prism info item yet.
	_ = m.appendSessionMessage(prismToolTurn(false, ""))
	require.Nil(t, m.chat.MessageItem(chat.AssistantInfoID("a1")))

	// An update mid-stream changes nothing.
	_ = m.updateSessionMessage(prismToolTurn(false, ""))
	require.Nil(t, m.chat.MessageItem(chat.AssistantInfoID("a1")))

	// Turn finishes as a tool-use turn with a routed model name: the
	// info item must be appended.
	_ = m.updateSessionMessage(prismToolTurn(true, "GLM 5.3"))
	require.NotNil(t, m.chat.MessageItem(chat.AssistantInfoID("a1")), "expected assistant info item between turns")

	// A later update for the same turn must not remove it.
	_ = m.updateSessionMessage(prismToolTurn(true, "GLM 5.3"))
	require.NotNil(t, m.chat.MessageItem(chat.AssistantInfoID("a1")))
}

// turnWithReason builds a finished assistant turn with the given finish reason
// and Prism-routed model name.
func turnWithReason(id string, reason message.FinishReason, prismName string) message.Message {
	return message.Message{
		ID:        id,
		SessionID: "s1",
		Role:      message.Assistant,
		Model:     "prism-model",
		Provider:  "hyper",
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc1", Name: "bash", Input: "{}", Finished: true},
			message.Finish{Reason: reason, Time: 1735689600},
		},
		PrismModelName: prismName,
	}
}

// TestUpdateSessionMessage_ToolUseInfoWithoutPrismName: a turn that hands off
// to a tool gets a footer even without a Prism-routed model name, because the
// footer carries the step's own generation metrics. A turn that ended for
// another reason still gets none, so the chat is not filled with footers that
// add nothing.
func TestUpdateSessionMessage_ToolUseInfoWithoutPrismName(t *testing.T) {
	m := newPrismTestUI()

	_ = m.appendSessionMessage(prismToolTurn(false, ""))
	_ = m.updateSessionMessage(prismToolTurn(true, ""))
	require.NotNil(t, m.chat.MessageItem(chat.AssistantInfoID("a1")), "a tool-use turn shows its step metrics")

	other := newPrismTestUI()
	_ = other.appendSessionMessage(turnWithReason("a2", message.FinishReasonMaxTokens, ""))
	_ = other.updateSessionMessage(turnWithReason("a2", message.FinishReasonMaxTokens, ""))
	require.Nil(t, other.chat.MessageItem(chat.AssistantInfoID("a2")))
}
