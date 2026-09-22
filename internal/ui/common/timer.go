package common

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// statusSeparator joins the parts of a generation status line.
const statusSeparator = " · "

// maxTrackedSteps bounds how many assistant messages keep their generation
// metrics in memory so a long session cannot grow the tracker without limit.
const maxTrackedSteps = 128

// stepMetrics holds the generation timings of a single assistant message.
type stepMetrics struct {
	// ttft is the time from the start of the step to its first token.
	ttft time.Duration
	// tokens is the step's output token count, and window the generation time
	// it was measured over. Together they feed the running average.
	tokens int64
	window time.Duration
	// tps is the step's throughput in output tokens per second.
	tps float64
	// hasTPS reports whether a throughput measurement is available.
	hasTPS bool
	// avgTPS is the running average throughput of the session as of this step.
	avgTPS float64
	// hasAvg reports whether an average throughput is available.
	hasAvg bool
}

// turnTimer tracks the elapsed time and the generation timings for the current
// agent turn: the time to the first streamed token and the output tokens per
// second of every step. Each model response is a separate step and gets its own
// assistant message, so metrics are kept per message ID. Every step also feeds
// the session's running average throughput.
var turnTimer struct {
	mu        sync.Mutex
	startTime time.Time
	active    bool

	// stepMessageID is the assistant message being timed right now.
	stepMessageID string
	// stepStartTime is when the current step began.
	stepStartTime time.Time
	// firstTokenTime is when the first token of the current step arrived.
	firstTokenTime time.Time
	// stepFinishedAt is when the current step stopped streaming. It freezes the
	// throughput window so a later, more accurate token count does not stretch
	// the measured duration.
	stepFinishedAt time.Time

	// totalTokens and totalWindow accumulate the measured output tokens and
	// generation time of every step of the session, which give the running
	// average throughput. totalSteps counts the steps that contributed.
	totalTokens int64
	totalWindow time.Duration
	totalSteps  int

	// steps keeps the timings of recent steps by assistant message ID so the
	// footer of a finished turn can keep showing them.
	steps map[string]stepMetrics
	order []string
}

// StartTurn begins tracking elapsed time for a new turn.
func StartTurn() {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	turnTimer.startTime = time.Now()
	turnTimer.active = true
	turnTimer.stepMessageID = ""
	turnTimer.stepStartTime = turnTimer.startTime
	turnTimer.firstTokenTime = time.Time{}
	turnTimer.stepFinishedAt = time.Time{}
}

// StopTurn stops tracking the current turn. Generation metrics stay readable so
// the finished turn can still display them.
func StopTurn() {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	turnTimer.active = false
}

// Elapsed returns the formatted elapsed time for the current turn.
// Returns empty string if no turn is active.
func Elapsed() string {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	if !turnTimer.active {
		return ""
	}
	return formatElapsed(time.Since(turnTimer.startTime))
}

// StartStep begins timing a new generation step for the assistant message with
// the given ID. The time to first token and throughput windows restart for
// every step.
func StartStep(messageID string) {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	turnTimer.stepMessageID = messageID
	turnTimer.stepStartTime = time.Now()
	turnTimer.firstTokenTime = time.Time{}
	turnTimer.stepFinishedAt = time.Time{}
}

// StepMessageID returns the assistant message currently being timed, or an
// empty string when no step is being timed.
func StepMessageID() string {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	return turnTimer.stepMessageID
}

// TrackedStep reports whether messageID is the assistant message whose
// generation is currently being timed.
func TrackedStep(messageID string) bool {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	return messageID != "" && messageID == turnTimer.stepMessageID
}

// MarkFirstToken records the first streamed token of the current step and how
// long it took to arrive. It is a no-op once the step already has a timing.
func MarkFirstToken() {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	if !turnTimer.active || turnTimer.stepMessageID == "" {
		return
	}
	if turnTimer.firstTokenTime.IsZero() {
		turnTimer.firstTokenTime = time.Now()
	}
	if _, ok := turnTimer.steps[turnTimer.stepMessageID]; ok {
		return
	}
	setStepLocked(turnTimer.stepMessageID, stepMetrics{
		ttft: turnTimer.firstTokenTime.Sub(turnTimer.stepStartTime),
	})
}

// MarkStepFinished freezes the current step's generation window. Repeating the
// call is a no-op so a later update cannot stretch the measurement.
func MarkStepFinished() {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	if turnTimer.stepFinishedAt.IsZero() {
		turnTimer.stepFinishedAt = time.Now()
	}
}

// minThroughputWindow is the shortest generation window a throughput reading is
// derived from. A response that arrives in one piece (a cache hit or an
// instant local reply) streams first token and finish within microseconds,
// which would report an absurd rate instead of none at all.
const minThroughputWindow = time.Millisecond

// FinishStep records the output tokens generated by the current step, derives
// its throughput, and folds it into the session's running average. Repeating
// the call is safe: the window was frozen when the step finished, so a more
// accurate token count only refines the rate and replaces the step's earlier
// contribution.
func FinishStep(tokens int64) {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	id := turnTimer.stepMessageID
	if id == "" || tokens <= 0 || turnTimer.firstTokenTime.IsZero() || turnTimer.stepFinishedAt.IsZero() {
		return
	}
	elapsed := turnTimer.stepFinishedAt.Sub(turnTimer.firstTokenTime)
	if elapsed < minThroughputWindow {
		return
	}

	metrics := turnTimer.steps[id]
	if metrics.window > 0 {
		turnTimer.totalTokens += tokens - metrics.tokens
		turnTimer.totalWindow += elapsed - metrics.window
	} else {
		turnTimer.totalTokens += tokens
		turnTimer.totalWindow += elapsed
		turnTimer.totalSteps++
	}

	metrics.tokens = tokens
	metrics.window = elapsed
	metrics.tps = float64(tokens) / elapsed.Seconds()
	metrics.hasTPS = true
	if turnTimer.totalWindow > 0 {
		metrics.avgTPS = float64(turnTimer.totalTokens) / turnTimer.totalWindow.Seconds()
		metrics.hasAvg = true
	}
	setStepLocked(id, metrics)
}

// MetricsFor returns the generation metrics of the assistant message with the
// given ID, formatted for display. It returns an empty string when the message
// was never timed, so replayed history never shows another turn's numbers.
func MetricsFor(messageID string) string {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	metrics, ok := turnTimer.steps[messageID]
	if messageID == "" || !ok {
		return ""
	}
	return formatMetrics(metrics)
}

// MetricsForWidth returns the metrics of the assistant message with the given
// ID trimmed to a column of the given width, dropping the least important
// measurements first (the turn average, then the throughput). It returns an
// empty string when nothing fits, so callers never have to cut a rate in half.
func MetricsForWidth(messageID string, width int) string {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	metrics, ok := turnTimer.steps[messageID]
	if messageID == "" || !ok || width <= 0 {
		return ""
	}
	parts := metricsParts(metrics)
	for len(parts) > 0 && width > 0 && visibleWidth(parts) > width {
		parts = parts[:len(parts)-1]
	}
	if width > 0 && visibleWidth(parts) > width {
		return ""
	}
	return strings.Join(parts, statusSeparator)
}

// MetricsStatus returns the live generation suffix for the working indicator:
// the elapsed turn time plus the time to first token and throughput of the
// current step once they are known.
func MetricsStatus() string {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	status := statusLocked()
	return strings.Join(append(status.timing, status.throughput...), statusSeparator)
}

// MetricsStatusLines lays the live generation status out for a column of the
// given width: one line when everything fits, otherwise the turn timing and the
// throughput split over two lines so no measurement is dropped. A non-positive
// width means no limit.
func MetricsStatusLines(width int) []string {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	status := statusLocked()
	if len(status.timing) == 0 && len(status.throughput) == 0 {
		return nil
	}
	joined := append(append([]string{}, status.timing...), status.throughput...)
	if width <= 0 || visibleWidth(joined) <= width {
		return []string{strings.Join(joined, statusSeparator)}
	}

	// The turn timing needs its own line before any measurement is dropped,
	// and the throughput line only sheds its tail (starting with the average)
	// when even that does not fit.
	timing := status.timing
	throughput := status.throughput
	for len(throughput) > 0 && visibleWidth(throughput) > width {
		throughput = throughput[:len(throughput)-1]
	}

	var lines []string
	if len(timing) > 0 {
		lines = append(lines, truncateLine(strings.Join(timing, statusSeparator), width))
	}
	if len(throughput) > 0 {
		lines = append(lines, strings.Join(throughput, statusSeparator))
	}
	return lines
}

// statusParts splits a generation status into the parts that read as the turn
// timing (elapsed time, time to first token) and those that read as throughput
// (step rate, turn average).
type statusParts struct {
	timing     []string
	throughput []string
}

// statusLocked returns the parts of the live generation status of the tracked
// step. Callers must hold turnTimer.mu.
func statusLocked() statusParts {
	var status statusParts
	if turnTimer.active {
		status.timing = append(status.timing, formatElapsed(time.Since(turnTimer.startTime)))
	}
	metrics, ok := turnTimer.steps[turnTimer.stepMessageID]
	if !ok {
		return status
	}
	status.timing = append(status.timing, "ttft "+formatTTFT(metrics.ttft))
	if metrics.hasTPS {
		status.throughput = append(status.throughput, formatTPS(metrics.tps))
	}
	if metrics.hasAvg {
		status.throughput = append(status.throughput, "avg "+formatTPS(metrics.avgTPS))
	}
	return status
}

// visibleWidth returns the printed width of a status line's parts, including
// the separators between them.
func visibleWidth(parts []string) int {
	return lipgloss.Width(strings.Join(parts, statusSeparator))
}

// truncateLine shortens an unprintably long status line, which only happens
// when an unusually slow time to first token leaves no room at all.
func truncateLine(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	return ansi.Truncate(line, width, "…")
}

// setStepLocked stores the metrics of a step and evicts the oldest entries once
// the tracker is full. Callers must hold turnTimer.mu.
func setStepLocked(messageID string, metrics stepMetrics) {
	if turnTimer.steps == nil {
		turnTimer.steps = make(map[string]stepMetrics)
	}
	if _, ok := turnTimer.steps[messageID]; !ok {
		turnTimer.order = append(turnTimer.order, messageID)
		for len(turnTimer.order) > maxTrackedSteps {
			oldest := turnTimer.order[0]
			turnTimer.order = turnTimer.order[1:]
			delete(turnTimer.steps, oldest)
		}
	}
	turnTimer.steps[messageID] = metrics
}

// formatMetrics renders a step's timings as
// "ttft 0.42s · 58.3 tok/s · avg 54.2 tok/s", omitting the throughput until it
// is known and the turn average until more than one step was measured.
func formatMetrics(metrics stepMetrics) string {
	return strings.Join(metricsParts(metrics), statusSeparator)
}

// metricsParts returns the display parts of a step's timings: the time to first
// token, its throughput once measured, and the turn's average once a second
// step has been measured.
func metricsParts(metrics stepMetrics) []string {
	parts := []string{"ttft " + formatTTFT(metrics.ttft)}
	if metrics.hasTPS {
		parts = append(parts, formatTPS(metrics.tps))
	}
	if metrics.hasAvg {
		parts = append(parts, "avg "+formatTPS(metrics.avgTPS))
	}
	return parts
}

// formatTTFT renders a time to first token with millisecond precision for fast
// responses and a single decimal for slow ones.
func formatTTFT(ttft time.Duration) string {
	switch {
	case ttft < time.Second:
		return fmt.Sprintf("%dms", ttft.Milliseconds())
	case ttft < 10*time.Second:
		return fmt.Sprintf("%.2fs", ttft.Seconds())
	default:
		return fmt.Sprintf("%.1fs", ttft.Seconds())
	}
}

// formatTPS renders a throughput in output tokens per second.
func formatTPS(tps float64) string {
	if tps >= 100 {
		return fmt.Sprintf("%.0f tok/s", tps)
	}
	return fmt.Sprintf("%.1f tok/s", tps)
}

// formatElapsed renders a duration the way the working indicator shows it:
// seconds below a minute, minutes and seconds below an hour, then hours.
func formatElapsed(elapsed time.Duration) string {
	totalSeconds := int(elapsed.Seconds())
	minutes := int(elapsed.Minutes())
	hours := int(elapsed.Hours())

	switch {
	case hours >= 1:
		return fmt.Sprintf("%dh %dm", hours, minutes%60)
	case minutes >= 1:
		return fmt.Sprintf("%dm %ds", minutes, totalSeconds%60)
	default:
		return fmt.Sprintf("%ds", totalSeconds)
	}
}
