package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// usageLanguageModel streams a short response that reports token usage the way
// a real provider does at the end of a request.
type usageLanguageModel struct {
	// promptTokens and cacheReadTokens make up the input side of the usage.
	promptTokens     int64
	cacheReadTokens  int64
	completionTokens int64
}

func (m *usageLanguageModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{}, nil
}

func (m *usageLanguageModel) Stream(_ context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
	return func(yield func(fantasy.StreamPart) bool) {
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "t"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "t", Delta: "hello"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "t"})
		yield(fantasy.StreamPart{
			Type:         fantasy.StreamPartTypeFinish,
			FinishReason: fantasy.FinishReasonStop,
			Usage: fantasy.Usage{
				InputTokens:     m.promptTokens,
				CacheReadTokens: m.cacheReadTokens,
				OutputTokens:    m.completionTokens,
			},
		})
	}, nil
}

func (m *usageLanguageModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return &fantasy.ObjectResponse{}, nil
}

func (m *usageLanguageModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, nil
}

func (m *usageLanguageModel) Provider() string { return "fake" }
func (m *usageLanguageModel) Model() string    { return "fake-model" }

// TestStepUsageIsWrittenToTheMessage checks the token counts a provider reports
// for a step end up on the assistant message, where the per-message footer can
// still read them after the process-local timings are gone.
func TestStepUsageIsWrittenToTheMessage(t *testing.T) {
	env := testEnv(t)
	model := &usageLanguageModel{promptTokens: 100, cacheReadTokens: 23, completionTokens: 45}
	agent := testSessionAgent(env, model, model, "test system prompt")

	session, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{
		Prompt:    "Hello",
		SessionID: session.ID,
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), session.ID)
	require.NoError(t, err)

	var finish *message.Finish
	for i := range msgs {
		if msgs[i].Role != message.Assistant {
			continue
		}
		if f := msgs[i].FinishPart(); f != nil {
			finish = f
		}
	}
	require.NotNil(t, finish, "the assistant message carries a finish part")
	require.Equal(t, int64(123), finish.PromptTokens, "the input count includes cache reads")
	require.Equal(t, int64(45), finish.CompletionTokens)
}
