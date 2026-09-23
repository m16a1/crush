package model

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
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

// finishStreamedStep closes a streamed step the way the agent does: the finish
// part lands on the message, and the usage follows on the session.
func finishStreamedStep(t *testing.T, m *UI, msg *message.Message, promptTokens, completionTokens int64) {
	t.Helper()
	msg.Parts = append(msg.Parts, message.Finish{
		Reason: message.FinishReasonEndTurn,
		Time:   time.Now().Unix(),
	})
	msg.SetFinishUsage(promptTokens, completionTokens)
	m.session.PromptTokens = promptTokens
	m.session.CompletionTokens = completionTokens
	_ = m.updateSessionMessage(*msg)
}

// TestStreamMetricsInAssistantFooter drives the live path the pubsub events
// take: a new assistant message starts a step, the first streamed text marks
// its time to first token, and the finished step plus its reported usage give
// the numbers shown in the footer. The sidebar reports the session averages.
func TestStreamMetricsInAssistantFooter(t *testing.T) {
	m := newMetricsTestUI()
	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)

	msg := streamedAssistantMessage("a-metrics")
	_ = m.appendSessionMessage(msg)
	_ = m.updateSessionMessage(msg)

	time.Sleep(2 * time.Millisecond)
	finishStreamedStep(t, m, &msg, 12_300, 250)

	infoItem := m.chat.MessageItem(chat.AssistantInfoID("a-metrics"))
	require.NotNil(t, infoItem, "the finished turn renders an assistant footer")

	rendered := ansi.Strip(infoItem.Render(120))
	require.Contains(t, rendered, "ttft ")
	require.Contains(t, rendered, "tps")
	require.Contains(t, rendered, "↑12.3K")
	require.Contains(t, rendered, "↓250")
	require.NotContains(t, rendered, "avg", "the footer reports one response, not the session average")

	// The sidebar carries the session's averages instead.
	sidebar := ansi.Strip(m.modelInfo(120))
	require.Contains(t, sidebar, "avg tps: ↑")
	require.Contains(t, sidebar, "tps")
	require.NotContains(t, sidebar, "↓250", "the sidebar does not repeat per-response counts")

	// The averages only move once a step has been measured.
	average := ansi.Strip(strings.Join(common.MetricsStatusLines(200), " "))
	require.NotContains(t, average, "avg tps: ↑-")

	// A second step of the same turn is measured on its own.
	second := streamedAssistantMessage("a-metrics-2")
	_ = m.appendSessionMessage(second)
	_ = m.updateSessionMessage(second)

	time.Sleep(2 * time.Millisecond)
	finishStreamedStep(t, m, &second, 14_100, 200)

	secondItem := m.chat.MessageItem(chat.AssistantInfoID("a-metrics-2"))
	require.NotNil(t, secondItem)
	require.NotContains(t, ansi.Strip(secondItem.Render(120)), "avg ")

	// The sidebar is 32 columns wide, so each status line has to fit the
	// column instead of spilling onto the chat area.
	narrowLines := strings.Split(ansi.Strip(m.modelInfo(30)), "\n")
	for _, line := range narrowLines {
		require.LessOrEqual(t, lipgloss.Width(line), 30, "sidebar line %q overflows the column", line)
	}
	require.Contains(t, strings.Join(narrowLines, "\n"), "avg tps: ↑")
}

// TestStreamMetricsIgnoredForReplayedMessages makes sure a message this process
// never timed (replayed history) shows no generation timings, while the token
// counts stored on it survive.
func TestStreamMetricsIgnoredForReplayedMessages(t *testing.T) {
	m := newMetricsTestUI()
	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)

	msg := streamedAssistantMessage("a-history")
	msg.Parts = append(msg.Parts, message.Finish{
		Reason: message.FinishReasonEndTurn,
		Time:   time.Now().Unix(),
	})
	msg.SetFinishUsage(12_300, 456)
	_ = m.updateSessionMessage(msg)

	require.Empty(t, common.MetricsForWidth("a-history", 0, 0, 200))

	infoItem := m.chat.MessageItem(chat.AssistantInfoID("a-history"))
	require.NotNil(t, infoItem)
	rendered := ansi.Strip(infoItem.Render(120))
	require.NotContains(t, rendered, "tps")
	require.Contains(t, rendered, "↑12.3K")
	require.Contains(t, rendered, "↓456")
}

// TestLivePromptEstimateCountsNewInputs: providers size a request only when it
// answers, so a request in flight is estimated from the last measured prompt
// plus the output it produced and the inputs the UI appended since. A follow-up
// user message and a tool result both show up before the provider's own count
// arrives and replaces the estimate.
func TestLivePromptEstimateCountsNewInputs(t *testing.T) {
	m := newMetricsTestUI()
	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)

	first := streamedAssistantMessage("a-prompt-1")
	_ = m.appendSessionMessage(first)
	_ = m.updateSessionMessage(first)
	time.Sleep(2 * time.Millisecond)
	finishStreamedStep(t, m, &first, 10_000, 200)

	// A follow-up user message is part of the next request's prompt.
	followUp := message.Message{
		ID:        "u-prompt-1",
		SessionID: "s1",
		Role:      message.User,
		Parts:     []message.ContentPart{message.TextContent{Text: strings.Repeat("a", 400)}},
	}
	_ = m.appendSessionMessage(followUp)

	second := streamedAssistantMessage("a-prompt-2")
	_ = m.appendSessionMessage(second)
	// 10K measured, plus the 200 the response produced, plus 100 for the
	// 400-character user message.
	require.Contains(t, common.MetricsStatus(), "↑10.3K")

	// The reported count covers the estimate and closes it out.
	time.Sleep(2 * time.Millisecond)
	finishStreamedStep(t, m, &second, 11_000, 300)

	// A tool result lands for the request after it.
	toolResult := message.Message{
		ID:        "t-prompt-1",
		SessionID: "s1",
		Role:      message.Tool,
		Parts: []message.ContentPart{message.ToolResult{
			ToolCallID: "tc1",
			Name:       "bash",
			Content:    strings.Repeat("b", 800),
		}},
	}
	_ = m.appendSessionMessage(toolResult)

	third := streamedAssistantMessage("a-prompt-3")
	_ = m.appendSessionMessage(third)
	// 11K reported, plus 300 output, plus 200 for the 800-character result.
	require.Contains(t, common.MetricsStatus(), "↑11.5K")
}

// TestSidebarReportsNoAveragesWithoutMeasurements pins the "-" state a new
// session, a reopened session and a model change all start from.
func TestSidebarReportsNoAveragesWithoutMeasurements(t *testing.T) {
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)

	require.Equal(t, []string{"avg tps: ↑- · ↓-"}, common.MetricsStatusLines(200))
}

// TestSwitchingSessionsResetsGenerationAverages: the averages describe the
// session they were measured in, so loading another session starts them over.
func TestSwitchingSessionsResetsGenerationAverages(t *testing.T) {
	pinTTLs(t)

	ws := &countingWorkspace{ready: true}
	m := newBusyUI(ws)
	m.session = &session.Session{ID: "metrics-test-session"}

	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)
	common.SessionChanged("metrics-test-session")

	common.StartStep("m1")
	common.MarkFirstToken()
	time.Sleep(2 * time.Millisecond)
	common.MarkStepFinished()
	common.FinishStep(12_300, 200)
	require.NotContains(t, common.MetricsStatusLines(200)[0], "avg tps: ↑-")

	_, cmd := m.Update(loadSessionMsg{session: &session.Session{ID: "metrics-test-other"}})
	runCmds(m, cmd)

	require.Equal(t, "avg tps: ↑- · ↓-", common.MetricsStatusLines(200)[0])
}
