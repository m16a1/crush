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

// TestShouldShowAssistantInfoForToolUseTurns pins which turns get a footer.
// A tool-use turn does, because its metrics are what show a tool-driven
// session is still moving; a turn that ended for any other reason does not,
// unless it was routed through Prism.
func TestShouldShowAssistantInfoForToolUseTurns(t *testing.T) {
	t.Parallel()

	finished := func(reason message.FinishReason) *message.Message {
		return &message.Message{Parts: []message.ContentPart{message.Finish{Reason: reason}}}
	}

	require.True(t, ShouldShowAssistantInfo(finished(message.FinishReasonEndTurn)))
	require.True(t, ShouldShowAssistantInfo(finished(message.FinishReasonToolUse)))

	require.False(t, ShouldShowAssistantInfo(finished(message.FinishReasonMaxTokens)))
	require.False(t, ShouldShowAssistantInfo(finished(message.FinishReasonContentFilter)))
	require.False(t, ShouldShowAssistantInfo(&message.Message{}))

	routed := finished(message.FinishReasonMaxTokens)
	routed.PrismModelName = "GLM 5.3"
	require.True(t, ShouldShowAssistantInfo(routed), "a Prism-routed turn is shown however it ended")
}

// TestAssistantInfoItemShowsMetricsOnToolUseTurns: a turn that hands off to a
// tool gets the same generation metrics as the final turn, laid out as a
// compact heading. Without it a long run of tool calls shows nothing between
// the tool items to say how fast the model is producing them.
func TestAssistantInfoItemShowsMetricsOnToolUseTurns(t *testing.T) {
	sty := styles.CharmtonePantera()

	common.StartTurn()
	t.Cleanup(common.StopTurn)
	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)
	measureTestStep("a-tool", 200)

	msg := &message.Message{
		ID:       "a-tool",
		Role:     message.Assistant,
		Model:    "gpt-x",
		Provider: "openai",
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc1", Name: "edit", Input: "{}", Finished: true},
			message.Finish{Reason: message.FinishReasonToolUse, Time: 1735689600},
		},
	}
	msg.SetFinishUsage(12_300, 456)

	item := NewAssistantInfoItem(&sty, msg, metricsTestConfig(), time.Unix(1735689590, 0))
	rendered := ansi.Strip(item.Render(120))
	require.Regexp(t, `Unknown Model · ttft `, rendered, "the heading keeps the model name, then the step metrics")
	require.Contains(t, rendered, "tps")
	require.Contains(t, rendered, "↑12.3K")
	require.Contains(t, rendered, "↓456")
	require.NotContains(t, rendered, "via ", "the compact heading drops the provider")
}

// TestAssistantInfoItemToolUseHeadingSurvivesNarrowWidths: the compact heading
// keeps the model name and drops metrics as the column narrows, just as the
// final footer does.
func TestAssistantInfoItemToolUseHeadingSurvivesNarrowWidths(t *testing.T) {
	sty := styles.CharmtonePantera()

	common.ResetMetrics()
	t.Cleanup(common.ResetMetrics)

	msg := &message.Message{
		ID:       "a-narrow",
		Role:     message.Assistant,
		Model:    "gpt-x",
		Provider: "openai",
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc1", Name: "edit", Input: "{}", Finished: true},
			message.Finish{Reason: message.FinishReasonToolUse, Time: 1735689600},
		},
	}
	msg.SetFinishUsage(12_300, 456)

	render := func(width int) string {
		item := NewAssistantInfoItem(&sty, msg, metricsTestConfig(), time.Unix(1735689590, 0))
		return ansi.Strip(item.Render(width))
	}

	require.Contains(t, render(120), "↑12.3K", "a wide heading keeps the token counts")
	require.NotContains(t, render(20), "↑12.3K", "no metrics when there is no room at all")
	require.Contains(t, render(20), "Unknown Model", "the model name is what stays")
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
