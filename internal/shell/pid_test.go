package shell

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestShellRecordsExternalProcessPID verifies that a command which spawns
// an external process exposes that process's real OS PID, so the UI can
// show a truthful PID instead of an internal counter.
func TestShellRecordsExternalProcessPID(t *testing.T) {
	t.Parallel()

	sh := NewShell(&Options{WorkingDir: t.TempDir()})
	_, _, err := sh.Exec(context.Background(), "/bin/echo hello")
	require.NoError(t, err)
	require.Greater(t, sh.ProcessID(), 0, "external command must report a real PID")
}

// TestShellReportsNoPIDForBuiltinOnly verifies that commands made up
// entirely of interpreter builtins have no OS process, so no PID is
// reported (the UI shows nothing rather than a fake identifier).
func TestShellReportsNoPIDForBuiltinOnly(t *testing.T) {
	t.Parallel()

	sh := NewShell(&Options{WorkingDir: t.TempDir()})
	_, _, err := sh.Exec(context.Background(), "echo hello")
	require.NoError(t, err)
	require.Zero(t, sh.ProcessID(), "builtin-only command must not report a PID")
}
