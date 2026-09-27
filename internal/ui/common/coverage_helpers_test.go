package common

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestFormatReasoningEffort(t *testing.T) {
	t.Parallel()

	require.Equal(t, "X-High", FormatReasoningEffort("xhigh"))
	require.Equal(t, "High", FormatReasoningEffort("high"))
	require.Equal(t, "Low", FormatReasoningEffort("low"))
	require.Equal(t, "", FormatReasoningEffort(""))
}

func TestFormatCredits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{12345, "12,345"},
		{999999, "999,999"},
		{1000000, "1,000,000"},
		{1234567, "1,234,567"},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, FormatCredits(tt.in), "input %d", tt.in)
	}
}

func TestFormatCompactCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1K"},
		{1500, "1.5K"},
		{2000000, "2M"},
		{1234567, "1.2M"},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, formatCompactCount(tt.in), "input %d", tt.in)
	}
}

func TestPrettyPath(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	path := filepath.Join(home.Dir(), "projects", "crush", "main.go")

	got := ansi.Strip(PrettyPath(&sty, path, 200))
	require.Equal(t, home.Short(path), strings.TrimRight(got, " "))
}

func TestEstimateInputTokens(t *testing.T) {
	t.Parallel()

	require.EqualValues(t, 0, EstimateInputTokens(message.Message{}))

	msg := message.Message{Parts: []message.ContentPart{
		message.TextContent{Text: "abcd"},                                     // 4 chars -> 1 token
		message.ToolResult{Name: "ab", Content: "cd"},                         // 4 chars -> 1 token
		message.ShellCommand{Command: "ab", Output: "cd"},                     // 4 chars -> 1 token
		message.ImageURLContent{URL: "abcd"},                                  // 4 chars -> 1 token
		message.BinaryContent{MIMEType: "ab", Path: "cd", Data: []byte("ef")}, // 6 chars -> 2 tokens
	}}

	require.EqualValues(t, 6, EstimateInputTokens(msg))
}

func TestStripBashDisplayPrefix(t *testing.T) {
	t.Parallel()

	require.Equal(t, "ls -la", StripBashDisplayPrefix("cd /work && ls -la", "/work"))
	require.Equal(t, "cd /other && ls", StripBashDisplayPrefix("cd /other && ls", "/work"))
}

func TestDiffFormatter(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	require.NotNil(t, DiffFormatter(&sty))
}

func TestScrollbar(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("content fits viewport", func(t *testing.T) {
		t.Parallel()

		require.Empty(t, Scrollbar(&sty, 10, 5, 10, 0))
	})

	t.Run("zero height", func(t *testing.T) {
		t.Parallel()

		require.Empty(t, Scrollbar(&sty, 0, 100, 10, 0))
	})

	t.Run("renders track of requested height", func(t *testing.T) {
		t.Parallel()

		out := Scrollbar(&sty, 10, 100, 10, 0)
		require.NotEmpty(t, out)
		require.Len(t, strings.Split(out, "\n"), 10)
	})

	t.Run("thumb moves with offset", func(t *testing.T) {
		t.Parallel()

		top := ansi.Strip(Scrollbar(&sty, 10, 100, 10, 0))
		bottom := ansi.Strip(Scrollbar(&sty, 10, 100, 10, 90))
		require.NotEqual(t, top, bottom)
	})
}

func TestStatus(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	out := ansi.Strip(Status(&sty, StatusOpts{
		Icon:        "*",
		Title:       "Title",
		Description: "desc",
	}, 80))
	require.Contains(t, out, "*")
	require.Contains(t, out, "Title")
	require.Contains(t, out, "desc")

	// No icon, no description.
	out = ansi.Strip(Status(&sty, StatusOpts{Title: "Only"}, 80))
	require.Equal(t, "Only", out)
}

func TestSection(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	out := ansi.Strip(Section(&sty, "Title", 40))
	require.True(t, strings.HasPrefix(out, "Title "))

	out = ansi.Strip(Section(&sty, "Title", 40, "info"))
	require.Contains(t, out, "info")

	// Width smaller than the text produces no separator line.
	out = ansi.Strip(Section(&sty, "LongTitle", 3))
	require.Equal(t, "LongTitle", out)
}

func TestDialogTitle(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	// Title wider than the available width is truncated.
	out := ansi.Strip(DialogTitle(&sty, "a very long dialog title", 5, nil, nil))
	require.Equal(t, "a ve…", out)

	// Normal title gets a gradient line appended.
	out = ansi.Strip(DialogTitle(&sty, "Title", 20, nil, nil))
	require.True(t, strings.HasPrefix(out, "Title "))
}

func TestCenterRect(t *testing.T) {
	t.Parallel()

	area := image.Rect(0, 0, 100, 50)
	got := CenterRect(area, 20, 10)
	require.Equal(t, image.Rect(40, 20, 60, 30), got)
}

func TestBottomLeftRect(t *testing.T) {
	t.Parallel()

	area := image.Rect(0, 0, 100, 50)
	got := BottomLeftRect(area, 20, 10)
	require.Equal(t, image.Rect(0, 40, 20, 50), got)
}

func TestIsFileTooBig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))

	tooBig, err := IsFileTooBig(path, 10)
	require.NoError(t, err)
	require.False(t, tooBig)

	tooBig, err = IsFileTooBig(path, 2)
	require.NoError(t, err)
	require.True(t, tooBig)

	_, err = IsFileTooBig(filepath.Join(t.TempDir(), "missing"), 10)
	require.Error(t, err)
}

func TestModelInfo(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("provider on first line", func(t *testing.T) {
		t.Parallel()

		out := ansi.Strip(ModelInfo(&sty, "Model", "Provider", "", nil, 200, nil))
		require.Contains(t, out, "Model")
		require.Contains(t, out, "via Provider")
	})

	t.Run("provider on fallback line", func(t *testing.T) {
		t.Parallel()

		out := ansi.Strip(ModelInfo(&sty, "Model", "Provider", "", nil, 15, nil))
		require.Contains(t, out, "via Provider")
	})

	t.Run("with context info and reasoning", func(t *testing.T) {
		t.Parallel()

		out := ansi.Strip(ModelInfo(&sty, "Model", "", "high", &ModelContextInfo{
			ContextUsed:  100,
			ModelContext: 1000,
			Cost:         0.5,
		}, 200, nil))
		require.Contains(t, out, "high")
		require.Contains(t, out, "(100)")
		require.Contains(t, out, "$0.50")
	})

	t.Run("hyper credits", func(t *testing.T) {
		t.Parallel()

		credits := 1500
		out := ansi.Strip(ModelInfo(&sty, "Model", hyper.DisplayName, "", nil, 200, &credits))
		require.Contains(t, out, "1,500 Hypercredits")
	})
}
