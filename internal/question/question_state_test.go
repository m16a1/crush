package question

import (
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// serviceWithPending builds a service holding a pending batch directly, so the
// deferred cleanup in Ask cannot run and mask whether Answer/Cancel clear the
// state themselves. This is the same state Ask publishes, minus the goroutine.
func serviceWithPending(t *testing.T) (*questionService, chan []Answer, chan struct{}) {
	t.Helper()

	svc := NewService()
	pending := make(chan []Answer, 1)
	cancelled := make(chan struct{})

	svc.mu.Lock()
	svc.pending = pending
	svc.cancelled = cancelled
	svc.pendingID = "batch-1"
	svc.mu.Unlock()

	return svc, pending, cancelled
}

func drainNotifications(ch <-chan pubsub.Event[Notification], wait time.Duration) int {
	count := 0
	deadline := time.After(wait)
	for {
		select {
		case <-ch:
			count++
		case <-deadline:
			return count
		}
	}
}

func TestAnswerTwiceResolvesTheBatchOnce(t *testing.T) {
	t.Parallel()

	svc, pending, _ := serviceWithPending(t)
	want := []Answer{{QuestionID: "q1", Yes: boolPtr(true)}}
	require.True(t, svc.Answer(want))

	done := make(chan bool, 1)
	go func() { done <- svc.Answer(nil) }()

	select {
	case got := <-done:
		require.False(t, got, "the second Answer must report the batch was already resolved")
	case <-time.After(2 * time.Second):
		t.Fatal("the second Answer blocked; it must return false immediately")
	}

	require.Equal(t, want, <-pending, "the first answers must be the ones delivered")
}

func TestCancelTwiceCancelsTheBatchOnce(t *testing.T) {
	t.Parallel()

	svc, _, cancelled := serviceWithPending(t)
	require.True(t, svc.Cancel())
	require.False(t, svc.Cancel(), "the second Cancel must report the batch was already resolved")

	select {
	case <-cancelled:
	default:
		t.Fatal("the batch was never cancelled")
	}
}

func TestCancelAfterAnswerIsANoOp(t *testing.T) {
	t.Parallel()

	svc, _, _ := serviceWithPending(t)
	notes := svc.SubscribeNotifications(t.Context())

	require.True(t, svc.Answer(nil))
	require.False(t, svc.Cancel(), "Cancel must not claim a batch that was already answered")
	require.Equal(t, 1, drainNotifications(notes, 200*time.Millisecond),
		"a resolved batch must notify exactly once")
}

func TestConcurrentCancelsResolveTheBatchOnce(t *testing.T) {
	t.Parallel()

	svc, _, _ := serviceWithPending(t)
	notes := svc.SubscribeNotifications(t.Context())

	const callers = 8
	results := make(chan bool, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- svc.Cancel()
		}()
	}
	wg.Wait()
	close(results)

	winners := 0
	for resolved := range results {
		if resolved {
			winners++
		}
	}
	require.Equal(t, 1, winners, "exactly one concurrent Cancel may resolve the batch")
	require.Equal(t, 1, drainNotifications(notes, 200*time.Millisecond),
		"a resolved batch must notify exactly once")
}

func TestConcurrentAnswerAndCancelResolveTheBatchOnce(t *testing.T) {
	t.Parallel()

	svc, _, _ := serviceWithPending(t)

	results := make(chan bool, 2)
	go func() { results <- svc.Answer(nil) }()
	go func() { results <- svc.Cancel() }()

	winners := 0
	for range 2 {
		if <-results {
			winners++
		}
	}
	require.Equal(t, 1, winners, "only one of Answer/Cancel may resolve the batch")
}
