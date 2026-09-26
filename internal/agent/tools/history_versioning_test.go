package tools

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/history"
	"github.com/stretchr/testify/require"
)

// newHistoryRecordingEdit builds an editContext whose history service is a
// recording mock, so tests can assert exactly which versions were stored.
func newHistoryRecordingEdit(t *testing.T, files *mockHistoryService, dir string) editContext {
	t.Helper()

	return editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       files,
		filetracker: &mockEditFileTracker{lastRead: time.Now().Add(time.Second)},
		workingDir:  dir,
	}
}

func TestCommitFileChangeStoresEachVersionOnceWhenHistoryIsMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))

	files := &mockHistoryService{lookupErr: sql.ErrNoRows}
	edit := newHistoryRecordingEdit(t, files, dir)

	require.NoError(t, commitFileChange(edit, "session", filePath, "alpha\n", "beta\n"))

	// A missing lookup means the path has no history in this session, so the
	// baseline comes from Create alone. The old code reused the zero value
	// returned alongside the error, misfired the "user changed the file"
	// guard, and stored the baseline twice.
	require.Equal(t, []string{"alpha\n"}, files.created)
	require.Equal(t, []string{"beta\n"}, files.versioned)
}

func TestCommitFileChangeSkipsTheBaselineWhenHistoryAlreadyMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))

	files := &mockHistoryService{lookupFile: history.File{Content: "alpha\n"}}
	edit := newHistoryRecordingEdit(t, files, dir)

	require.NoError(t, commitFileChange(edit, "session", filePath, "alpha\n", "beta\n"))

	require.Empty(t, files.created, "history already exists, nothing to create")
	require.Equal(t, []string{"beta\n"}, files.versioned)
}

func TestCommitFileChangeRecordsTheOnDiskStateWhenItDivergedFromHistory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))

	// The last version this session recorded differs from what was on disk
	// before the edit, so the on-disk state needs its own entry to keep the
	// stored history linear.
	files := &mockHistoryService{lookupFile: history.File{Content: "stale\n"}}
	edit := newHistoryRecordingEdit(t, files, dir)

	require.NoError(t, commitFileChange(edit, "session", filePath, "alpha\n", "beta\n"))

	require.Empty(t, files.created)
	require.Equal(t, []string{"alpha\n", "beta\n"}, files.versioned)
}
