package agent

import (
	"context"
	"sync"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// titleReplyModel answers title requests with a fixed text and finish reason.
// It records the output budget each request was given, which is what a model
// configured without a maximum of its own would otherwise be asked for.
type titleReplyModel struct {
	mu      sync.Mutex
	text    string
	reason  fantasy.FinishReason
	budgets []int64
	calls   int
}

func (m *titleReplyModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{}, nil
}

func (m *titleReplyModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	m.calls++
	if call.MaxOutputTokens != nil {
		m.budgets = append(m.budgets, *call.MaxOutputTokens)
	}
	text, reason := m.text, m.reason
	m.mu.Unlock()
	return func(yield func(fantasy.StreamPart) bool) {
		if text != "" {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "t"})
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "t", Delta: text})
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "t"})
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: reason})
	}, nil
}

func (m *titleReplyModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return &fantasy.ObjectResponse{}, nil
}

func (m *titleReplyModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, nil
}

func (m *titleReplyModel) Provider() string { return "fake" }
func (m *titleReplyModel) Model() string    { return "fake-model" }

func (m *titleReplyModel) firstBudget() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.budgets) == 0 {
		return -1
	}
	return m.budgets[0]
}

// reasoningTitleAgent builds an agent whose models reason and report no
// maximum of their own, which is how a user-defined model is configured.
func reasoningTitleAgent(env fakeEnv, model fantasy.LanguageModel) SessionAgent {
	cfg := catwalk.Model{ContextWindow: 200000, CanReason: true}
	return NewSessionAgent(SessionAgentOptions{
		LargeModel:   Model{Model: model, CatwalkCfg: cfg},
		SmallModel:   Model{Model: model, CatwalkCfg: cfg},
		SystemPrompt: "test system prompt",
		IsYolo:       true,
		Sessions:     env.sessions,
		Messages:     env.messages,
	})
}

// TestGenerateTitleGivesAModelWithoutAMaximumARealBudget: a reasoning model
// configured without a maximum reports zero, which used to be sent as a
// zero-token request. The provider then stopped immediately, every attempt
// "hit the token limit", and the session kept its fallback name.
func TestGenerateTitleGivesAModelWithoutAMaximumARealBudget(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &titleReplyModel{text: "Sidebar metrics", reason: fantasy.FinishReasonStop}
	agent := reasoningTitleAgent(env, model)

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	agent.GenerateTitle(t.Context(), session.ID, "why is the sidebar showing empty blocks")

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Sidebar metrics", updated.Title)
	require.Greater(t, model.firstBudget(), int64(0), "the request must not ask for zero tokens")
}

// TestGenerateTitleUsesATitleThatStoppedAtTheTokenLimit: the model may stop at
// the output limit while still having produced a usable title, which names the
// session better than rejecting it and falling back to the prompt.
func TestGenerateTitleUsesATitleThatStoppedAtTheTokenLimit(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &titleReplyModel{text: "Sidebar metrics", reason: fantasy.FinishReasonLength}
	agent := testSessionAgent(env, model, model, "test system prompt")

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	agent.GenerateTitle(t.Context(), session.ID, "why is the sidebar showing empty blocks")

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Sidebar metrics", updated.Title)
}

// TestGenerateTitleTriesTheNextModelWhenOneReturnsNothing: a model that
// answers with no text is skipped instead of ending the attempt.
func TestGenerateTitleTriesTheNextModelWhenOneReturnsNothing(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	silent := &titleReplyModel{reason: fantasy.FinishReasonStop}
	talkative := &titleReplyModel{text: "Sidebar metrics", reason: fantasy.FinishReasonStop}
	// The small model is asked first, then the large one.
	agent := testSessionAgent(env, talkative, silent, "test system prompt")

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	agent.GenerateTitle(t.Context(), session.ID, "why is the sidebar showing empty blocks")

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Sidebar metrics", updated.Title)
	require.Greater(t, silent.firstBudget(), int64(0), "the silent model was tried")
}
