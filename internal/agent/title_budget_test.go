package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// titleReplyModel answers title requests with a fixed text and finish reason.
// It records the output budget each request was given, which is what a model
// configured without a maximum of its own would otherwise be asked for.
type titleReplyModel struct {
	mu        sync.Mutex
	text      string
	reason    fantasy.FinishReason
	minBudget int64
	budgets   []int64
	calls     int
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
	if m.minBudget > 0 && call.MaxOutputTokens != nil && *call.MaxOutputTokens < m.minBudget {
		// The model spends the whole budget reasoning and answers nothing.
		text = ""
	}
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

func (m *titleReplyModel) budgetsSnapshot() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int64(nil), m.budgets...)
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

// TestTitleOutputBudgetLeavesRoomForReasoning: a title budget has to fit the
// reasoning a model does before it writes the title, so it never drops to the
// handful of tokens a title alone would need.
func TestTitleOutputBudgetLeavesRoomForReasoning(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(titleMaxOutputTokens), titleOutputBudget(Model{}))
	require.Equal(t, int64(titleMaxOutputTokens), titleOutputBudget(Model{
		CatwalkCfg: catwalk.Model{CanReason: true},
	}), "a reasoning model with no maximum of its own still gets a real budget")
	require.Equal(t, int64(32768), titleOutputBudget(Model{
		CatwalkCfg: catwalk.Model{CanReason: true, DefaultMaxTokens: 32768},
	}))
	require.Equal(t, int64(titleMaxOutputTokens), titleOutputBudget(Model{
		CatwalkCfg: catwalk.Model{CanReason: true, DefaultMaxTokens: 100},
	}), "a maximum smaller than a title needs is ignored")
	require.Equal(t, int64(titleMaxOutputTokens), titleOutputBudget(Model{
		CatwalkCfg: catwalk.Model{DefaultMaxTokens: 32768},
	}), "a maximum only applies to models that reason")
}

// TestGenerateTitleRetriesWithALargerBudgetWhenAModelReturnsNoText: a model
// whose first budget went entirely to reasoning gets a second, larger budget
// before it is given up on.
func TestGenerateTitleRetriesWithALargerBudgetWhenAModelReturnsNoText(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &titleReplyModel{
		text:      "Sidebar metrics",
		reason:    fantasy.FinishReasonStop,
		minBudget: 2 * titleMaxOutputTokens,
	}
	agent := testSessionAgent(env, model, model, "test system prompt")

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	agent.GenerateTitle(t.Context(), session.ID, "why is the sidebar showing empty blocks")

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Sidebar metrics", updated.Title)
	require.Equal(t, []int64{titleMaxOutputTokens, 4 * titleMaxOutputTokens}, model.budgetsSnapshot())
}

// TestGenerateTitleKeepsARamblingTitleWithinTheLimit: a model that answers
// with more than a title still names the session, cut to the length every
// other title respects.
func TestGenerateTitleKeepsARamblingTitleWithinTheLimit(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &titleReplyModel{
		text:   strings.Repeat("sidebar metrics and footer metrics ", 10),
		reason: fantasy.FinishReasonStop,
	}
	agent := testSessionAgent(env, model, model, "test system prompt")

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	agent.GenerateTitle(t.Context(), session.ID, "why is the sidebar showing empty blocks")

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.Title)
	require.LessOrEqual(t, ansi.StringWidth(updated.Title), maxTitleChars)
}

// TestTitlePromptKeepsItsThinkingCue: the title instruction ends with an
// already-opened, empty thinking block so a model with a thinking channel
// answers instead of reasoning. Losing the tags (an editor stripping them,
// say) sends the words alone and the model reasons through its whole budget.
func TestTitlePromptKeepsItsThinkingCue(t *testing.T) {
	t.Parallel()

	require.Contains(t, titleInstructionSuffix, "\x3cthink\x3e")
	require.Contains(t, titleInstructionSuffix, "\x3c/think\x3e")

	env := testEnv(t)
	model := &titleAnswerModel{}
	agent := testSessionAgent(env, model, model, "test system prompt")

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	agent.GenerateTitle(t.Context(), session.ID, "why is the sidebar showing empty blocks")

	require.Contains(t, model.lastPrompt(), titleInstructionSuffix)
}
