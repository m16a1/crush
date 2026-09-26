package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRenameUnknownSessionReturnsErrSessionNotFound(t *testing.T) {

	svc := newTestService(t)

	err := svc.Rename(t.Context(), "00000000-0000-4000-8000-000000000000", "renamed")
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestUpdateTitleAndUsageUnknownSessionReturnsErrSessionNotFound(t *testing.T) {

	svc := newTestService(t)

	err := svc.UpdateTitleAndUsage(t.Context(), "00000000-0000-4000-8000-000000000000", "titled", 1, 2, 0.5)
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestMutationsOnADeletedSessionReturnErrSessionNotFound(t *testing.T) {

	svc := newTestService(t)

	s, err := svc.Create(t.Context(), "live")
	require.NoError(t, err)
	require.NoError(t, svc.Delete(t.Context(), s.ID))

	// The session existed when the caller resolved it but is gone now; both
	// mutations must report that rather than silently succeeding.
	require.ErrorIs(t, svc.Rename(t.Context(), s.ID, "renamed"), ErrSessionNotFound)
	require.ErrorIs(t, svc.UpdateTitleAndUsage(t.Context(), s.ID, "titled", 1, 2, 0.5), ErrSessionNotFound)
}

func TestUnresolvedMutationsPublishNothing(t *testing.T) {

	svc := newTestService(t)
	sub := svc.Subscribe(t.Context())

	require.ErrorIs(t, svc.Rename(t.Context(), "00000000-0000-4000-8000-000000000000", "x"), ErrSessionNotFound)
	require.ErrorIs(t, svc.UpdateTitleAndUsage(t.Context(), "00000000-0000-4000-8000-000000000000", "x", 1, 2, 0.5), ErrSessionNotFound)

	select {
	case ev := <-sub:
		t.Fatalf("a failed mutation published an event: %v %+v", ev.Type, ev.Payload)
	case <-time.After(200 * time.Millisecond):
	}
}
