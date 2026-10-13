package agent

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// TestRun_JobReportTurnsPublishInternalNotifications verifies that
// turn-finished notifications are marked Internal when they come from
// background-job plumbing: the turn preempted by a job report, and the
// report turn itself. A plain user-driven turn must not be marked.
// UIs rely on the flag to suppress desktop notifications and sounds for
// internal plumbing while keeping the busy-to-idle edge.
func TestRun_JobReportTurnsPublishInternalNotifications(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &interruptibleModel{started: make(chan struct{})}
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

	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "background job finished",
		Interrupt: true,
	})
	require.NoError(t, err)
	// Waiting on the interrupted turn also waits for the report turn,
	// so both notifications are published by the time this returns.
	require.NoError(t, <-firstDone)

	require.Eventually(t, func() bool { return len(finished) >= 2 },
		5*time.Second, 10*time.Millisecond)
	preempted := <-finished
	report := <-finished
	require.True(t, preempted.Internal,
		"a turn preempted by a job report must be marked internal")
	require.True(t, report.Internal,
		"a job-report turn must be marked internal")

	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "plain turn",
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(finished) >= 1 },
		5*time.Second, 10*time.Millisecond)
	plain := <-finished
	require.False(t, plain.Internal,
		"a user-driven turn must not be marked internal")
}
