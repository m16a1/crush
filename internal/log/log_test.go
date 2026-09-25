package log

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupInitializesAndInstallsHandlers(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "crush.log")

	Setup(logFile, true)

	require.True(t, Initialized())

	// The default logger now writes to the rotating file, so a record must
	// land on disk.
	require.NotPanics(t, func() { slog.Info("hello from test") })

	// Setup is one-shot; a second call must not replace the handlers.
	Setup(filepath.Join(t.TempDir(), "other.log"), false)
	require.True(t, Initialized())
}

func TestRecoverPanicWritesReportAndRunsCleanup(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cleaned := false
	func() {
		defer RecoverPanic("unit-test", func() { cleaned = true })
		panic("boom")
	}()

	require.True(t, cleaned)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Name(), "crush-panic-unit-test-")

	content, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Contains(t, string(content), "Panic in unit-test: boom")
	require.Contains(t, string(content), "Stack Trace:")
}

func TestRecoverPanicDoesNothingWithoutPanic(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cleaned := false
	func() {
		defer RecoverPanic("no-panic", func() { cleaned = true })
	}()

	require.False(t, cleaned)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestBodyToString(t *testing.T) {
	t.Parallel()

	require.Empty(t, bodyToString(nil))
	require.Equal(t, `{`+"\n"+`  "a": 1`+"\n"+`}`, bodyToString(io.NopCloser(strings.NewReader(`{"a":1}`))))
	require.Equal(t, "not json", bodyToString(io.NopCloser(strings.NewReader("not json"))))
}

func TestDrainBody(t *testing.T) {
	t.Parallel()

	t.Run("nil body", func(t *testing.T) {
		t.Parallel()
		a, b, err := drainBody(nil)
		require.NoError(t, err)
		require.Equal(t, http.NoBody, a)
		require.Equal(t, http.NoBody, b)
	})

	t.Run("http.NoBody", func(t *testing.T) {
		t.Parallel()
		a, b, err := drainBody(http.NoBody)
		require.NoError(t, err)
		require.Equal(t, http.NoBody, a)
		require.Equal(t, http.NoBody, b)
	})

	t.Run("copies content into two readers", func(t *testing.T) {
		t.Parallel()
		a, b, err := drainBody(io.NopCloser(strings.NewReader("payload")))
		require.NoError(t, err)

		first, err := io.ReadAll(a)
		require.NoError(t, err)
		second, err := io.ReadAll(b)
		require.NoError(t, err)
		require.Equal(t, "payload", string(first))
		require.Equal(t, "payload", string(second))
	})
}
