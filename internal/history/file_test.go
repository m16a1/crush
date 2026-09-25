package history

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

type testEnv struct {
	ctx context.Context
	q   *db.Queries
	svc Service
}

func setupHistory(t *testing.T) *testEnv {
	t.Helper()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	q := db.New(conn)
	return &testEnv{ctx: t.Context(), q: q, svc: NewService(q, conn)}
}

func (e *testEnv) createSession(t *testing.T, id string) {
	t.Helper()
	_, err := e.q.CreateSession(e.ctx, db.CreateSessionParams{ID: id, Title: "session"})
	require.NoError(t, err)
}

func awaitEvent(t *testing.T, ch <-chan pubsub.Event[File]) pubsub.Event[File] {
	t.Helper()
	select {
	case ev, ok := <-ch:
		require.True(t, ok)
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a history event")
		return pubsub.Event[File]{}
	}
}

func TestCreateStoresAnInitialVersionAndPublishes(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")
	sub := env.svc.Subscribe(env.ctx)

	file, err := env.svc.Create(env.ctx, "sess-1", "/tmp/a.go", "package a")
	require.NoError(t, err)
	require.Equal(t, "sess-1", file.SessionID)
	require.Equal(t, "/tmp/a.go", file.Path)
	require.Equal(t, "package a", file.Content)
	require.Equal(t, int64(InitialVersion), file.Version)
	require.NotEmpty(t, file.ID)

	ev := awaitEvent(t, sub)
	require.Equal(t, pubsub.CreatedEvent, ev.Type)
	require.Equal(t, file.ID, ev.Payload.ID)
}

func TestCreateVersionIncrementsPerPath(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")

	first, err := env.svc.Create(env.ctx, "sess-1", "/tmp/a.go", "one")
	require.NoError(t, err)
	require.Equal(t, int64(0), first.Version)

	second, err := env.svc.CreateVersion(env.ctx, "sess-1", "/tmp/a.go", "two")
	require.NoError(t, err)
	require.Equal(t, int64(1), second.Version)

	third, err := env.svc.CreateVersion(env.ctx, "sess-1", "/tmp/a.go", "three")
	require.NoError(t, err)
	require.Equal(t, int64(2), third.Version)

	latest, err := env.svc.GetByPathAndSession(env.ctx, "/tmp/a.go", "sess-1")
	require.NoError(t, err)
	require.Equal(t, "three", latest.Content)
	require.Equal(t, int64(2), latest.Version)
}

func TestCreateVersionWithoutAPriorVersionStartsAtZero(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")

	file, err := env.svc.CreateVersion(env.ctx, "sess-1", "/tmp/new.go", "fresh")
	require.NoError(t, err)
	require.Equal(t, int64(InitialVersion), file.Version)
}

func TestVersionsAreTrackedIndependentlyPerPath(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")

	a0, err := env.svc.Create(env.ctx, "sess-1", "/tmp/a.go", "a0")
	require.NoError(t, err)
	b0, err := env.svc.Create(env.ctx, "sess-1", "/tmp/b.go", "b0")
	require.NoError(t, err)
	a1, err := env.svc.CreateVersion(env.ctx, "sess-1", "/tmp/a.go", "a1")
	require.NoError(t, err)

	require.Equal(t, int64(0), a0.Version)
	require.Equal(t, int64(0), b0.Version)
	require.Equal(t, int64(1), a1.Version, "b.go must not advance a.go's version")
}

func TestGetReturnsAStoredFile(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")

	file, err := env.svc.Create(env.ctx, "sess-1", "/tmp/a.go", "content")
	require.NoError(t, err)

	fetched, err := env.svc.Get(env.ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, file, fetched)
}

func TestGetIsScopedToTheSessionForPathLookups(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")

	created, err := env.svc.Create(env.ctx, "sess-1", "/tmp/a.go", "content")
	require.NoError(t, err)

	_, err = env.svc.GetByPathAndSession(env.ctx, "/tmp/a.go", "sess-2")
	require.Error(t, err)

	fetched, err := env.svc.GetByPathAndSession(env.ctx, "/tmp/a.go", "sess-1")
	require.NoError(t, err)
	require.Equal(t, created.ID, fetched.ID)
}

func TestListBySessionReturnsEveryVersion(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")
	env.createSession(t, "sess-2")

	_, err := env.svc.Create(env.ctx, "sess-1", "/tmp/a.go", "one")
	require.NoError(t, err)
	_, err = env.svc.CreateVersion(env.ctx, "sess-1", "/tmp/a.go", "two")
	require.NoError(t, err)
	_, err = env.svc.Create(env.ctx, "sess-2", "/tmp/other.go", "elsewhere")
	require.NoError(t, err)

	files, err := env.svc.ListBySession(env.ctx, "sess-1")
	require.NoError(t, err)
	require.Len(t, files, 2)
	for _, f := range files {
		require.Equal(t, "sess-1", f.SessionID)
		require.Equal(t, "/tmp/a.go", f.Path)
	}
}

func TestListLatestSessionFilesPicksTheNewestVersionPerPath(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")

	_, err := env.svc.Create(env.ctx, "sess-1", "/tmp/a.go", "a0")
	require.NoError(t, err)
	_, err = env.svc.CreateVersion(env.ctx, "sess-1", "/tmp/a.go", "a1")
	require.NoError(t, err)
	_, err = env.svc.Create(env.ctx, "sess-1", "/tmp/b.go", "b0")
	require.NoError(t, err)

	files, err := env.svc.ListLatestSessionFiles(env.ctx, "sess-1")
	require.NoError(t, err)
	require.Len(t, files, 2, "one entry per path, not one per version")

	byPath := map[string]File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	require.Equal(t, "a1", byPath["/tmp/a.go"].Content)
	require.Equal(t, "b0", byPath["/tmp/b.go"].Content)
}

func TestDeleteRemovesTheFileAndPublishes(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")

	file, err := env.svc.Create(env.ctx, "sess-1", "/tmp/a.go", "content")
	require.NoError(t, err)

	sub := env.svc.Subscribe(env.ctx)
	require.NoError(t, env.svc.Delete(env.ctx, file.ID))

	ev := awaitEvent(t, sub)
	require.Equal(t, pubsub.DeletedEvent, ev.Type)
	require.Equal(t, file.ID, ev.Payload.ID)

	_, err = env.svc.Get(env.ctx, file.ID)
	require.Error(t, err)
}

func TestDeleteUnknownFileErrors(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	require.Error(t, env.svc.Delete(env.ctx, "does-not-exist"))
}

func TestDeleteSessionFilesRemovesEveryVersion(t *testing.T) {
	t.Parallel()

	env := setupHistory(t)
	env.createSession(t, "sess-1")
	env.createSession(t, "sess-2")

	_, err := env.svc.Create(env.ctx, "sess-1", "/tmp/a.go", "one")
	require.NoError(t, err)
	_, err = env.svc.CreateVersion(env.ctx, "sess-1", "/tmp/a.go", "two")
	require.NoError(t, err)
	kept, err := env.svc.Create(env.ctx, "sess-2", "/tmp/b.go", "keep me")
	require.NoError(t, err)

	require.NoError(t, env.svc.DeleteSessionFiles(env.ctx, "sess-1"))

	files, err := env.svc.ListBySession(env.ctx, "sess-1")
	require.NoError(t, err)
	require.Empty(t, files)

	other, err := env.svc.ListBySession(env.ctx, "sess-2")
	require.NoError(t, err)
	require.Len(t, other, 1)
	require.Equal(t, kept.ID, other[0].ID)
}
