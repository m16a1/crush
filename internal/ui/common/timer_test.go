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

// sidebarPattern matches the average status line the sidebar renders.
var sidebarPattern = regexp.MustCompile(`^avg: ttft (\S+) · ([0-9.]+) tps$`)

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

// measureStep times a step over the given window, which must be long enough to
// count as real generation.
func measureStep(t *testing.T, messageID string, tokens int64, window time.Duration) {
	t.Helper()
	StartStep(messageID)
	MarkFirstToken()
	time.Sleep(window)
	MarkStepFinished()
	FinishStep(0, tokens)
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

func TestAverageThroughputIsTheMeanOfStepRates(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 4, 20*time.Millisecond)
	measureStep(t, "m2", 4, 2*time.Millisecond)

	slow := tpsOf(t, MetricsForWidth("m1", 0, 0, 200))
	fast := tpsOf(t, MetricsForWidth("m2", 0, 0, 200))
	average := averageTPSOf(t)

	// Every step carries the same weight, however long it took. Weighting by
	// generation time instead would drag the average towards the slow step.
	require.InDelta(t, (slow+fast)/2, average, 1)
}

func TestAverageTimeToFirstTokenCoversEveryMeasuredStep(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	first := sidebarStatus(t)

	measureStep(t, "m2", 100, 2*time.Millisecond)
	require.NotEqual(t, first, sidebarStatus(t), "the second step joins the average")
	require.Regexp(t, `^avg: ttft [0-9.]+(ms|s) · `, sidebarStatus(t))
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
	// share of the average rather than add to it, so the mean still covers
	// exactly two steps: the untouched one and the refined one.
	FinishStep(0, 200)
	refined := tpsOf(t, MetricsForWidth("m2", 0, 0, 200))
	after := averageTPSOf(t)

	require.Greater(t, after, before)
	require.InDelta(t, (first+refined)/2, after, 2)

	// Counting the refined step twice would leave the mean diluted by its
	// earlier, slower rate instead.
	require.Greater(t, after, (first+refined/2+refined)/3+2)
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

	require.Equal(t, []string{"avg: ttft - · - tps"}, MetricsStatusLines(30))
}

func TestMetricsStatusLinesKeepsOneLineWhenItFits(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	measureStep(t, "m2", 100, 2*time.Millisecond)

	lines := MetricsStatusLines(200)
	require.Len(t, lines, 1)
	require.Regexp(t, `^avg: ttft [0-9.]+(ms|s) · [0-9.]+ tps$`, lines[0])
}

func TestMetricsStatusLinesWrapInsteadOfDropping(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	// Slow steps keep the average rate short, like real generation does.
	measureStep(t, "m1", 4, 50*time.Millisecond)
	measureStep(t, "m2", 4, 50*time.Millisecond)

	lines := MetricsStatusLines(13)
	require.Len(t, lines, 2, "the averages wrap instead of being dropped")
	require.Contains(t, lines[0], "ttft ")
	require.Contains(t, lines[1], "tps")
	for _, line := range lines {
		require.LessOrEqual(t, lipgloss.Width(line), 13)
	}
}

func TestResetMetricsStartsTheAveragesOver(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	require.NotContains(t, sidebarStatus(t), "avg: ttft -")

	ResetMetrics()

	require.Equal(t, "avg: ttft - · - tps", sidebarStatus(t), "the averages are gone")
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

func TestLiveStatusCarriesTheLastStepIntoTheNextOne(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	FinishStep(12_300, 456)

	// The next step has produced nothing yet, so the indicator keeps showing
	// the numbers of the step before it rather than a row of dashes.
	StartStep("m2")
	status := MetricsStatus()
	require.Contains(t, status, "↑12.3K")
	require.Contains(t, status, "↓456")
	require.Regexp(t, `ttft [0-9]+ms`, status)
}
