package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// titleOrderModel records the order in which the turn's request and the
// title request are served, and tracks how many requests were in flight
// at the same time. A model that serves one request at a time is the
// reason the title must not be asked for while the turn is still
// running.
type titleOrderModel struct {
	mu          sync.Mutex
	events      []string
	inFlight    int
	maxInFlight int
	titleDone   chan struct{}
}

func (m *titleOrderModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{}, nil
}

func (m *titleOrderModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	isTitle := isTitleCall(call)
	return func(yield func(fantasy.StreamPart) bool) {
		m.enter(isTitle)
		defer m.leave(isTitle)

		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "t"})
		text := "hello"
		if isTitle {
			text = "Named the turn"
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "t", Delta: text})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "t"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *titleOrderModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return &fantasy.ObjectResponse{}, nil
}

func (m *titleOrderModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, nil
}

func (m *titleOrderModel) Provider() string { return "fake" }
func (m *titleOrderModel) Model() string    { return "fake-model" }

func (m *titleOrderModel) enter(isTitle bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inFlight++
	if m.inFlight > m.maxInFlight {
		m.maxInFlight = m.inFlight
	}
	if isTitle {
		m.events = append(m.events, "title-start")
		return
	}
	m.events = append(m.events, "turn-start")
}

func (m *titleOrderModel) leave(isTitle bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inFlight--
	if isTitle {
		m.events = append(m.events, "title-end")
		select {
		case m.titleDone <- struct{}{}:
		default:
		}
		return
	}
	m.events = append(m.events, "turn-end")
}

func (m *titleOrderModel) snapshot() ([]string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.events...), m.maxInFlight
}

// isTitleCall reports whether a request is the title request by looking
// for the prompt the title generator builds.
func isTitleCall(call fantasy.Call) bool {
	for _, msg := range call.Prompt {
		for _, part := range msg.Content {
			text, ok := fantasy.AsContentType[fantasy.TextPart](part)
			if ok && strings.Contains(text.Text, "Generate a concise title") {
				return true
			}
		}
	}
	return false
}

// TestTitleRequestWaitsForTheTurnToEnd pins the ordering the title
// request must follow: it is sent once the turn's own request has
// ended, never alongside it. Asking for the title up front made the two
// race on models that serve a single request at a time, so the title
// call failed and the session kept its fallback name.
func TestTitleRequestWaitsForTheTurnToEnd(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &titleOrderModel{titleDone: make(chan struct{}, 1)}
	agent := testSessionAgent(env, model, model, "test system prompt")

	session, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{
		Prompt:    "Hello",
		SessionID: session.ID,
	})
	require.NoError(t, err)

	select {
	case <-model.titleDone:
	case <-time.After(10 * time.Second):
		t.Fatal("title was never requested")
	}

	events, maxInFlight := model.snapshot()
	require.Equal(t, []string{"turn-start", "turn-end", "title-start", "title-end"}, events)
	require.Equal(t, 1, maxInFlight, "the title request must not overlap the turn's request")

	// The generated title is saved after the title stream ends, so wait for
	// the stored value rather than for the stream.
	require.Eventually(t, func() bool {
		updated, err := env.sessions.Get(t.Context(), session.ID)
		return err == nil && updated.Title == "Named the turn"
	}, 5*time.Second, 10*time.Millisecond, "the generated title is stored on the session")
}
