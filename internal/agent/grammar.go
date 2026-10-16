package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
)

// grammarTimeout bounds a grammar side request. The request shares the
// provider with the main turn and may queue behind it, so the timeout is a
// backstop against wedged requests, not a latency guarantee: a verdict that
// misses it simply never appears.
const grammarTimeout = 30 * time.Second

// grammarMaxPromptChars caps the proofread input. A user prompt can carry
// large pasted content; proofreading does not need it and the side request
// must stay small.
const grammarMaxPromptChars = 4000

// grammarWanted reports whether the grammar police side request should
// fire for a call: only for interactive, non-retry turns with prose, when
// the mode is on. Channels and non-interactive runs have no UI to badge.
func grammarWanted(cfg *config.ConfigStore, call SessionAgentCall) bool {
	if cfg == nil || call.NonInteractive || call.Retry || call.Channel != "" {
		return false
	}
	return cfg.Config().Options.GrammarPolice
}

// ProofreadUserMessage runs the grammar police side request for a user
// message and attaches the verdict to it. It is fired concurrently with the
// turn and is fire-and-forget: the request fulfills whenever the provider
// gets to it, failures are logged quietly, and the verdict is appended to
// the message's parts the moment it arrives. The part is annotation only —
// ToAIMessage never sends it back to the model.
func (a *sessionAgent) ProofreadUserMessage(ctx context.Context, sessionID, messageID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grammarTimeout)
	defer cancel()

	msg, err := a.messages.Get(ctx, messageID)
	if err != nil {
		slog.Debug("Failed to load message for grammar check", "error", err, "message_id", messageID)
		return
	}
	if msg.GrammarVerdict() != nil {
		return
	}
	text := strings.TrimSpace(msg.Content().Text)
	if text == "" {
		return
	}

	verdict, err := a.grammarVerdict(ctx, sessionID, text)
	if err != nil {
		slog.Debug("Grammar check failed", "error", err, "message_id", messageID)
		return
	}

	// Re-read so a message that changed while the request was in flight
	// (for example a bang-mode shell command filling in its output) is
	// not clobbered by a stale copy.
	fresh, err := a.messages.Get(ctx, messageID)
	if err != nil {
		slog.Debug("Failed to reload message for grammar verdict", "error", err, "message_id", messageID)
		return
	}
	if fresh.GrammarVerdict() != nil {
		return
	}
	fresh.Parts = append(fresh.Parts, verdict)
	if err := a.messages.Update(ctx, fresh); err != nil {
		slog.Debug("Failed to save grammar verdict", "error", err, "message_id", messageID)
	}
}

// grammarVerdict sends the text to the small model as a single request with
// no conversation history and parses the JSON verdict.
func (a *sessionAgent) grammarVerdict(ctx context.Context, sessionID, text string) (message.GrammarVerdict, error) {
	var verdict message.GrammarVerdict
	if runes := []rune(text); len(runes) > grammarMaxPromptChars {
		text = string(runes[:grammarMaxPromptChars])
	}

	model := a.smallModel.Get()
	grammarAgent := fantasy.NewAgent(
		model.Model,
		fantasy.WithSystemPrompt(string(grammarPrompt)+"\n /no_think"),
		fantasy.WithMaxOutputTokens(titleOutputBudget(model)),
		fantasy.WithUserAgent(userAgent),
	)
	resp, err := grammarAgent.Stream(ctx, fantasy.AgentStreamCall{
		Prompt:  text,
		Headers: sessionHeaders(sessionID),
	})
	if err != nil {
		return verdict, fmt.Errorf("grammar request failed: %w", err)
	}

	out := strings.TrimSpace(resp.Response.Content.Text())
	out = thinkTagRegex.ReplaceAllString(out, "")
	out = orphanThinkTagRegex.ReplaceAllString(out, "")
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end <= start {
		return verdict, errors.New("grammar response is not a JSON object")
	}
	if err := json.Unmarshal([]byte(out[start:end+1]), &verdict); err != nil {
		return verdict, fmt.Errorf("failed to parse grammar verdict: %w", err)
	}
	if !verdict.Correct && verdict.Corrected == "" {
		return verdict, errors.New("grammar verdict reports errors without a correction")
	}
	return verdict, nil
}
