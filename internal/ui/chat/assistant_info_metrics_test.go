package chat

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// metricsTestConfig returns a config with an initialized provider map, which
// the assistant footer renders through.
func metricsTestConfig() *config.Config {
	return &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
}

// timedAssistantMessage returns an assistant message that ended the turn.
func timedAssistantMessage(id string) *message.Message {
	return &message.Message{
		ID:       id,
		Role:     message.Assistant,
		Model:    "gpt-x",
		Provider: "openai",
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello"},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 1735689600},
		},
	}
}

// timedAssistantMessageWithUsage returns an assistant message that ended the
// turn and carries the token usage the provider reported for it.
func timedAssistantMessageWithUsage(id string, promptTokens, completionTokens int64) *message.Message {
	msg := timedAssistantMessage(id)
	msg.SetFinishUsage(promptTokens, completionTokens)
	return msg
}

// measureTestStep times a step of the given message over a real generation
// window, which is what the decode speed needs.
func measureTestStep(id string, tokens int64) {
	common.StartStep(id)
	common.MarkFirstToken()
	time.Sleep(2 * time.Millisecond)
	common.MarkStepFinished()
	common.FinishStep(12_300, tokens)
}

func TestAssistantInfoItemShowsGenerationMetrics(t *testing.T) {
	sty := styles.CharmtonePantera()
	msg := timedAssistantMessageWithUsage("a1", 12_300, 456)

	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)
	measureTestStep(msg.ID, 120)

	item := NewAssistantInfoItem(&sty, msg, metricsTestConfig(), time.Unix(1735689590, 0))
	rendered := ansi.Strip(item.Render(120))
	require.Contains(t, rendered, "ttft ")
	require.Contains(t, rendered, "tps")
	require.Contains(t, rendered, "↑12.3K")
	require.Contains(t, rendered, "↓456")
	require.NotContains(t, rendered, "avg", "the footer reports one response, not the session average")
}

func TestAssistantInfoItemOmitsMetricsForUntimedMessages(t *testing.T) {
	sty := styles.CharmtonePantera()

	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)
	common.StartStep("a1")
	common.MarkFirstToken()

	item := NewAssistantInfoItem(&sty, timedAssistantMessage("a2"), metricsTestConfig(), time.Unix(1735689590, 0))
	rendered := ansi.Strip(item.Render(120))
	require.NotContains(t, rendered, "ttft ")
	require.NotContains(t, rendered, "tps")
}

func TestAssistantInfoItemShowsTokenCountsAfterAReload(t *testing.T) {
	sty := styles.CharmtonePantera()

	// A session reopened in a fresh process has no timings, but the counts
	// stored on the message are still there.
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)

	msg := timedAssistantMessageWithUsage("a-replay", 12_300, 456)
	item := NewAssistantInfoItem(&sty, msg, metricsTestConfig(), time.Unix(1735689590, 0))
	rendered := ansi.Strip(item.Render(120))
	require.Contains(t, rendered, "↑12.3K")
	require.Contains(t, rendered, "↓456")
	require.NotContains(t, rendered, "ttft ")
}

func TestAssistantInfoItemTrimsMetricsToAvailableWidth(t *testing.T) {
	sty := styles.CharmtonePantera()

	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)
	measureTestStep("a-width", 500)
	msg := timedAssistantMessageWithUsage("a-width", 12_300, 456)

	render := func(width int) string {
		item := NewAssistantInfoItem(&sty, msg, metricsTestConfig(), time.Unix(1735689590, 0))
		return ansi.Strip(item.Render(width))
	}

	require.Contains(t, render(120), "tps", "a wide footer keeps every measurement")
	require.Contains(t, render(120), "↓456")

	require.NotContains(t, render(64), "tps", "the decode speed is dropped first")
	require.Contains(t, render(64), "ttft ")
	require.Contains(t, render(64), "↑12.3K", "the token counts outlive the timings")

	require.NotContains(t, render(52), "ttft ", "then the time to first token goes")
	require.Contains(t, render(52), "↓456")

	require.NotContains(t, render(36), "↓456", "no metrics when there is no room at all")
}

func TestAssistantInfoItemRefreshesMetricsAfterInvalidation(t *testing.T) {
	sty := styles.CharmtonePantera()
	msg := timedAssistantMessageWithUsage("a-refresh", 12_300, 456)

	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)

	item := NewAssistantInfoItem(&sty, msg, metricsTestConfig(), time.Unix(1735689590, 0)).(*AssistantInfoItem)
	require.NotContains(t, ansi.Strip(item.Render(120)), "tps")

	// The step is timed after the footer was first drawn, as happens when the
	// session usage lands after the finish part.
	measureTestStep(msg.ID, 120)
	item.InvalidateMetrics()

	rendered := ansi.Strip(item.Render(120))
	require.Contains(t, rendered, "tps")
	require.Contains(t, rendered, "↑12.3K")
}
