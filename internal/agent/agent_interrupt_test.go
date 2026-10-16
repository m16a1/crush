package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interruptibleModel blocks the first Stream call until its context is
// canceled, simulating a long in-flight request, and behaves like a
// plain finishing model on every later call.
type interruptibleModel struct {
	started   chan struct{}
	startOnce sync.Once
	calls     atomic.Int64
}

func (m *interruptibleModel) Provider() string { return "fake" }
func (m *interruptibleModel) Model() string    { return "fake-model" }

func (m *interruptibleModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "done"}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *interruptibleModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if m.calls.Add(1) == 1 {
		m.startOnce.Do(func() { close(m.started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return func(yield func(fantasy.StreamPart) bool) {
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: "done"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *interruptibleModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *interruptibleModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

// TestRun_InterruptBusySessionDeliversQueuedCall covers the interrupt
// path: a call flagged Interrupt that arrives while the session is busy
// enqueues itself, cancels the in-flight run, and is delivered as its
// own turn as soon as the interrupted turn unwinds.
func TestRun_InterruptBusySessionDeliversQueuedCall(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &interruptibleModel{started: make(chan struct{})}
	sa := testSessionAgent(env, model, model, "system").(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	firstDone := make(chan error, 1)
	go func() {
		_, err := sa.Run(t.Context(), SessionAgentCall{
			SessionID: sess.ID,
			Prompt:    "long turn",
		})
		firstDone <- err
	}()
	<-model.started

	result, err := sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "background job finished",
		Interrupt: true,
	})
	require.NoError(t, err)
	// The call was queued behind the busy session, so its result is not
	// returned here; the interrupted turn hands off to it and runs it.
	require.Nil(t, result)

	// The first Run goroutine unwinds through the queue handoff, so
	// waiting on it also waits for the report turn to complete.
	require.NoError(t, <-firstDone)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var userMsgs []message.Message
	var assistants []message.Message
	for _, m := range msgs {
		switch m.Role {
		case message.User:
			userMsgs = append(userMsgs, m)
		case message.Assistant:
			assistants = append(assistants, m)
		}
	}
	require.Len(t, userMsgs, 2, "interrupted turn and report turn each have their user message")
	assert.Equal(t, "long turn", userMsgs[0].Content().String())
	assert.Equal(t, "background job finished", userMsgs[1].Content().String())

	require.Len(t, assistants, 2)
	require.Equal(t, message.FinishReasonCanceled, assistants[0].FinishReason(),
		"the interrupted turn must be persisted as canceled")
	if part := assistants[0].FinishPart(); part != nil {
		assert.Equal(t, interruptReasonJobReport, part.Message,
			"the interrupted turn must carry the job-report interrupt reason")
	}
	assert.Equal(t, message.FinishReasonEndTurn, assistants[1].FinishReason(),
		"the report turn must complete normally")

	// The queue must be empty: the report was delivered, not left behind.
	require.Equal(t, 0, sa.QueuedPrompts(sess.ID))
}

// opaqueCancelModel behaves like interruptibleModel but surfaces the
// cancellation as an error that no longer satisfies
// errors.Is(err, context.Canceled), the way provider clients and
// transport wrappers can mangle the cause.
type opaqueCancelModel struct {
	started   chan struct{}
	startOnce sync.Once
	calls     atomic.Int64
}

func (m *opaqueCancelModel) Provider() string { return "fake" }
func (m *opaqueCancelModel) Model() string    { return "fake-model" }

func (m *opaqueCancelModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "done"}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *opaqueCancelModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if m.calls.Add(1) == 1 {
		m.startOnce.Do(func() { close(m.started) })
		<-ctx.Done()
		return nil, errors.New("stream torn down by shutdown")
	}
	return func(yield func(fantasy.StreamPart) bool) {
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: "done"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *opaqueCancelModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *opaqueCancelModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

// TestRun_InterruptDeliversQueuedCallDespiteOpaqueStreamError covers the
// orphan path: when the interrupted run's stream surfaces the
// cancellation as an error that is not context.Canceled, the run must
// still fall through to the queue handoff. Demoting the interrupt in
// that case made the run return early, leaving the queued report
// stranded in the queue and the busy-to-idle notification unpublished,
// so the UI stayed busy and declined input while the agent was idle.
func TestRun_InterruptDeliversQueuedCallDespiteOpaqueStreamError(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &opaqueCancelModel{started: make(chan struct{})}
	sa := testSessionAgent(env, model, model, "system").(*sessionAgent)

	finished := make(chan notify.Notification, 8)
	broker := pubsub.NewBroker[notify.Notification]()
	subCtx, cancelSub := context.WithCancel(t.Context())
	defer cancelSub()
	go func() {
		for ev := range broker.Subscribe(subCtx) {
			if ev.Payload.Type == notify.TypeAgentFinished {
				finished <- ev.Payload
			}
		}
	}()
	sa.notify = broker

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	firstDone := make(chan error, 1)
	go func() {
		_, err := sa.Run(t.Context(), SessionAgentCall{
			SessionID: sess.ID,
			Prompt:    "long turn",
		})
		firstDone <- err
	}()
	<-model.started

	result, err := sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "background job finished",
		Interrupt: true,
	})
	require.NoError(t, err)
	require.Nil(t, result)

	// The interrupted turn unwinds through the queue handoff, so waiting
	// on it also waits for the report turn to complete.
	require.NoError(t, <-firstDone)

	require.Equal(t, 0, sa.QueuedPrompts(sess.ID),
		"the queued report must be delivered, not orphaned")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var userMsgs []message.Message
	var assistants []message.Message
	for _, m := range msgs {
		switch m.Role {
		case message.User:
			userMsgs = append(userMsgs, m)
		case message.Assistant:
			assistants = append(assistants, m)
		}
	}
	require.Len(t, userMsgs, 2, "interrupted turn and report turn each have their user message")
	assert.Equal(t, "long turn", userMsgs[0].Content().String())
	assert.Equal(t, "background job finished", userMsgs[1].Content().String())

	require.Len(t, assistants, 2)
	assert.Equal(t, message.FinishReasonEndTurn, assistants[1].FinishReason(),
		"the report turn must complete normally")

	// The busy-to-idle edge must be published even though the interrupted
	// turn ended with a non-cancel stream error, and it must be marked
	// internal so UIs suppress the desktop notification for it.
	require.Eventually(t, func() bool { return len(finished) >= 2 },
		5*time.Second, 10*time.Millisecond)
	preempted := <-finished
	report := <-finished
	require.True(t, preempted.Internal,
		"a turn preempted by a job report must be marked internal")
	require.True(t, report.Internal,
		"a job-report turn must be marked internal")
}

// TestRun_InterruptIdleSessionRunsDirectly covers the non-busy path: an
// interrupt-flagged call arriving while the session is idle simply runs
// as its own turn without any cancellation side effects.
func TestRun_InterruptIdleSessionRunsDirectly(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &finishStreamModel{text: "done"}
	sa := testSessionAgent(env, model, model, "system").(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	result, err := sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "background job finished",
		Interrupt: true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "background job finished", msgs[0].Content().String())
	assert.Equal(t, message.FinishReasonEndTurn, msgs[1].FinishReason())
}

// TestDrainQueueForStep_KeepsInterruptCallsQueued verifies that the
// per-step queue drain never folds an interrupt-flagged call into the
// running turn: the call exists to interrupt that turn and must be
// delivered by the handoff after it unwinds.
func TestDrainQueueForStep_KeepsInterruptCallsQueued(t *testing.T) {
	t.Parallel()
	sa, _ := newStreamTestAgent(t)

	const sid = "sid"
	sa.enqueueCall(SessionAgentCall{SessionID: sid, Prompt: "report", Interrupt: true})
	sa.enqueueCall(SessionAgentCall{SessionID: sid, Prompt: "follow-up"})

	fold, canceled := sa.drainQueueForStep(sid)
	require.Empty(t, canceled)
	require.Len(t, fold, 1, "only the plain follow-up may be folded into the step")
	assert.Equal(t, "follow-up", fold[0].Prompt)
	require.Equal(t, 1, sa.QueuedPrompts(sid), "the interrupt-flagged call must stay queued")
}
