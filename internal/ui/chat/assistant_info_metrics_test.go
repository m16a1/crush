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

func TestAssistantInfoItemShowsGenerationMetrics(t *testing.T) {
	sty := styles.CharmtonePantera()
	msg := timedAssistantMessage("a1")

	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.StartStep(msg.ID)
	common.MarkFirstToken()
	time.Sleep(2 * time.Millisecond)
	common.MarkStepFinished()
	common.FinishStep(120)

	item := NewAssistantInfoItem(&sty, msg, metricsTestConfig(), time.Unix(1735689590, 0))
	rendered := ansi.Strip(item.Render(120))
	require.Contains(t, rendered, "ttft ")
	require.Contains(t, rendered, "tok/s")
}

func TestAssistantInfoItemOmitsMetricsForUntimedMessages(t *testing.T) {
	sty := styles.CharmtonePantera()

	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.StartStep("a1")
	common.MarkFirstToken()

	item := NewAssistantInfoItem(&sty, timedAssistantMessage("a2"), metricsTestConfig(), time.Unix(1735689590, 0))
	rendered := ansi.Strip(item.Render(120))
	require.NotContains(t, rendered, "ttft ")
	require.NotContains(t, rendered, "tok/s")
}

func TestAssistantInfoItemTrimsMetricsToAvailableWidth(t *testing.T) {
	sty := styles.CharmtonePantera()

	common.StartTurn()
	t.Cleanup(common.StopTurn)
	// Two steps so the turn average is available too.
	for _, id := range []string{"a-width-1", "a-width-2"} {
		common.StartStep(id)
		common.MarkFirstToken()
		time.Sleep(2 * time.Millisecond)
		common.MarkStepFinished()
		common.FinishStep(500)
	}
	msg := timedAssistantMessage("a-width-2")

	render := func(width int) string {
		item := NewAssistantInfoItem(&sty, msg, metricsTestConfig(), time.Unix(1735689590, 0))
		return ansi.Strip(item.Render(width))
	}

	require.Contains(t, render(100), "avg ", "a wide footer keeps every measurement")
	require.NotContains(t, render(60), "avg ", "the average is dropped first")
	require.Contains(t, render(60), "tok/s")
	require.Contains(t, render(44), "ttft ", "the time to first token outlives the rate")
	require.NotContains(t, render(44), "tok/s", "the rate is dropped rather than cut in half")
	require.NotContains(t, render(30), "ttft ", "no metrics when there is no room at all")
}

func TestAssistantInfoItemRefreshesMetricsAfterInvalidation(t *testing.T) {
	sty := styles.CharmtonePantera()
	msg := timedAssistantMessage("a-refresh")

	common.StartTurn()
	t.Cleanup(common.StopTurn)

	item := NewAssistantInfoItem(&sty, msg, metricsTestConfig(), time.Unix(1735689590, 0)).(*AssistantInfoItem)
	require.NotContains(t, ansi.Strip(item.Render(120)), "tok/s")

	// The step is timed after the footer was first drawn, as happens when the
	// session usage lands after the finish part.
	common.StartStep(msg.ID)
	common.MarkFirstToken()
	time.Sleep(2 * time.Millisecond)
	common.MarkStepFinished()
	common.FinishStep(120)
	item.InvalidateMetrics()

	rendered := ansi.Strip(item.Render(120))
	require.Contains(t, rendered, "ttft ")
	require.Contains(t, rendered, "tok/s")
}
