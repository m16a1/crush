package prompt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func skipWithoutGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func TestGetGitStatusSummaryDoesNotReportCleanWhenGitFails(t *testing.T) {

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	sh := newTestShell(t, dir)
	status, err := getGitStatusSummary(t.Context(), sh)
	require.NoError(t, err)
	require.Empty(t, status, "a git that cannot inspect the tree must not be reported as clean")
}

func TestGetGitStatusSummaryDoesNotReportCleanForABrokenWorktreePointer(t *testing.T) {

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /does/not/exist\n"), 0o644))

	sh := newTestShell(t, dir)
	status, err := getGitStatusSummary(t.Context(), sh)
	require.NoError(t, err)
	require.Empty(t, status, "a broken worktree pointer must not be reported as clean")
}

func TestBuildDoesNotClaimACleanTreeWhenGitCannotRun(t *testing.T) {

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	store := testStore(t, dir)
	p, err := NewPrompt("git", "{{.IsGitRepo}}|{{.GitStatus}}")
	require.NoError(t, err)

	out, err := p.Build(t.Context(), "prov", "model", store)
	require.NoError(t, err)
	require.Equal(t, "true|", out, "a tree git cannot inspect must not reach the prompt as clean")
}

func TestGetGitStatusSummaryReportsUncommittedChanges(t *testing.T) {

	skipWithoutGit(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x"), 0o644))

	sh := newTestShell(t, dir)
	status, err := getGitStatusSummary(t.Context(), sh)
	require.NoError(t, err)
	require.Contains(t, status, "?? untracked.txt")
}

func TestGetGitStatusSummaryTruncatesLongOutput(t *testing.T) {

	skipWithoutGit(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	for i := range gitStatusMaxLines + 5 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), []byte("x"), 0o644))
	}

	sh := newTestShell(t, dir)
	status, err := getGitStatusSummary(t.Context(), sh)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(status, "Status:\n"), "status=%q", status)

	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(status, "Status:")), "\n")
	require.Len(t, lines, gitStatusMaxLines)
}
