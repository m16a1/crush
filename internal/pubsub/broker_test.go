package pubsub

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func recv(t *testing.T, ch <-chan Event[string]) Event[string] {
	t.Helper()
	select {
	case ev, ok := <-ch:
		require.True(t, ok, "subscriber channel closed unexpectedly")
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return Event[string]{}
	}
}

func TestPublishDeliversToEverySubscriber(t *testing.T) {
	t.Parallel()

	b := NewBroker[string]()
	sub1 := b.Subscribe(t.Context())
	sub2 := b.Subscribe(t.Context())

	require.Equal(t, 2, b.GetSubscriberCount())

	b.Publish("updated", "hello")

	require.Equal(t, Event[string]{Type: "updated", Payload: "hello"}, recv(t, sub1))
	require.Equal(t, Event[string]{Type: "updated", Payload: "hello"}, recv(t, sub2))
	require.Zero(t, b.DropCount())
}

func TestPublishDropsWhenSubscriberChannelIsFull(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[string](1)
	sub := b.Subscribe(t.Context())

	b.Publish("updated", "first")
	b.Publish("updated", "second")

	require.Equal(t, uint64(1), b.DropCount(), "the second publish must be counted as a drop")
	require.Equal(t, "first", recv(t, sub).Payload, "the buffered event must survive the drop")
}

func TestPublishNeverBlocksOnASlowSubscriber(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[string](1)
	b.Subscribe(t.Context())

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			b.Publish("updated", "token")
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscriber channel")
	}

	require.Equal(t, uint64(999), b.DropCount())
}

func TestSubscribeRemovesSubscriberOnContextCancel(t *testing.T) {
	t.Parallel()

	b := NewBroker[string]()
	ctx, cancel := context.WithCancel(t.Context())
	sub := b.Subscribe(ctx)
	require.Equal(t, 1, b.GetSubscriberCount())

	cancel()

	require.Eventually(t, func() bool { return b.GetSubscriberCount() == 0 }, 2*time.Second, 5*time.Millisecond)
	_, ok := <-sub
	require.False(t, ok, "cancelling the context must close the subscriber channel")
}

func TestSubscribeAfterShutdownReturnsClosedChannel(t *testing.T) {
	t.Parallel()

	b := NewBroker[string]()
	b.Shutdown()

	sub := b.Subscribe(t.Context())
	_, ok := <-sub
	require.False(t, ok)
	require.Zero(t, b.GetSubscriberCount())
}

func TestShutdownClosesSubscriberChannels(t *testing.T) {
	t.Parallel()

	b := NewBroker[string]()
	sub := b.Subscribe(t.Context())

	b.Shutdown()

	_, ok := <-sub
	require.False(t, ok, "shutdown must close live subscriber channels")
	require.Zero(t, b.GetSubscriberCount())
}

func TestShutdownIsIdempotentAndSilencesPublish(t *testing.T) {
	t.Parallel()

	b := NewBroker[string]()
	b.Subscribe(t.Context())

	require.NotPanics(t, func() {
		b.Shutdown()
		b.Shutdown()
		b.Publish("updated", "after shutdown")
		b.PublishMustDeliver(t.Context(), "updated", "after shutdown")
	})
	require.Zero(t, b.GetSubscriberCount())
}

func TestPublishMustDeliverDeliversIntoAvailableSpace(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[string](4)
	sub := b.Subscribe(t.Context())

	b.PublishMustDeliver(t.Context(), CreatedEvent, "must arrive")

	require.Equal(t, Event[string]{Type: CreatedEvent, Payload: "must arrive"}, recv(t, sub))
	require.Zero(t, b.MustDeliverDropCount())
}

func TestPublishMustDeliverTimesOutAndCountsDrop(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[string](1)
	sub := b.Subscribe(t.Context())

	b.Publish("updated", "fills the buffer")
	b.SetMustDeliverTimeout(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.PublishMustDeliver(t.Context(), "finished", "terminal")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PublishMustDeliver blocked past its timeout")
	}

	require.Equal(t, uint64(1), b.MustDeliverDropCount())
	require.Equal(t, "fills the buffer", recv(t, sub).Payload, "the dropped terminal event must not evict the buffered one")
}

func TestPublishMustDeliverWaitsForSpaceWithinTimeout(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[string](1)
	sub := b.Subscribe(t.Context())

	b.Publish("updated", "occupies the buffer")
	b.SetMustDeliverTimeout(2 * time.Second)

	go func() {
		time.Sleep(20 * time.Millisecond)
		<-sub
	}()

	b.PublishMustDeliver(t.Context(), "finished", "terminal")

	require.Zero(t, b.MustDeliverDropCount())
	require.Equal(t, "terminal", recv(t, sub).Payload)
}

func TestPublishMustDeliverStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[string](1)
	b.Subscribe(t.Context())

	b.Publish("updated", "occupies the buffer")
	b.SetMustDeliverTimeout(30 * time.Second)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.PublishMustDeliver(ctx, "finished", "terminal")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PublishMustDeliver ignored a cancelled context")
	}

	require.Zero(t, b.MustDeliverDropCount(), "a cancelled publish is not a timeout drop")
}

func TestSetMustDeliverTimeoutResetsNonPositiveValues(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[string](1)
	b.SetMustDeliverTimeout(0)

	require.Equal(t, defaultMustDeliverTimeout, b.mustDeliverTimeout)

	b.SetMustDeliverTimeout(-time.Second)
	require.Equal(t, defaultMustDeliverTimeout, b.mustDeliverTimeout)

	b.SetMustDeliverTimeout(3 * time.Second)
	require.Equal(t, 3*time.Second, b.mustDeliverTimeout)
}

func TestDropCountsAreCumulative(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[string](1)
	b.Subscribe(t.Context())

	b.Publish("updated", "1")
	b.Publish("updated", "2")
	b.Publish("updated", "3")

	b.SetMustDeliverTimeout(time.Millisecond)
	b.PublishMustDeliver(t.Context(), "finished", "x")

	require.Equal(t, uint64(2), b.DropCount())
	require.Equal(t, uint64(1), b.MustDeliverDropCount())
}
