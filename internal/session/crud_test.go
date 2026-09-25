package session

import (
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T) Service {
	t.Helper()
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	return NewService(db.New(conn), conn)
}

func waitForEvent[T any](t *testing.T, ch <-chan pubsub.Event[T], want pubsub.EventType) pubsub.Event[T] {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %v", want)
		}
	}
}

func TestHashID(t *testing.T) {
	t.Parallel()

	id := "9c8a1b2c-0000-4000-8000-000000000000"
	require.NotEmpty(t, HashID(id))
	require.Equal(t, HashID(id), HashID(id))
	require.NotEqual(t, HashID(id), HashID("other"))
}

func TestHasIncompleteTodos(t *testing.T) {
	t.Parallel()

	require.False(t, HasIncompleteTodos(nil))
	require.False(t, HasIncompleteTodos([]Todo{{Status: TodoStatusCompleted}}))
	require.True(t, HasIncompleteTodos([]Todo{{Status: TodoStatusPending}}))
	require.True(t, HasIncompleteTodos([]Todo{{Status: TodoStatusCompleted}, {Status: TodoStatusInProgress}}))
}

func TestAgentToolSessionIDRoundTrip(t *testing.T) {
	t.Parallel()

	svc := &service{}

	id := svc.CreateAgentToolSessionID("msg-1", "call-1")
	require.Equal(t, "msg-1$$call-1", id)
	require.True(t, svc.IsAgentToolSession(id))

	messageID, toolCallID, ok := svc.ParseAgentToolSessionID(id)
	require.True(t, ok)
	require.Equal(t, "msg-1", messageID)
	require.Equal(t, "call-1", toolCallID)

	_, _, ok = svc.ParseAgentToolSessionID("no-separator")
	require.False(t, ok)
	require.False(t, svc.IsAgentToolSession("a$$b$$c"))
}

func TestMarshalUnmarshalTodos(t *testing.T) {
	t.Parallel()

	data, err := marshalTodos(nil)
	require.NoError(t, err)
	require.Empty(t, data)

	todos := []Todo{{Content: "c", Status: TodoStatusPending, ActiveForm: "af"}}
	data, err = marshalTodos(todos)
	require.NoError(t, err)

	got, err := unmarshalTodos(data)
	require.NoError(t, err)
	require.Equal(t, todos, got)

	empty, err := unmarshalTodos("")
	require.NoError(t, err)
	require.Empty(t, empty)

	_, err = unmarshalTodos("{not json")
	require.Error(t, err)
}

func TestCreateGetListLifecycle(t *testing.T) {
	svc := newTestService(t)

	ch := svc.Subscribe(t.Context())
	created, err := svc.Create(t.Context(), "first")
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "first", created.Title)

	ev := waitForEvent(t, ch, pubsub.CreatedEvent)
	require.Equal(t, created.ID, ev.Payload.ID)

	fetched, err := svc.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, fetched.ID)

	_, err = svc.Get(t.Context(), "does-not-exist")
	require.Error(t, err)

	last, err := svc.GetLast(t.Context())
	require.NoError(t, err)
	require.Equal(t, created.ID, last.ID)

	all, err := svc.List(t.Context())
	require.NoError(t, err)
	require.Len(t, all, 1)
}

func TestCreateTitleAndTaskSessions(t *testing.T) {
	svc := newTestService(t)

	parent, err := svc.Create(t.Context(), "parent")
	require.NoError(t, err)

	title, err := svc.CreateTitleSession(t.Context(), parent.ID)
	require.NoError(t, err)
	require.Equal(t, "title-"+parent.ID, title.ID)
	require.Equal(t, parent.ID, title.ParentSessionID)
	require.Equal(t, "Generate a title", title.Title)

	task, err := svc.CreateTaskSession(t.Context(), "call-1", parent.ID, "task")
	require.NoError(t, err)
	require.Equal(t, "call-1", task.ID)
	require.Equal(t, parent.ID, task.ParentSessionID)
}

func TestRenameAndUpdateTitleAndUsage(t *testing.T) {
	svc := newTestService(t)

	s, err := svc.Create(t.Context(), "original")
	require.NoError(t, err)

	require.NoError(t, svc.Rename(t.Context(), s.ID, "renamed"))
	got, err := svc.Get(t.Context(), s.ID)
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Title)

	require.NoError(t, svc.UpdateTitleAndUsage(t.Context(), s.ID, "titled", 11, 22, 0.5))
	got, err = svc.Get(t.Context(), s.ID)
	require.NoError(t, err)
	require.Equal(t, "titled", got.Title)
	require.Equal(t, int64(11), got.PromptTokens)
	require.Equal(t, int64(22), got.CompletionTokens)
	require.InDelta(t, 0.5, got.Cost, 1e-9)
}

func TestSetChannelPublishesAnUpdate(t *testing.T) {
	svc := newTestService(t)

	s, err := svc.Create(t.Context(), "chan")
	require.NoError(t, err)

	ch := svc.Subscribe(t.Context())
	updated, err := svc.SetChannel(t.Context(), s.ID, "slack")
	require.NoError(t, err)
	require.Equal(t, "slack", updated.Channel)

	ev := waitForEvent(t, ch, pubsub.UpdatedEvent)
	require.Equal(t, "slack", ev.Payload.Channel)

	got, err := svc.Get(t.Context(), s.ID)
	require.NoError(t, err)
	require.Equal(t, "slack", got.Channel)

	cleared, err := svc.SetChannel(t.Context(), s.ID, "")
	require.NoError(t, err)
	require.Empty(t, cleared.Channel)
}

func TestDeleteRemovesSessionAndPublishes(t *testing.T) {
	svc := newTestService(t)

	s, err := svc.Create(t.Context(), "doomed")
	require.NoError(t, err)

	// Mark it as estimated-usage so Delete's cleanup path is exercised.
	fetched, err := svc.Get(t.Context(), s.ID)
	require.NoError(t, err)
	fetched.EstimatedUsage = true
	_, err = svc.Save(t.Context(), fetched)
	require.NoError(t, err)

	ch := svc.Subscribe(t.Context())
	require.NoError(t, svc.Delete(t.Context(), s.ID))
	ev := waitForEvent(t, ch, pubsub.DeletedEvent)
	require.Equal(t, s.ID, ev.Payload.ID)

	_, err = svc.Get(t.Context(), s.ID)
	require.Error(t, err)

	require.Error(t, svc.Delete(t.Context(), s.ID))
}

func TestSavePersistsTodosAndSummary(t *testing.T) {
	svc := newTestService(t)

	s, err := svc.Create(t.Context(), "rich")
	require.NoError(t, err)

	s.Todos = []Todo{{Content: "todo", Status: TodoStatusPending, ActiveForm: "doing"}}
	s.SummaryMessageID = fmt.Sprintf("summary-%s", s.ID)
	saved, err := svc.Save(t.Context(), s)
	require.NoError(t, err)
	require.Equal(t, s.SummaryMessageID, saved.SummaryMessageID)

	fetched, err := svc.Get(t.Context(), s.ID)
	require.NoError(t, err)
	require.Len(t, fetched.Todos, 1)
	require.Equal(t, s.SummaryMessageID, fetched.SummaryMessageID)
}
