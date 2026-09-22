package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// titleAnswerModel answers title requests with a fixed title and records the
// prompt it was asked to name, so a test can see what the title was based on.
type titleAnswerModel struct {
	mu     sync.Mutex
	prompt string
	calls  int
}

func (m *titleAnswerModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{}, nil
}

func (m *titleAnswerModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.record(call)
	return func(yield func(fantasy.StreamPart) bool) {
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "t"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "t", Delta: "Sidebar metrics"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "t"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *titleAnswerModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return &fantasy.ObjectResponse{}, nil
}

func (m *titleAnswerModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, nil
}

func (m *titleAnswerModel) Provider() string { return "fake" }
func (m *titleAnswerModel) Model() string    { return "fake-model" }

func (m *titleAnswerModel) record(call fantasy.Call) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, msg := range call.Prompt {
		for _, part := range msg.Content {
			if text, ok := fantasy.AsContentType[fantasy.TextPart](part); ok {
				m.prompt += text.Text
			}
		}
	}
}

func (m *titleAnswerModel) lastPrompt() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prompt
}

// titleErrorModel fails every request, which is how a title regeneration
// fails before it can produce any output.
type titleErrorModel struct{}

func (titleErrorModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("title model unavailable")
}

func (titleErrorModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	return nil, errors.New("title model unavailable")
}

func (titleErrorModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("title model unavailable")
}

func (titleErrorModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("title model unavailable")
}

func (titleErrorModel) Provider() string { return "fake" }
func (titleErrorModel) Model() string    { return "fake-model" }

// userTextMessage stores a user prompt on the session.
func userTextMessage(t *testing.T, env fakeEnv, sessionID, text string) {
	t.Helper()
	_, err := env.messages.Create(t.Context(), sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	})
	require.NoError(t, err)
}

// TestRegenerateTitleUsesTheSessionsFirstPrompt: a regenerated title describes
// what started the session, so it is built from the first prompt and not from
// whatever the conversation has drifted into since.
func TestRegenerateTitleUsesTheSessionsFirstPrompt(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &titleAnswerModel{}
	agent := testSessionAgent(env, model, model, "test system prompt")

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	userTextMessage(t, env, session.ID, "why is the sidebar showing empty blocks")
	userTextMessage(t, env, session.ID, "and now the footer")

	require.NoError(t, agent.RegenerateTitle(t.Context(), session.ID))

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Sidebar metrics", updated.Title)

	prompt := model.lastPrompt()
	require.Contains(t, prompt, "why is the sidebar showing empty blocks")
	require.NotContains(t, prompt, "and now the footer", "the title describes the first prompt only")
}

// TestRegenerateTitleKeepsTheTitleWhenGenerationFails: the session already has
// a usable name, so a failed regeneration must leave it alone instead of
// resetting it to the default.
func TestRegenerateTitleKeepsTheTitleWhenGenerationFails(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := testSessionAgent(env, titleErrorModel{}, titleErrorModel{}, "test system prompt")

	session, err := env.sessions.Create(t.Context(), "Sidebar blocks")
	require.NoError(t, err)

	userTextMessage(t, env, session.ID, "hide empty sidebar blocks")

	require.Error(t, agent.RegenerateTitle(t.Context(), session.ID))

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Sidebar blocks", updated.Title)
	require.NotEqual(t, DefaultSessionName, updated.Title)
}

// TestRegenerateTitleWithoutAPrompt: a session with no user prompt has nothing
// to name, and says so instead of silently doing nothing.
func TestRegenerateTitleWithoutAPrompt(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &titleAnswerModel{}
	agent := testSessionAgent(env, model, model, "test system prompt")

	session, err := env.sessions.Create(t.Context(), "Empty")
	require.NoError(t, err)

	err = agent.RegenerateTitle(t.Context(), session.ID)
	require.ErrorContains(t, err, "no prompt to base a title on")

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Empty", updated.Title)
}
