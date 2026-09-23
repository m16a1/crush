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

// statusPrefix labels the sidebar's average generation speeds.
const statusPrefix = "avg tps: "

// maxTrackedSteps bounds how many assistant messages keep their generation
// metrics in memory so a long session cannot grow the tracker without limit.
const maxTrackedSteps = 128

// minThroughputWindow is the shortest generation window a throughput reading is
// derived from. A response that arrives in one piece (a cache hit or an
// instant local reply) streams first token and finish within microseconds,
// which would report an absurd rate instead of none at all.
const minThroughputWindow = time.Millisecond

// minDecodeWindow is the shortest decode window a throughput reading is
// measured over. Below it the first token and the last arrive together, which
// means the provider held the output back and delivered it in one burst; the
// burst alone has no duration to speak of, so the reading is taken over the
// whole step instead and the wait for the output counts.
const minDecodeWindow = 250 * time.Millisecond

// estimatedTokenChars is how many streamed characters the live estimate counts
// as one token, matching the fallback the agent uses when a provider reports no
// usage (internal/agent/usage_fallback.go). It only feeds the working indicator
// while a response is still streaming, and the reported counts replace it as
// soon as the request ends.
const estimatedTokenChars = 4

// stepMetrics holds the generation timings of a single assistant message.
type stepMetrics struct {
	// ttft is the time from the start of the step to its first token.
	ttft time.Duration
	// promptTokens and tokens are the input and output token counts the
	// provider reported for the step.
	promptTokens int64
	tokens       int64
	// tps is the step's decode speed in output tokens per second.
	tps float64
	// hasTPS reports whether a decode speed measurement is available.
	hasTPS bool
	// window is the step's decode window, the time its tokens were counted
	// over (see decodeWindow). Kept so the session's aggregate throughput can
	// drop this step's share when a more accurate count replaces it.
	window time.Duration
	// prefillTokens and prefillWindow are the step's share of the session's
	// prefill speed: the prompt tokens it read and the time it took them to
	// reach its first token. hasPrefill reports whether it made one; a step
	// with no measured first token or no reported prompt tokens has none.
	prefillTokens int64
	prefillWindow time.Duration
	hasPrefill    bool
}

// metricsTracker tracks the elapsed time of the current agent turn, the
// generation timings of the step being streamed, and the session's running
// averages. Each model response is a separate step and gets its own assistant
// message, so per-step metrics are kept by message ID.
type metricsTracker struct {
	mu        sync.Mutex
	startTime time.Time
	active    bool

	// sessionID is the session the accumulated metrics belong to, which is
	// what tells a session switch apart from a reload of the same session.
	sessionID string

	// stepMessageID is the assistant message being timed right now, and
	// stepStartTime when it began.
	stepMessageID  string
	stepStartTime  time.Time
	firstTokenTime time.Time
	// stepFinishedAt is when the current step stopped streaming. It freezes
	// the throughput window so a later, more accurate token count does not
	// stretch the measured duration.
	stepFinishedAt time.Time
	// streamedChars is how much model output the current step has produced
	// so far, which feeds the live estimate.
	streamedChars int

	// last* remember the most recent completed step so every part of the
	// live status stays populated between steps. They describe the session
	// and are cleared when it changes.
	lastTTFT         time.Duration
	lastTPS          float64
	lastPromptTokens int64
	lastOutputTokens int64

	// tokenSum and windowSum hold the session's aggregate decode speed:
	// the output tokens every measured step produced over the total time
	// they took to produce them. A short response contributes only the
	// handful of tokens and milliseconds it had, so it cannot outweigh a
	// long one the way a mean of per-step rates would. promptTokenSum and
	// ttftSum hold the aggregate prefill speed the same way: the prompt
	// tokens every measured step read over the total time it took them to
	// reach their first token.
	tokenSum       int64
	windowSum      time.Duration
	promptTokenSum int64
	ttftSum        time.Duration

	// steps keeps the timings of recent steps by assistant message ID so the
	// footer of a finished turn can keep showing them.
	steps map[string]stepMetrics
	order []string
}

// turnTimer is the process-wide tracker. Generation metrics describe the
// session on screen, so switching sessions or models resets them.
var turnTimer metricsTracker

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
	turnTimer.streamedChars = 0
}

// StopTurn stops tracking the current turn. Generation metrics stay readable so
// the finished turn can still display them.
func StopTurn() {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	turnTimer.active = false
}

// SessionChanged tells the tracker which session is on screen. When it differs
// from the session the accumulated metrics belong to, they are dropped: the
// averages describe the session they were measured in. A turn that is already
// running keeps its elapsed time, so loading a session mid-turn does not
// restart its clock.
func SessionChanged(sessionID string) {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	if turnTimer.sessionID == sessionID {
		return
	}
	turnTimer.sessionID = sessionID
	turnTimer.resetLocked()
}

// ResetMetrics drops the session's accumulated averages and per-step timings,
// which is what callers do when the model changes. A turn that is already
// running keeps its elapsed time: the reset must not restart it.
func ResetMetrics() {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	turnTimer.resetLocked()
}

// resetLocked clears everything that describes the session's generation
// history. Callers must hold turnTimer.mu.
func (t *metricsTracker) resetLocked() {
	t.tokenSum = 0
	t.windowSum = 0
	t.promptTokenSum = 0
	t.ttftSum = 0
	t.lastTTFT = 0
	t.lastTPS = 0
	t.lastPromptTokens = 0
	t.lastOutputTokens = 0
	t.steps = nil
	t.order = nil
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
	turnTimer.streamedChars = 0
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
// long it took to arrive. It is a no-op once the step already has a timing. The
// step only joins the session's prefill speed once its prompt size is known, so
// the time is kept on the step rather than added to the aggregate here.
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
	ttft := turnTimer.firstTokenTime.Sub(turnTimer.stepStartTime)
	turnTimer.lastTTFT = ttft
	setStepLocked(turnTimer.stepMessageID, stepMetrics{ttft: ttft})
}

// MarkStreamedOutput records how much model output the step currently being
// timed has produced, in characters. The working indicator turns it into a live
// token estimate, since providers report real usage only when a request ends.
func MarkStreamedOutput(chars int) {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	if !turnTimer.active || turnTimer.stepMessageID == "" {
		return
	}
	turnTimer.streamedChars = chars
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

// FinishStep records the token usage the provider reported for the current
// step, derives its decode speed, and folds the step into the session's
// aggregate prefill and decode speeds. Repeating the call is safe: the window
// was frozen when the step finished, and a step's earlier share is dropped
// before its new one is added, so a more accurate count only refines the rates.
func FinishStep(promptTokens, completionTokens int64) {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	id := turnTimer.stepMessageID
	if id == "" {
		return
	}

	metrics := turnTimer.steps[id]
	previous := metrics
	if promptTokens > 0 {
		metrics.promptTokens = promptTokens
		turnTimer.lastPromptTokens = promptTokens
	}
	if completionTokens > 0 {
		metrics.tokens = completionTokens
		turnTimer.lastOutputTokens = completionTokens
	}

	// Prefill speed: the prompt tokens the step read over the time it took
	// them to reach its first token. Both are needed to make a rate, and a
	// step is only counted once.
	if metrics.promptTokens > 0 && metrics.ttft > 0 {
		if previous.hasPrefill {
			turnTimer.promptTokenSum -= previous.prefillTokens
			turnTimer.ttftSum -= previous.prefillWindow
		}
		turnTimer.promptTokenSum += metrics.promptTokens
		turnTimer.ttftSum += metrics.ttft
		metrics.prefillTokens = metrics.promptTokens
		metrics.prefillWindow = metrics.ttft
		metrics.hasPrefill = true
	}

	if completionTokens > 0 && !turnTimer.firstTokenTime.IsZero() && !turnTimer.stepFinishedAt.IsZero() {
		elapsed := decodeWindow(turnTimer.stepStartTime, turnTimer.firstTokenTime, turnTimer.stepFinishedAt)
		if elapsed >= minThroughputWindow {
			// Drop the step's earlier contribution, if it had one, so
			// the aggregate covers the step once at its latest count.
			if previous.hasTPS {
				turnTimer.tokenSum -= previous.tokens
				turnTimer.windowSum -= previous.window
			}
			turnTimer.tokenSum += completionTokens
			turnTimer.windowSum += elapsed
			metrics.tps = float64(completionTokens) / elapsed.Seconds()
			metrics.hasTPS = true
			metrics.window = elapsed
			turnTimer.lastTPS = metrics.tps
		}
	}

	setStepLocked(id, metrics)
}

// MetricsStatus returns the live generation status of the working indicator:
// the elapsed turn time and, for the response being generated, its time to
// first token, decode speed, and input and output token counts.
func MetricsStatus() string {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	return strings.Join(turnTimer.livePartsLocked(), statusSeparator)
}

// MetricsStatusLines returns the session's average generation status laid out
// for a column of the given width: the prompt tokens every measured step read
// per second (prefill) and the output tokens it wrote per second (decode). Both
// are always reported, as "-" until the session has measured something, and the
// averages wrap rather than being dropped when the column cannot hold them.
func MetricsStatusLines(width int) []string {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	prefill := "↑" + formatRate(turnTimer.averagePrefillLocked())
	decode := "↓" + formatRate(turnTimer.averageDecodeLocked())
	joined := statusPrefix + prefill + statusSeparator + decode
	if width <= 0 || lipgloss.Width(joined) <= width {
		return []string{joined}
	}
	// The label and one rate per line: a measurement is never cut into while
	// it has a line of its own to sit on.
	if head := statusPrefix + prefill; lipgloss.Width(head) <= width {
		return []string{head, truncateLine(decode, width)}
	}
	return []string{
		truncateLine(strings.TrimRight(statusPrefix, " "), width),
		truncateLine(prefill+statusSeparator+decode, width),
	}
}

// MetricsForWidth returns the generation metrics of the assistant message with
// the given ID, trimmed to a column of the given width. The token counts are
// read from the message rather than the tracker, so they stay available after a
// reload, when the process-local timings are gone. The decode speed is dropped
// before the time to first token, so a rate is never cut in half, and an empty
// string means nothing fits.
func MetricsForWidth(messageID string, messagePromptTokens, messageCompletionTokens int64, width int) string {
	if width <= 0 {
		return ""
	}
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()

	var timings []string
	if metrics, ok := turnTimer.steps[messageID]; ok {
		timings = append(timings, "ttft "+formatTTFTValue(metrics.ttft))
		if metrics.hasTPS {
			timings = append(timings, formatTPS(metrics.tps, true))
		}
	}
	tokens := tokenCountParts(messagePromptTokens, messageCompletionTokens)

	// Walk the measurements from the most complete to the least, so a narrow
	// footer loses the decode speed, then the time to first token, and keeps
	// the token counts for as long as they fit.
	candidates := make([][]string, 0, 3)
	candidates = append(candidates, append(append([]string{}, timings...), tokens...))
	if len(timings) == 2 {
		candidates = append(candidates, append([]string{timings[0]}, tokens...))
	}
	candidates = append(candidates, tokens)
	for _, parts := range candidates {
		if len(parts) > 0 && visibleWidth(parts) <= width {
			return strings.Join(parts, statusSeparator)
		}
	}
	return ""
}

// tokenCountParts renders the input and output token counts of a response,
// leaving out either one when the message carries no count for it.
func tokenCountParts(promptTokens, completionTokens int64) []string {
	var parts []string
	if promptTokens > 0 {
		parts = append(parts, "↑"+formatCompactCount(promptTokens))
	}
	if completionTokens > 0 {
		parts = append(parts, "↓"+formatCompactCount(completionTokens))
	}
	return parts
}

// livePartsLocked returns the parts of the live generation status of the
// tracked step. A step that has produced nothing yet reports "-" rather than
// the numbers of the response before it; those belong to that response. With
// no step being tracked at all, the last completed step keeps the indicator
// populated. Callers must hold turnTimer.mu.
func (t *metricsTracker) livePartsLocked() []string {
	var parts []string
	if t.active {
		parts = append(parts, formatElapsed(time.Since(t.startTime)))
	}

	metrics, tracked := t.steps[t.stepMessageID]
	// A step whose message has started but has produced nothing yet reports
	// nothing of its own: the numbers of the previous response belong to that
	// response, not this one.
	stepStarted := t.stepMessageID != ""
	estimatedTokens, estimatedTPS, hasEstimate := t.liveEstimateLocked()

	ttft := t.lastTTFT
	switch {
	case tracked && metrics.ttft > 0:
		ttft = metrics.ttft
	case stepStarted:
		ttft = 0
	}

	tps, hasTPS := t.lastTPS, t.lastTPS > 0
	switch {
	case tracked && metrics.hasTPS:
		tps, hasTPS = metrics.tps, true
	case hasEstimate:
		tps, hasTPS = estimatedTPS, true
	case stepStarted:
		tps, hasTPS = 0, false
	}

	promptTokens := t.lastPromptTokens
	switch {
	case tracked && metrics.promptTokens > 0:
		promptTokens = metrics.promptTokens
	case stepStarted:
		promptTokens = 0
	}

	outputTokens := t.lastOutputTokens
	switch {
	case tracked && metrics.tokens > 0:
		outputTokens = metrics.tokens
	case hasEstimate:
		outputTokens = estimatedTokens
	case stepStarted:
		outputTokens = 0
	}

	parts = append(parts, "ttft "+formatTTFTValue(ttft), formatTPS(tps, hasTPS))
	if promptTokens > 0 {
		parts = append(parts, "↑"+formatCompactCount(promptTokens))
	} else {
		parts = append(parts, "↑-")
	}
	if outputTokens > 0 {
		parts = append(parts, "↓"+formatCompactCount(outputTokens))
	} else {
		parts = append(parts, "↓-")
	}
	return parts
}

// liveEstimateLocked estimates the output tokens and decode speed of the step
// currently streaming from the number of characters it has produced. Callers
// must hold turnTimer.mu.
func (t *metricsTracker) liveEstimateLocked() (tokens int64, tps float64, ok bool) {
	if t.firstTokenTime.IsZero() || t.streamedChars <= 0 {
		return 0, 0, false
	}
	elapsed := decodeWindow(t.stepStartTime, t.firstTokenTime, time.Now())
	if elapsed < minThroughputWindow {
		return 0, 0, false
	}
	tokens = int64((t.streamedChars + estimatedTokenChars - 1) / estimatedTokenChars)
	return tokens, float64(tokens) / elapsed.Seconds(), true
}

// decodeWindow returns the window a step's generation speed is measured over:
// the time from its first token to its last, or the whole step when that is too
// short to be a real decode window. A provider that holds the output back
// delivers it in one burst, so the first token and the last arrive together and
// timing the burst alone reads as a speed no model can reach; the step's own
// clock cannot be beaten.
func decodeWindow(stepStart, firstToken, finished time.Time) time.Duration {
	if firstToken.IsZero() {
		firstToken = stepStart
	}
	window := finished.Sub(firstToken)
	if window >= minDecodeWindow {
		return window
	}
	if total := finished.Sub(stepStart); total > window {
		return total
	}
	return window
}

// averagePrefillLocked returns the session's average prefill speed: the prompt
// tokens every measured step read over the total time it took them to reach
// their first token. Weighting by prompt tokens this way keeps the rate
// independent of the prompt size, which is what makes it comparable between
// steps and sessions. Callers must hold turnTimer.mu.
func (t *metricsTracker) averagePrefillLocked() (float64, bool) {
	if t.ttftSum <= 0 || t.promptTokenSum <= 0 {
		return 0, false
	}
	return float64(t.promptTokenSum) / t.ttftSum.Seconds(), true
}

// averageDecodeLocked returns the session's average decode speed, which is the
// output tokens every measured step produced over the total time they took to
// produce them. Weighting by generation time this way keeps a short response
// from outweighing a long one. Callers must hold turnTimer.mu.
func (t *metricsTracker) averageDecodeLocked() (float64, bool) {
	if t.windowSum <= 0 {
		return 0, false
	}
	return float64(t.tokenSum) / t.windowSum.Seconds(), true
}

// visibleWidth returns the printed width of a status line's parts, including
// the separators between them.
func visibleWidth(parts []string) int {
	return lipgloss.Width(strings.Join(parts, statusSeparator))
}

// truncateLine shortens an unprintably long status line, which only happens
// when the column is narrower than a single measurement.
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

// formatTTFTValue renders a time to first token, or "-" when it was never
// measured.
func formatTTFTValue(ttft time.Duration) string {
	if ttft <= 0 {
		return "-"
	}
	return formatTTFT(ttft)
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

// formatTPS renders a decode speed in output tokens per second, or "-" when no
// measurement is available.
func formatTPS(tps float64, ok bool) string {
	if !ok {
		return "- tps"
	}
	return formatRate(tps, true) + " tps"
}

// formatRate renders a token rate as a bare number: whole numbers at 100 and
// above, one decimal below, and "-" when nothing has been measured. The unit is
// carried by the label the caller prints it under.
func formatRate(rate float64, ok bool) string {
	if !ok {
		return "-"
	}
	if rate >= 100 {
		return fmt.Sprintf("%.0f", rate)
	}
	return fmt.Sprintf("%.1f", rate)
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
