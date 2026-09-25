package prompt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

func newTestShell(t *testing.T, dir string) *shell.Shell {
	t.Helper()
	return shell.NewShell(&shell.Options{WorkingDir: dir})
}

func testStore(t *testing.T, workingDir string) *config.ConfigStore {
	t.Helper()
	t.Setenv("CRUSH_GLOBAL_CONFIG", t.TempDir())
	t.Setenv("CRUSH_GLOBAL_DATA", t.TempDir())
	store, err := config.Init(workingDir, "", false)
	require.NoError(t, err)
	return store
}

func TestNewPromptAppliesOptions(t *testing.T) {

	fixed := time.Date(2024, 3, 7, 0, 0, 0, 0, time.UTC)
	p, err := NewPrompt(
		"test",
		"{{.WorkingDir}}|{{.Platform}}|{{.Date}}",
		WithTimeFunc(func() time.Time { return fixed }),
		WithPlatform("plan9"),
		WithWorkingDir("/work"),
	)
	require.NoError(t, err)
	require.Equal(t, "test", p.Name())

	store := testStore(t, t.TempDir())
	out, err := p.Build(t.Context(), "prov", "model", store)
	require.NoError(t, err)
	require.Equal(t, "/work|plan9|3/7/2024", out)
}

func TestBuildPropagatesTemplateErrors(t *testing.T) {

	store := testStore(t, t.TempDir())

	_, err := NewPrompt("bad-parse", "{{.Nope", WithWorkingDir(t.TempDir()))
	require.NoError(t, err)

	p, err := NewPrompt("bad-parse", "{{.Nope")
	require.NoError(t, err)
	_, err = p.Build(t.Context(), "prov", "model", store)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing template")

	p2, err := NewPrompt("bad-exec", "{{.Missing.Field}}")
	require.NoError(t, err)
	_, err = p2.Build(t.Context(), "prov", "model", store)
	require.Error(t, err)
	require.Contains(t, err.Error(), "executing template")
}

func TestProcessFile(t *testing.T) {

	dir := t.TempDir()
	path := filepath.Join(dir, "ctx.md")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))

	got := processFile(path)
	require.NotNil(t, got)
	require.Equal(t, path, got.Path)
	require.Equal(t, "hello", got.Content)

	require.Nil(t, processFile(filepath.Join(dir, "missing.md")))
}

func TestProcessContextPath(t *testing.T) {

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("a"), 0o644))
	sub := filepath.Join(dir, "nested")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "b.md"), []byte("b"), 0o644))

	t.Run("directory is walked", func(t *testing.T) {
		store := testStore(t, dir)
		got := processContextPath("nested", store)
		require.Len(t, got, 1)
		require.Equal(t, "b", got[0].Content)
	})

	t.Run("file is read directly", func(t *testing.T) {
		store := testStore(t, dir)
		got := processContextPath("a.md", store)
		require.Len(t, got, 1)
		require.Equal(t, "a", got[0].Content)
	})

	t.Run("missing path yields nothing", func(t *testing.T) {
		store := testStore(t, dir)
		require.Empty(t, processContextPath("nope.md", store))
	})
}

func TestExpandPathResolvesEnvVars(t *testing.T) {

	dir := t.TempDir()
	store := testStore(t, dir)
	t.Setenv("PROMPT_TEST_DIR", dir)

	require.Equal(t, dir, expandPath("$PROMPT_TEST_DIR", store))
	require.Equal(t, filepath.Join(dir, "x"), expandPath("$PROMPT_TEST_DIR/x", store))

	// A non-$ path is left alone.
	require.Equal(t, "plain.md", expandPath("plain.md", store))
}

func TestLoadContextFilesDeduplicatesCaseInsensitively(t *testing.T) {

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ctx.md"), []byte("x"), 0o644))
	t.Setenv("PROMPT_DEDUP_DIR", dir)
	store := testStore(t, dir)

	files := loadContextFiles([]string{
		"$PROMPT_DEDUP_DIR/ctx.md",
		"$PROMPT_DEDUP_DIR/CTX.MD",
		"$PROMPT_DEDUP_DIR/missing.md",
	}, store)

	require.Len(t, files, 2)
	require.Len(t, files[strings.ToLower(dir)+"/ctx.md"], 1)
	require.Empty(t, files[strings.ToLower(dir)+"/missing.md"])
}

func TestIsGitRepo(t *testing.T) {

	require.False(t, isGitRepo(t.TempDir()))

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	require.True(t, isGitRepo(dir))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.email=test@example.com", "-c", "user.name=Test"}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestBuildIncludesGitStatusInARepo(t *testing.T) {

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644))
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-q", "-m", "initial")

	store := testStore(t, dir)
	p, err := NewPrompt("git", "{{.IsGitRepo}}|{{.GitStatus}}")
	require.NoError(t, err)

	out, err := p.Build(t.Context(), "prov", "model", store)
	require.NoError(t, err)
	require.Contains(t, out, "true|")
	require.Contains(t, out, "Current branch:")
	require.Contains(t, out, "Status: clean")
	require.Contains(t, out, "Recent commits:")
}

func TestGetGitHelpersFailSoftOutsideARepo(t *testing.T) {

	sh := newTestShell(t, t.TempDir())

	branch, err := getGitBranch(t.Context(), sh)
	require.NoError(t, err)
	require.Empty(t, branch)

	commits, err := getGitRecentCommits(t.Context(), sh)
	require.NoError(t, err)
	require.Empty(t, commits)
}

func TestPromptDataCollectsContextFilesAndSkills(t *testing.T) {

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "local.md"), []byte("local"), 0o644))
	globalDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "global.md"), []byte("global"), 0o644))
	t.Setenv("PROMPT_GLOBAL_DIR", globalDir)

	store := testStore(t, dir)
	cfg := store.Config()
	cfg.Options.ContextPaths = []string{"local.md"}
	cfg.Options.GlobalContextPaths = []string{"$PROMPT_GLOBAL_DIR/global.md"}

	p, err := NewPrompt("data", "x", WithWorkingDir(dir))
	require.NoError(t, err)

	data, err := p.promptData(t.Context(), "prov", "model", store)
	require.NoError(t, err)
	require.Equal(t, "prov", data.Provider)
	require.Equal(t, "model", data.Model)
	require.Equal(t, filepath.ToSlash(dir), data.WorkingDir)
	require.False(t, data.IsGitRepo)
	require.Len(t, data.ContextFiles, 1)
	require.Equal(t, "local", data.ContextFiles[0].Content)
	require.Len(t, data.GlobalContextFiles, 1)
	require.Equal(t, "global", data.GlobalContextFiles[0].Content)
}
