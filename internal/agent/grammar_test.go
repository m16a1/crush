package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grammarModel answers every Stream call with a fixed verdict payload,
// standing in for the small model the grammar side request targets.
type grammarModel struct {
	verdict string
}

func (m *grammarModel) Provider() string { return "fake" }
func (m *grammarModel) Model() string    { return "fake-model" }

func (m *grammarModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: m.verdict}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *grammarModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	return func(yield func(fantasy.StreamPart) bool) {
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: m.verdict})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *grammarModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *grammarModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

// grammarTestAgent mirrors testSessionAgent but with a config store, so
// the grammar police mode can be exercised.
func grammarTestAgent(env fakeEnv, large, small fantasy.LanguageModel, cfg *config.ConfigStore) *sessionAgent {
	largeModel := Model{
		Model: large,
		CatwalkCfg: catwalk.Model{
			ContextWindow:    200000,
			DefaultMaxTokens: 10000,
		},
	}
	smallModel := Model{
		Model: small,
		CatwalkCfg: catwalk.Model{
			ContextWindow:    200000,
			DefaultMaxTokens: 10000,
		},
	}
	agent := NewSessionAgent(SessionAgentOptions{
		LargeModel:   largeModel,
		SmallModel:   smallModel,
		SystemPrompt: "system",
		IsYolo:       true,
		Sessions:     env.sessions,
		Messages:     env.messages,
		Cfg:          cfg,
	}).(*sessionAgent)
	return agent
}

// TestProofreadUserMessageAttachesVerdict covers the side request: it
// carries only the message's text, parses the JSON verdict, and appends
// the verdict part to the message.
func TestProofreadUserMessageAttachesVerdict(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	large := &finishStreamModel{text: "done"}
	small := &grammarModel{verdict: `{"correct": false, "corrected": "hello world", "explanation": "misspelled words"}`}
	sa := grammarTestAgent(env, large, small, config.NewTestStore(&config.Config{Options: &config.Options{}}))

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	msg, err := env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "helo werld"}},
	})
	require.NoError(t, err)

	sa.ProofreadUserMessage(t.Context(), sess.ID, msg.ID)

	fresh, err := env.messages.Get(t.Context(), msg.ID)
	require.NoError(t, err)
	verdict := fresh.GrammarVerdict()
	require.NotNil(t, verdict, "the verdict must be attached to the message")
	assert.False(t, verdict.Correct)
	assert.Equal(t, "hello world", verdict.Corrected)
	assert.Equal(t, "misspelled words", verdict.Explanation)
}

// TestProofreadUserMessageCorrectMarksOK covers the ✅ path: a verdict
// reporting correct prose is still attached so the badge can render.
func TestProofreadUserMessageCorrectMarksOK(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	large := &finishStreamModel{text: "done"}
	small := &grammarModel{verdict: `{"correct": true}`}
	sa := grammarTestAgent(env, large, small, config.NewTestStore(&config.Config{Options: &config.Options{}}))

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	msg, err := env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hello world"}},
	})
	require.NoError(t, err)

	sa.ProofreadUserMessage(t.Context(), sess.ID, msg.ID)

	fresh, err := env.messages.Get(t.Context(), msg.ID)
	require.NoError(t, err)
	verdict := fresh.GrammarVerdict()
	require.NotNil(t, verdict)
	assert.True(t, verdict.Correct)
}

// TestProofreadUserMessageToleratesGarbage covers the failure path: an
// unparseable verdict attaches nothing and the call stays silent.
func TestProofreadUserMessageToleratesGarbage(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	large := &finishStreamModel{text: "done"}
	small := &grammarModel{verdict: "the model rambled instead of answering"}
	sa := grammarTestAgent(env, large, small, config.NewTestStore(&config.Config{Options: &config.Options{}}))

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	msg, err := env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "helo werld"}},
	})
	require.NoError(t, err)

	sa.ProofreadUserMessage(t.Context(), sess.ID, msg.ID)

	fresh, err := env.messages.Get(t.Context(), msg.ID)
	require.NoError(t, err)
	assert.Nil(t, fresh.GrammarVerdict(), "no verdict may be attached on a failed check")
}

// TestRun_FiresGrammarSideRequest covers the submit wiring: with the mode
// on, the turn and the side request race and the verdict lands on the
// user message whenever the provider gets to it.
func TestRun_FiresGrammarSideRequest(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	large := &finishStreamModel{text: "done"}
	small := &grammarModel{verdict: `{"correct": false, "corrected": "hello world", "explanation": "misspelled words"}`}
	sa := grammarTestAgent(env, large, small, config.NewTestStore(&config.Config{Options: &config.Options{GrammarPolice: true}}))

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "helo werld",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		msgs, err := env.messages.List(t.Context(), sess.ID)
		if err != nil {
			return false
		}
		for _, m := range msgs {
			if m.Role == message.User && m.Content().Text == "helo werld" {
				return m.GrammarVerdict() != nil
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "the side request must attach a verdict to the user message")
}

// TestRun_GrammarDisabledSkipsSideRequest pins the off state: with the
// mode disabled no verdict is ever attached.
func TestRun_GrammarDisabledSkipsSideRequest(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	large := &finishStreamModel{text: "done"}
	small := &grammarModel{verdict: `{"correct": false, "corrected": "hello world", "explanation": "x"}`}
	sa := grammarTestAgent(env, large, small, config.NewTestStore(&config.Config{Options: &config.Options{}}))

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "helo werld",
	})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	for _, m := range msgs {
		if m.Role == message.User {
			assert.Nil(t, m.GrammarVerdict(), "grammar police must stay silent when disabled")
		}
	}
}

// TestGrammarWanted pins the gating table for the side request.
func TestGrammarWanted(t *testing.T) {
	t.Parallel()

	on := config.NewTestStore(&config.Config{Options: &config.Options{GrammarPolice: true}})
	off := config.NewTestStore(&config.Config{Options: &config.Options{}})

	tests := []struct {
		name string
		cfg  *config.ConfigStore
		call SessionAgentCall
		want bool
	}{
		{"enabled interactive turn", on, SessionAgentCall{SessionID: "s", Prompt: "hi"}, true},
		{"disabled", off, SessionAgentCall{SessionID: "s", Prompt: "hi"}, false},
		{"nil config", nil, SessionAgentCall{SessionID: "s", Prompt: "hi"}, false},
		{"non-interactive", on, SessionAgentCall{SessionID: "s", Prompt: "hi", NonInteractive: true}, false},
		{"retry", on, SessionAgentCall{SessionID: "s", Retry: true}, false},
		{"channel", on, SessionAgentCall{SessionID: "s", Prompt: "hi", Channel: "slack"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, grammarWanted(tt.cfg, tt.call))
		})
	}
}
