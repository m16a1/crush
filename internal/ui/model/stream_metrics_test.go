package model

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// metricsWorkspace is a workspace stub whose config has the provider map the
// assistant footer renders through.
type metricsWorkspace struct {
	prismWorkspace
}

func (w *metricsWorkspace) Config() *config.Config {
	return &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
}

func newMetricsTestUI() *UI {
	m := newPrismTestUI()
	m.com = common.DefaultCommon(&metricsWorkspace{})
	m.status = NewStatus(m.com, nil)
	m.chat = NewChat(m.com, config.ScrollbarDefault)
	return m
}

func streamedAssistantMessage(id string) message.Message {
	return message.Message{
		ID:        id,
		SessionID: "s1",
		Role:      message.Assistant,
		Model:     "gpt-x",
		Provider:  "openai",
		Parts:     []message.ContentPart{message.TextContent{Text: "hello"}},
	}
}

// TestStreamMetricsInAssistantFooter drives the live path the pubsub events
// take: a new assistant message starts a step, the first streamed text marks
// its time to first token, and the finished step plus its reported usage give
// the throughput shown in the footer. A second step adds the turn average.
func TestStreamMetricsInAssistantFooter(t *testing.T) {
	m := newMetricsTestUI()
	common.StartTurn()
	t.Cleanup(common.StopTurn)

	msg := streamedAssistantMessage("a-metrics")
	_ = m.appendSessionMessage(msg)
	_ = m.updateSessionMessage(msg)

	time.Sleep(2 * time.Millisecond)
	m.session.CompletionTokens = 250

	msg.Parts = append(msg.Parts, message.Finish{
		Reason: message.FinishReasonEndTurn,
		Time:   time.Now().Unix(),
	})
	_ = m.updateSessionMessage(msg)

	infoItem := m.chat.MessageItem(chat.AssistantInfoID("a-metrics"))
	require.NotNil(t, infoItem, "the finished turn renders an assistant footer")

	rendered := ansi.Strip(infoItem.Render(120))
	require.Contains(t, rendered, "ttft ")
	require.Contains(t, rendered, "tok/s")
	require.Contains(t, rendered, "avg ", "the running average is available from the first step")

	// A second step of the same turn reports the average of both steps.
	second := streamedAssistantMessage("a-metrics-2")
	_ = m.appendSessionMessage(second)
	_ = m.updateSessionMessage(second)

	time.Sleep(2 * time.Millisecond)
	m.session.CompletionTokens = 200

	second.Parts = append(second.Parts, message.Finish{
		Reason: message.FinishReasonEndTurn,
		Time:   time.Now().Unix(),
	})
	_ = m.updateSessionMessage(second)

	secondItem := m.chat.MessageItem(chat.AssistantInfoID("a-metrics-2"))
	require.NotNil(t, secondItem)
	require.Contains(t, ansi.Strip(secondItem.Render(120)), "avg ")
	require.Contains(t, ansi.Strip(m.modelInfo(120)), "avg ")

	// The sidebar keeps the timings visible after the turn ends.
	sidebar := ansi.Strip(m.modelInfo(120))
	require.Contains(t, sidebar, "ttft ")
	require.Contains(t, sidebar, "tok/s")

	// The sidebar is 32 columns wide, so each status line has to fit the
	// column instead of spilling onto the chat area.
	narrowLines := strings.Split(ansi.Strip(m.modelInfo(30)), "\n")
	for _, line := range narrowLines {
		require.LessOrEqual(t, lipgloss.Width(line), 30, "sidebar line %q overflows the column", line)
	}
	require.Contains(t, strings.Join(narrowLines, "\n"), "tok/s")
}

// TestStreamMetricsIgnoredForReplayedMessages makes sure a message this process
// never timed (replayed history) shows no generation metrics.
func TestStreamMetricsIgnoredForReplayedMessages(t *testing.T) {
	m := newMetricsTestUI()
	common.StartTurn()
	t.Cleanup(common.StopTurn)

	msg := streamedAssistantMessage("a-history")
	msg.Parts = append(msg.Parts, message.Finish{
		Reason: message.FinishReasonEndTurn,
		Time:   time.Now().Unix(),
	})
	_ = m.updateSessionMessage(msg)

	require.Empty(t, common.MetricsFor("a-history"))
	infoItem := m.chat.MessageItem(chat.AssistantInfoID("a-history"))
	require.NotNil(t, infoItem)
	require.NotContains(t, ansi.Strip(infoItem.Render(120)), "tok/s")
}
