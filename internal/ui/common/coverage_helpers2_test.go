package common

import (
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestCapabilitiesPredicates(t *testing.T) {
	t.Parallel()

	t.Run("true color", func(t *testing.T) {
		t.Parallel()

		require.True(t, Capabilities{Profile: colorprofile.TrueColor}.SupportsTrueColor())
		require.False(t, Capabilities{Profile: colorprofile.ANSI256}.SupportsTrueColor())
	})

	t.Run("graphics", func(t *testing.T) {
		t.Parallel()

		require.True(t, Capabilities{KittyGraphics: true}.SupportsKittyGraphics())
		require.False(t, Capabilities{}.SupportsKittyGraphics())
		require.True(t, Capabilities{SixelGraphics: true}.SupportsSixelGraphics())
		require.False(t, Capabilities{}.SupportsSixelGraphics())
	})

	t.Run("cell size", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, 0, mustFirst(Capabilities{}.CellSize()))
		require.Equal(t, 0, mustSecond(Capabilities{}.CellSize()))

		w, h := Capabilities{Columns: 80, Rows: 24, PixelX: 800, PixelY: 480}.CellSize()
		require.Equal(t, 10, w)
		require.Equal(t, 20, h)
	})
}

func TestModeSupported(t *testing.T) {
	t.Parallel()

	require.True(t, modeSupported(ansi.ModeSet))
	require.True(t, modeSupported(ansi.ModeReset))
	require.False(t, modeSupported(ansi.ModeNotRecognized))
}

func TestShouldQueryCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  []string
		want bool
	}{
		{"empty env", nil, true},
		{"apple terminal", []string{"TERM_PROGRAM=Apple_Terminal"}, false},
		{"ghostty over ssh", []string{"TERM=xterm-256color", "TERM_PROGRAM=ghostty", "SSH_TTY=/dev/ttys001"}, false},
		{"kitty term", []string{"TERM=xterm-kitty"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, shouldQueryCapabilities(uv.Environ(tt.env)))
		})
	}
}

func TestQueryCmd(t *testing.T) {
	t.Parallel()

	require.NotNil(t, QueryCmd(uv.Environ(nil)))
	require.NotNil(t, QueryCmd(uv.Environ([]string{"TMUX=/tmp/tmux", "TERM=xterm-kitty"})))
}

func TestCapabilitiesUpdate(t *testing.T) {
	t.Parallel()

	t.Run("env", func(t *testing.T) {
		t.Parallel()

		var c Capabilities
		c.Update(tea.EnvMsg{"TERM=xterm"})
		require.Equal(t, "xterm", c.Env.Getenv("TERM"))
	})

	t.Run("color profile", func(t *testing.T) {
		t.Parallel()

		var c Capabilities
		c.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
		require.True(t, c.SupportsTrueColor())
	})

	t.Run("window size", func(t *testing.T) {
		t.Parallel()

		var c Capabilities
		c.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		require.Equal(t, 120, c.Columns)
		require.Equal(t, 40, c.Rows)
	})

	t.Run("pixel size", func(t *testing.T) {
		t.Parallel()

		var c Capabilities
		c.Update(uv.PixelSizeEvent{Width: 800, Height: 480})
		require.Equal(t, 800, c.PixelX)
		require.Equal(t, 480, c.PixelY)
	})

	t.Run("kitty graphics", func(t *testing.T) {
		t.Parallel()

		var c Capabilities
		c.Update(uv.KittyGraphicsEvent{})
		require.True(t, c.KittyGraphics)
	})

	t.Run("primary device attributes enables sixel", func(t *testing.T) {
		t.Parallel()

		var c Capabilities
		c.Update(uv.PrimaryDeviceAttributesEvent{4})
		require.True(t, c.SixelGraphics)
	})

	t.Run("terminal version", func(t *testing.T) {
		t.Parallel()

		var c Capabilities
		c.Update(tea.TerminalVersionMsg{Name: "ghostty 1.0"})
		require.Equal(t, "ghostty 1.0", c.TerminalVersion)
	})

	t.Run("focus event mode", func(t *testing.T) {
		t.Parallel()

		var c Capabilities
		c.Update(tea.ModeReportMsg{Mode: ansi.ModeFocusEvent, Value: ansi.ModeSet})
		require.True(t, c.ReportFocusEvents)
	})
}

func TestChromaStyle(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	base := ChromaStyle(&sty, nil)
	require.NotNil(t, base)
	require.Same(t, base, ChromaStyle(&sty, nil), "base style should be memoized")

	withBg := ChromaStyle(&sty, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	require.NotNil(t, withBg)
	require.Same(t, withBg, ChromaStyle(&sty, color.RGBA{R: 1, G: 2, B: 3, A: 255}), "background style should be memoized")
}

func TestInvalidateStyleCaches(t *testing.T) {
	t.Parallel()

	// Must not panic and must allow the cache to rebuild.
	InvalidateChromaStyleCache()
	InvalidateStyleCaches()
}

func TestSyntaxHighlight(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	out, err := SyntaxHighlight(&sty, "package main\n", "main.go", nil)
	require.NoError(t, err)
	require.NotEmpty(t, out)

	out, err = SyntaxHighlightLexerName(&sty, "echo hi", "bash", nil)
	require.NoError(t, err)
	require.NotEmpty(t, out)

	// Unknown lexer name falls back without error.
	out, err = SyntaxHighlightLexerName(&sty, "x", "not-a-real-lexer", nil)
	require.NoError(t, err)
	require.NotEmpty(t, out)
}

func TestMarkdownRenderers(t *testing.T) {
	// Touches package-wide renderer caches, so no t.Parallel here.
	sty := styles.CharmtonePantera()

	require.NotNil(t, UserMarkdownRenderer(&sty, 80))
	require.Same(t, UserMarkdownRenderer(&sty, 80), UserMarkdownRenderer(&sty, 80))
	require.NotNil(t, PlanMarkdownRenderer(&sty, 80))

	InvalidateMarkdownRendererCache()
	require.NotNil(t, UserMarkdownRenderer(&sty, 80))
}

func TestStepMessageID(t *testing.T) {
	// Mutates the process-wide tracker, so no t.Parallel here.
	StartTurn()
	t.Cleanup(StartTurn)

	require.Empty(t, StepMessageID())
	StartStep("msg-1")
	require.Equal(t, "msg-1", StepMessageID())
}

func mustFirst[T any](a, _ T) T { return a }

func mustSecond[T any](_, b T) T { return b }
