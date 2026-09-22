package common

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

var tpsPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?) tok/s`)
var avgPattern = regexp.MustCompile(`avg ([0-9]+(?:\.[0-9]+)?) tok/s`)

// resetTracker clears the package-level tracker between tests so cases do not
// leak timings into each other.
func resetTracker() {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	turnTimer.active = false
	turnTimer.stepMessageID = ""
	turnTimer.firstTokenTime = time.Time{}
	turnTimer.stepFinishedAt = time.Time{}
	turnTimer.totalTokens = 0
	turnTimer.totalWindow = 0
	turnTimer.totalSteps = 0
	turnTimer.steps = nil
	turnTimer.order = nil
}

// tpsOf extracts the throughput from a formatted metrics string.
func tpsOf(t *testing.T, metrics string) float64 {
	t.Helper()
	match := tpsPattern.FindStringSubmatch(metrics)
	require.Len(t, match, 2, "no throughput in %q", metrics)
	value, err := strconv.ParseFloat(match[1], 64)
	require.NoError(t, err)
	return value
}

func TestMetricsStatusReportsElapsedBeforeFirstToken(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	status := MetricsStatus()
	require.Contains(t, status, "0s")
	require.NotContains(t, status, "ttft")
}

func TestMetricsStatusStopsReportingElapsedWhenTurnEnds(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	StopTurn()

	require.Regexp(t, `^ttft [0-9]+ms$`, MetricsStatus())
}

func TestMarkFirstTokenRecordsTimeToFirstToken(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	require.Empty(t, MetricsFor("m1"), "no metrics before the first token")

	MarkFirstToken()
	metrics := MetricsFor("m1")
	require.Regexp(t, `^ttft [0-9]+(ms|[0-9.]+s)$`, metrics)
	require.NotContains(t, metrics, "tok/s", "throughput needs the step to finish")
}

func TestFinishStepDerivesThroughputFromFrozenWindow(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	time.Sleep(2 * time.Millisecond)
	MarkStepFinished()
	FinishStep(100)
	first := tpsOf(t, MetricsFor("m1"))

	// A later, more accurate count must refine the rate without stretching the
	// generation window.
	FinishStep(200)
	require.InDelta(t, first*2, tpsOf(t, MetricsFor("m1")), 1)
}

func TestFinishStepIsIgnoredBeforeTheStepFinishes(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	FinishStep(500)

	require.NotContains(t, MetricsFor("m1"), "tok/s")
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
	FinishStep(500)

	require.NotContains(t, MetricsFor("m1"), "tok/s")
}

func TestFinishStepIgnoresEmptyUsage(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	MarkStepFinished()
	FinishStep(0)

	require.NotContains(t, MetricsFor("m1"), "tok/s")
}

// avgOf extracts the running average from a formatted metrics string.
func avgOf(t *testing.T, metrics string) float64 {
	t.Helper()
	match := avgPattern.FindStringSubmatch(metrics)
	require.Len(t, match, 2, "no average throughput in %q", metrics)
	value, err := strconv.ParseFloat(match[1], 64)
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
	FinishStep(tokens)
}

func TestAverageThroughputIsReportedFromTheFirstStep(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)

	// The average is the session's, so it is available as soon as a step has
	// been measured rather than waiting for a second step.
	require.InDelta(t, tpsOf(t, MetricsFor("m1")), avgOf(t, MetricsFor("m1")), 1)
}

func TestAverageThroughputCoversEveryMeasuredStep(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 4*time.Millisecond)
	measureStep(t, "m2", 100, 4*time.Millisecond)

	// Both steps generated the same number of tokens over the same window, so
	// the average lands on the individual rates.
	stepRate := tpsOf(t, MetricsFor("m2"))
	average := avgOf(t, MetricsFor("m2"))
	require.InDelta(t, stepRate, average, stepRate*0.2)
	require.InDelta(t, tpsOf(t, MetricsFor("m1")), average, average*0.2)
}

func TestAverageThroughputWeightsLongerSteps(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)

	// A much longer, slower step pulls the average down.
	measureStep(t, "m2", 100, 20*time.Millisecond)

	average := avgOf(t, MetricsFor("m2"))
	require.Less(t, average, tpsOf(t, MetricsFor("m1")), "the slower step pulls the average down")
}

func TestAverageThroughputIsRefinedNotDuplicated(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	measureStep(t, "m2", 100, 2*time.Millisecond)
	before := avgOf(t, MetricsFor("m2"))

	// A refined count for a step that already contributed must replace its
	// share of the session totals rather than add to them: the step's window is
	// unchanged, so doubling its tokens scales the contributions to 300/200.
	FinishStep(200)
	after := avgOf(t, MetricsFor("m2"))

	require.Greater(t, after, before)
	require.InDelta(t, before*1.5, after, 1)
}

func TestAverageThroughputCarriesAcrossTurns(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	firstTurnRate := tpsOf(t, MetricsFor("m1"))

	StartTurn()
	measureStep(t, "m2", 100, 20*time.Millisecond)

	average := avgOf(t, MetricsFor("m2"))
	require.Less(t, average, firstTurnRate, "earlier steps stay in the running average")

	// The finished turn keeps the average as it was when it finished.
	require.InDelta(t, firstTurnRate, avgOf(t, MetricsFor("m1")), 1)
}

func TestMetricsAreScopedToTheTimedMessage(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	StartStep("m1")
	MarkFirstToken()
	MarkStepFinished()
	FinishStep(120)

	require.NotEmpty(t, MetricsFor("m1"))
	require.Empty(t, MetricsFor("m2"), "another message never inherits timings")
	require.Empty(t, MetricsFor(""), "an empty message id has no timings")
	require.True(t, TrackedStep("m1"))
	require.False(t, TrackedStep("m2"))

	StartStep("m2")
	require.False(t, TrackedStep("m1"), "starting a step stops tracking the previous one")
	require.True(t, TrackedStep("m2"))
	require.NotEmpty(t, MetricsFor("m1"), "finished steps keep their timings")
}

func TestStartTurnDoesNotTimeUntrackedMessages(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	MarkFirstToken()
	MarkStepFinished()
	FinishStep(120)

	require.Empty(t, MetricsFor("m1"), "tokens are only timed for tracked steps")
}

func TestTrackedStepsAreBounded(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	for i := range maxTrackedSteps + 10 {
		StartStep(strconv.Itoa(i))
		MarkFirstToken()
	}

	require.Empty(t, MetricsFor("0"), "the oldest step is evicted")
	require.NotEmpty(t, MetricsFor(strconv.Itoa(maxTrackedSteps+9)))
}

func TestFormatMetricsOmitsMissingThroughput(t *testing.T) {
	require.Equal(t, "ttft 420ms", formatMetrics(stepMetrics{ttft: 420 * time.Millisecond}))
	require.Equal(t, "ttft 1.20s · 58.3 tok/s", formatMetrics(stepMetrics{ttft: 1200 * time.Millisecond, tps: 58.3, hasTPS: true}))
	require.Equal(t, "ttft 12.5s · 120 tok/s", formatMetrics(stepMetrics{ttft: 12500 * time.Millisecond, tps: 120, hasTPS: true}))
	require.Equal(t, "ttft 1.20s · 58.3 tok/s · avg 54.2 tok/s",
		formatMetrics(stepMetrics{ttft: 1200 * time.Millisecond, tps: 58.3, hasTPS: true, avgTPS: 54.2, hasAvg: true}))
}

func TestMetricsStatusLinesAreEmptyWithoutTimings(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	require.Empty(t, MetricsStatusLines(30))
}

func TestMetricsStatusLinesKeepsOneLineWhenItFits(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 100, 2*time.Millisecond)
	measureStep(t, "m2", 100, 2*time.Millisecond)

	full := MetricsStatus()
	lines := MetricsStatusLines(200)
	require.Len(t, lines, 1)
	require.Equal(t, full, lines[0])
}

func TestMetricsStatusLinesSplitsInsteadOfDroppingOnNarrowColumns(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	// Slow steps keep the throughput numbers short, like real generation does.
	measureStep(t, "m1", 4, 50*time.Millisecond)
	measureStep(t, "m2", 4, 50*time.Millisecond)

	// The sidebar is 32 columns wide, which cannot hold the whole status.
	lines := MetricsStatusLines(28)
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "ttft ", "the turn timing keeps its own line")
	require.Contains(t, lines[1], "tok/s")
	require.Contains(t, lines[1], "avg ", "a realistic rate and average fit the second line")
	for _, line := range lines {
		require.LessOrEqual(t, lipgloss.Width(line), 28, "status lines must fit the column")
	}
}

func TestMetricsStatusLinesShedsTheAverageOnVeryNarrowColumns(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 4, 50*time.Millisecond)
	measureStep(t, "m2", 4, 50*time.Millisecond)

	lines := MetricsStatusLines(13)
	require.Len(t, lines, 2)
	require.NotContains(t, lines[1], "avg ", "the average is dropped before a rate is cut in half")
	for _, line := range lines {
		require.LessOrEqual(t, lipgloss.Width(line), 13)
	}
}

func TestMetricsForWidthDropsTheLeastImportantMeasurements(t *testing.T) {
	resetTracker()
	t.Cleanup(resetTracker)

	StartTurn()
	measureStep(t, "m1", 4, 50*time.Millisecond)
	measureStep(t, "m2", 4, 50*time.Millisecond)

	require.Contains(t, MetricsForWidth("m2", 200), "avg ", "everything fits when there is room")
	require.NotContains(t, MetricsForWidth("m2", 22), "avg ", "the average goes first")
	require.Contains(t, MetricsForWidth("m2", 22), "tok/s")
	require.Equal(t, "ttft 0ms", MetricsForWidth("m2", 10))
	require.Empty(t, MetricsForWidth("m2", 3), "nothing is shown rather than a cut-off number")
	require.Empty(t, MetricsForWidth("other", 200), "untimed messages have no metrics")
}
