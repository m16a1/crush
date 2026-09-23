package common

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// tpsPattern matches the decode speed of a formatted metrics string.
var tpsPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?) tps`)

// sidebarPattern matches the average status line the sidebar renders: the
// session's prefill speed and its decode speed, both in tokens per second.
var sidebarPattern = regexp.MustCompile(`^avg tps: ↑([0-9.]+) · ↓([0-9.]+)$`)

// resetTracker clears the process-wide tracker between tests so cases do not
// leak timings into each other.
func resetTracker() {
	ResetMetrics()
	StopTurn()
}

// tpsOf extracts the decode speed from a formatted metrics string.
func tpsOf(t *testing.T, metrics string) float64 {
	t.Helper()
	match := tpsPattern.FindStringSubmatch(metrics)
	require.Len(t, match, 2, "no decode speed in %q", metrics)
	value, err := strconv.ParseFloat(match[1], 64)
	require.NoError(t, err)
	return value
}

// sidebarStatus returns the average status line the sidebar renders.
func sidebarStatus(t *testing.T) string {
	t.Helper()
	lines := MetricsStatusLines(200)
	require.Len(t, lines, 1)
	return lines[0]
}

// averageTPSOf extracts the average decode speed from the sidebar status.
func averageTPSOf(t *testing.T) float64 {
	t.Helper()
	match := sidebarPattern.FindStringSubmatch(sidebarStatus(t))
	require.Len(t, match, 3, "sidebar status does not match: %q", sidebarStatus(t))
	value, err := strconv.ParseFloat(match[2], 64)
	require.NoError(t, err)
	return value
}

// averagePrefillOf extracts the average prefill speed from the sidebar status.
func averagePrefillOf(t *testing.T) float64 {
	t.Helper()
	match := sidebarPattern.FindStringSubmatch(sidebarStatus(t))
	require.Len(t, match, 3, "sidebar status does not match: %q", sidebarStatus(t))
	value, err := strconv.ParseFloat(match[1], 64)
	require.NoError(t, err)
	return value
}

// measureStep times a step the given window long, reporting a prompt size with
// it so the step joins both of the session's averages.
func measureStep(t *testing.T, messageID string, tokens int64, window time.Duration) {
	t.Helper()
	measureStepWithPrompt(t, messageID, 1_000, tokens, window)
}

// measureStepWithPrompt times a step the given window long, having read the
// given prompt tokens before its first token.
func measureStepWithPrompt(t *testing.T, messageID string, promptTokens, tokens int64, window time.Duration) {
	t.Helper()
	StartStep(messageID)
	MarkFirstToken()
	time.Sleep(window)
	MarkStepFinished()
	FinishStep(promptTokens, tokens)
}

// measurePrefillStep times a step whose first token waits out the prefill
// before arriving, which is what the session's prefill speed is measured from.
func measurePrefillStep(t *testing.T, messageID string, promptTokens int64, prefill, decode time.Duration) {
	t.Helper()
	StartStep(messageID)
	time.Sleep(prefill)
	MarkFirstToken()
	time.Sleep(decode)
	MarkStepFinished()
	FinishStep(promptTokens, 10)
}

func TestMetricsStatusReportsElapsedBeforeAnythingIsMeasured(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	status := MetricsStatus()
	require.Contains(t, status, "0s", "the turn timer is live")
	require.Contains(t, status, "ttft -", "no time to first token yet")
	require.Contains(t, status, "- tps", "no decode speed yet")
	require.Contains(t, status, "↑-", "no input tokens yet")
	require.Contains(t, status, "↓-", "no output tokens yet")
}

func TestMetricsStatusKeepsTheElapsedTimeWhenTheTurnEnds(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	StopTurn()

	require.Regexp(t, `^ttft [0-9]+ms`, MetricsStatus())
}

func TestMarkFirstTokenRecordsTimeToFirstToken(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	require.Empty(t, MetricsForWidth("m1", 0, 0, 200), "no metrics before the first token")

	MarkFirstToken()
	metrics := MetricsForWidth("m1", 0, 0, 200)
	require.Regexp(t, `^ttft [0-9]+(ms|[0-9.]+s)$`, metrics)
	require.NotContains(t, metrics, "tps", "the decode speed needs the step to finish")
}

func TestFinishStepDerivesThroughputFromFrozenWindow(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	time.Sleep(2 * time.Millisecond)
	MarkStepFinished()
	FinishStep(0, 100)
	first := tpsOf(t, MetricsForWidth("m1", 0, 0, 200))

	// A later, more accurate count must refine the rate without stretching the
	// generation window.
	FinishStep(0, 200)
	require.InDelta(t, first*2, tpsOf(t, MetricsForWidth("m1", 0, 0, 200)), 1)
}

func TestFinishStepRecordsTheTokenCounts(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	time.Sleep(2 * time.Millisecond)
	MarkStepFinished()
	FinishStep(12_300, 456)

	require.Contains(t, MetricsStatus(), "↑12.3K")
	require.Contains(t, MetricsStatus(), "↓456")
}

func TestFinishStepIsIgnoredBeforeTheStepFinishes(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	FinishStep(0, 500)

	require.NotContains(t, MetricsForWidth("m1", 0, 0, 200), "tps")
}

func TestFinishStepIgnoresInstantSteps(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	// A response that arrives in one piece has no meaningful generation window,
	// so it reports no throughput instead of an absurd rate.
	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	MarkStepFinished()
	FinishStep(0, 500)

	require.NotContains(t, MetricsForWidth("m1", 0, 0, 200), "tps")
}

func TestAverageThroughputIsReportedFromTheFirstStep(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)

	// The average is the session's, so it is available as soon as a step has
	// been measured rather than waiting for a second step.
	require.InDelta(t, tpsOf(t, MetricsForWidth("m1", 0, 0, 200)), averageTPSOf(t), 1)
}

func TestAverageThroughputIsWeightedByGenerationTime(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 4, 2*time.Millisecond)
	measureStep(t, "m2", 400, 40*time.Millisecond)

	short := tpsOf(t, MetricsForWidth("m1", 0, 0, 200))
	long := tpsOf(t, MetricsForWidth("m2", 0, 0, 200))
	average := averageTPSOf(t)

	// The average is the session's total output over its total generation
	// time, so the long, token-heavy step dominates it instead of every step
	// casting an equal vote. A mean of the rates would sit halfway between
	// the two, giving the short step far more weight than it earned.
	require.Greater(t, average, (short+long)/2, "the long step carries the weight, not the short one")
	require.Greater(t, average, long*0.8, "the average belongs near the long step")
}

func TestAverageThroughputDoesNotLetAShortStepInflateIt(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	// A short response reports an absurd rate next to a longer, slower one.
	measureStep(t, "m1", 10, 2*time.Millisecond)
	measureStep(t, "m2", 200, 200*time.Millisecond)

	short := tpsOf(t, MetricsForWidth("m1", 0, 0, 200))
	long := tpsOf(t, MetricsForWidth("m2", 0, 0, 200))
	average := averageTPSOf(t)

	require.Greater(t, short, long*2, "the short step reads far faster")
	require.Less(t, average, 2*long, "its few tokens do not pull the session's speed up to its own")
	require.Less(t, average, (short+long)/2, "the mean of rates would be inflated")
}

// TestAveragePrefillSpeedIsStableAcrossPromptSizes: the session's prefill speed
// is the prompt tokens every measured step read over the time it took them to
// reach their first token. Because both grow together, the rate does not move
// with the prompt size, which is what a mean time to first token could not do.
func TestAveragePrefillSpeedIsStableAcrossPromptSizes(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measurePrefillStep(t, "m1", 1_000, 20*time.Millisecond, 10*time.Millisecond)
	measurePrefillStep(t, "m2", 10_000, 200*time.Millisecond, 10*time.Millisecond)

	// Both steps read their prompt at about fifty thousand tokens per second,
	// so the session reports that rate rather than a value the bigger prompt
	// dragged up or down.
	require.InDelta(t, 50_000, averagePrefillOf(t), 0.25*50_000)
}

// TestAveragePrefillSpeedIsWeightedByPromptTokens: a step that read a large
// prompt carries more of the session's prefill speed than a tiny one, the way
// the decode average is weighted by output tokens. A mean of the per-step rates
// would let the tiny step pull the session's speed to its own.
func TestAveragePrefillSpeedIsWeightedByPromptTokens(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measurePrefillStep(t, "m1", 100, 100*time.Millisecond, 10*time.Millisecond)
	measurePrefillStep(t, "m2", 10_000, 10*time.Millisecond, 10*time.Millisecond)

	require.Less(t, averagePrefillOf(t), 200_000.0, "the token-heavy step carries the average, not the mean of the rates")
}

// TestAveragePrefillSpeedIsRefinedNotDuplicated: a later, more accurate prompt
// count replaces the step's earlier share of the prefill speed rather than
// adding to it, so the step is never counted twice.
func TestAveragePrefillSpeedIsRefinedNotDuplicated(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measurePrefillStep(t, "m1", 1_000, 20*time.Millisecond, 10*time.Millisecond)
	before := averagePrefillOf(t)

	// The step turns out to have read twice the prompt over the same prefill.
	FinishStep(2_000, 10)

	require.InDelta(t, 2*before, averagePrefillOf(t), before*0.05, "the refined count replaces the earlier one")
}

func TestAverageThroughputIsRefinedNotDuplicated(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	measureStep(t, "m2", 100, 2*time.Millisecond)
	before := averageTPSOf(t)
	first := tpsOf(t, MetricsForWidth("m1", 0, 0, 200))

	// A refined count for a step that already contributed must replace its
	// share of the aggregate rather than add to it, so the totals still
	// cover exactly two steps: the untouched one and the refined one.
	FinishStep(0, 200)
	refined := tpsOf(t, MetricsForWidth("m2", 0, 0, 200))
	after := averageTPSOf(t)

	require.Greater(t, after, before)

	// The aggregate is the reported tokens over the windows they were
	// produced in, both of which are recoverable from the per-step rates.
	expected := (100.0 + 200.0) / (100.0/first + 200.0/refined)
	require.InDelta(t, expected, after, expected*0.01)

	// Counting the refined step twice would leave the aggregate diluted by
	// its earlier, slower count instead.
	require.Greater(t, after, (first+first+refined)/3)
}

func TestAverageThroughputCarriesAcrossTurns(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	firstRate := tpsOf(t, MetricsForWidth("m1", 0, 0, 200))

	StartTurn()
	measureStep(t, "m2", 100, 20*time.Millisecond)

	require.Less(t, averageTPSOf(t), firstRate, "earlier steps stay in the running average")

	// The finished step keeps the timings it had when it finished.
	require.InDelta(t, firstRate, tpsOf(t, MetricsForWidth("m1", 0, 0, 200)), 1)
}

func TestMetricsAreScopedToTheTimedMessage(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	MarkStepFinished()
	FinishStep(0, 120)

	require.NotEmpty(t, MetricsForWidth("m1", 0, 0, 200))
	require.Empty(t, MetricsForWidth("m2", 0, 0, 200), "another message never inherits timings")
	require.Empty(t, MetricsForWidth("", 0, 0, 200), "an empty message id has no timings")
	require.True(t, TrackedStep("m1"))
	require.False(t, TrackedStep("m2"))

	StartStep("m2")
	require.False(t, TrackedStep("m1"), "starting a step stops tracking the previous one")
	require.True(t, TrackedStep("m2"))
	require.NotEmpty(t, MetricsForWidth("m1", 0, 0, 200), "finished steps keep their timings")
}

func TestStartTurnDoesNotTimeUntrackedMessages(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	MarkFirstToken()
	MarkStepFinished()
	FinishStep(0, 120)

	require.Empty(t, MetricsForWidth("m1", 0, 0, 200), "tokens are only timed for tracked steps")
}

func TestTrackedStepsAreBounded(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	for i := range maxTrackedSteps + 10 {
		StartStep(strconv.Itoa(i))
		MarkFirstToken()
	}

	require.Empty(t, MetricsForWidth("0", 0, 0, 200), "the oldest step is evicted")
	require.NotEmpty(t, MetricsForWidth(strconv.Itoa(maxTrackedSteps+9), 0, 0, 200))
}

func TestMetricsStatusLinesAlwaysReportBothAverages(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	require.Equal(t, []string{"avg tps: ↑- · ↓-"}, MetricsStatusLines(30))
}

func TestMetricsStatusLinesKeepsOneLineWhenItFits(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	measureStep(t, "m2", 100, 2*time.Millisecond)

	lines := MetricsStatusLines(200)
	require.Len(t, lines, 1)
	require.Regexp(t, `^avg tps: ↑[0-9.]+ · ↓[0-9.]+$`, lines[0])
}

func TestMetricsStatusLinesWrapInsteadOfDropping(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	// Slow steps keep the average rate short, like real generation does.
	measureStepWithPrompt(t, "m1", 0, 4, 50*time.Millisecond)
	measureStepWithPrompt(t, "m2", 0, 4, 50*time.Millisecond)

	lines := MetricsStatusLines(13)
	require.Len(t, lines, 2, "the averages wrap instead of being dropped")
	require.Contains(t, lines[0], "avg tps: ↑")
	require.Contains(t, lines[1], "↓")
	for _, line := range lines {
		require.LessOrEqual(t, lipgloss.Width(line), 13)
	}
}

// TestMetricsStatusLinesFitsANarrowerColumnThanTheLabel: even a column too
// narrow for the label keeps every line inside it rather than spilling into the
// chat beside the sidebar.
func TestMetricsStatusLinesFitsANarrowerColumnThanTheLabel(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measurePrefillStep(t, "m1", 1_000, 20*time.Millisecond, 10*time.Millisecond)

	lines := MetricsStatusLines(5)
	require.Len(t, lines, 2)
	for _, line := range lines {
		require.LessOrEqual(t, lipgloss.Width(line), 5)
	}
}

func TestResetMetricsStartsTheAveragesOver(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	require.NotEqual(t, "avg tps: ↑- · ↓-", sidebarStatus(t), "the step is measured")

	ResetMetrics()

	require.Equal(t, "avg tps: ↑- · ↓-", sidebarStatus(t), "the averages are gone")
	require.Empty(t, MetricsForWidth("m1", 0, 0, 200), "per-step timings are gone")
	// The token counts live on the message, so a footer keeps showing them.
	require.Equal(t, "↑12.3K · ↓456", MetricsForWidth("m1", 12_300, 456, 200))
}

func TestMetricsForWidthKeepsTheTokenCounts(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 4, 50*time.Millisecond)

	require.Contains(t, MetricsForWidth("m1", 12_300, 456, 200), "tps", "everything fits when there is room")
	require.Contains(t, MetricsForWidth("m1", 12_300, 456, 200), "↑12.3K")
	require.Contains(t, MetricsForWidth("m1", 12_300, 456, 200), "↓456")

	require.NotContains(t, MetricsForWidth("m1", 12_300, 456, 24), "tps", "the decode speed goes first")
	require.Contains(t, MetricsForWidth("m1", 12_300, 456, 24), "ttft ")
	require.Contains(t, MetricsForWidth("m1", 12_300, 456, 24), "↑12.3K")

	require.NotContains(t, MetricsForWidth("m1", 12_300, 456, 17), "ttft ", "then the time to first token")
	require.Equal(t, "↑12.3K · ↓456", MetricsForWidth("m1", 12_300, 456, 17))

	require.Empty(t, MetricsForWidth("m1", 12_300, 456, 3), "nothing is shown rather than a cut-off number")
	require.Empty(t, MetricsForWidth("m1", 12_300, 456, 0), "a zero width has no room")
}

func TestMetricsForWidthWithoutTimings(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	// A replayed message has no timings, but the counts stored on it survive.
	require.Equal(t, "↑12.3K · ↓456", MetricsForWidth("replayed", 12_300, 456, 200))
	require.Empty(t, MetricsForWidth("replayed", 0, 0, 200))
}

func TestLiveStatusEstimatesOutputWhileStreaming(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	time.Sleep(5 * time.Millisecond)
	MarkStreamedOutput(400)

	status := MetricsStatus()
	require.Contains(t, status, "↓100", "400 streamed characters estimate 100 tokens")
	require.Regexp(t, `[0-9.]+ tps`, status)
	require.Contains(t, status, "↑-", "the prompt size is unknown until the request ends")
}

func TestLiveStatusPrefersTheReportedUsage(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	time.Sleep(5 * time.Millisecond)
	MarkStreamedOutput(400)
	MarkStepFinished()
	FinishStep(12_300, 456)

	status := MetricsStatus()
	require.Contains(t, status, "↑12.3K")
	require.Contains(t, status, "↓456")
	require.NotContains(t, status, "↓100", "the estimate gives way to the reported count")
}

// TestNewStepShowsItsOwnTimingsNotThePreviousResponse: a fresh response
// reports its own measurements, not the numbers of the response before it.
// Its prompt has to include what that response produced, though, since the
// next request carries it, so the prompt count is the one part that builds on
// the previous step.
func TestNewStepShowsItsOwnTimingsNotThePreviousResponse(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStepWithPrompt(t, "m1", 12_300, 456, 2*time.Millisecond)

	StartStep("m2")
	status := MetricsStatus()
	require.Contains(t, status, "ttft -", "the new response has no time to first token yet")
	require.Contains(t, status, "- tps", "nor a decode speed")
	require.Contains(t, status, "↑12.8K", "the measured prompt plus the output that followed it")
	require.Contains(t, status, "↓-")
	require.NotContains(t, status, "↓456", "the previous response's output count is not the new one's")
}

// TestLiveStatusEstimatesTheRequestInFlight: the provider sizes a request only
// when it answers, so the request in flight is estimated from the last measured
// prompt plus the output it produced and every input that landed since, which
// is what the new request carries. The reported count replaces the estimate.
func TestLiveStatusEstimatesTheRequestInFlight(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStepWithPrompt(t, "m1", 1_000, 100, 2*time.Millisecond)

	// A tool result and a follow-up user message land after the measured
	// request; both are part of the next request's prompt.
	AddPendingPromptTokens(400)
	AddPendingPromptTokens(100)

	StartStep("m2")
	MarkFirstToken()
	time.Sleep(5 * time.Millisecond)
	MarkStreamedOutput(400)

	status := MetricsStatus()
	require.Contains(t, status, "↑1.6K", "1K measured, plus 100 output and 500 pending input")
	require.Contains(t, status, "↓100", "the output is estimated as it streams")

	// The reported prompt covers everything that was pending, so the
	// estimate is dropped rather than added to it.
	MarkStepFinished()
	FinishStep(12_300, 456)
	require.Contains(t, MetricsStatus(), "↑12.3K", "the reported count replaces the estimate")

	StartStep("m3")
	require.Contains(t, MetricsStatus(), "↑12.8K", "the pending input is not counted twice")
}

// TestPendingPromptTokensAreClearedWithTheSession: the estimate describes the
// inputs appended to one session, so it goes when the session's metrics do.
func TestPendingPromptTokensAreClearedWithTheSession(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStepWithPrompt(t, "m1", 1_000, 100, 2*time.Millisecond)
	AddPendingPromptTokens(500)

	ResetMetrics()

	StartStep("m2")
	require.Contains(t, MetricsStatus(), "↑-", "the session's estimate is gone with its metrics")
}

// TestBufferedStepIsTimedFromTheStartOfTheStep: a slow model can hold its
// output back and deliver it in one burst. Timing the burst alone would report
// thousands of tokens per second, so the reading is taken over the whole step
// and the wait for the output counts.
func TestBufferedStepIsTimedFromTheStartOfTheStep(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	time.Sleep(300 * time.Millisecond)
	MarkFirstToken()
	MarkStepFinished()
	FinishStep(0, 60)

	speed := tpsOf(t, MetricsForWidth("m1", 0, 0, 200))
	require.Less(t, speed, 1000.0, "a burst is not a decode speed")
	require.Greater(t, speed, 50.0, "the rate is measured over the whole step")
}

// TestLiveStatusDoesNotReportABurstAsADecodeSpeed: the same applies while the
// step is still streaming, which is where the absurd rates were showing up.
func TestLiveStatusDoesNotReportABurstAsADecodeSpeed(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	time.Sleep(300 * time.Millisecond)
	MarkFirstToken()
	MarkStreamedOutput(240)

	status := MetricsStatus()
	require.Contains(t, status, "↓60", "240 streamed characters estimate 60 tokens")
	require.Less(t, tpsOf(t, status), 1000.0, "the live rate is not the burst rate")
}
