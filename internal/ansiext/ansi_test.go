package ansiext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEscapeRendersControlCharactersAsControlPictures(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"\x00": "\u2400",
		"\x01": "\u2401",
		"\t":   "\u2409",
		"\n":   "\u240a",
		"\r":   "\u240d",
		"\x1b": "\u241b",
		"\x1f": "\u241f",
		"\x7f": "\u2421", // DEL gets the one non-sequential control picture
	}

	for in, want := range tests {
		require.Equal(t, want, Escape(in), "escaping %q", in)
	}
}

func TestEscapeLeavesPrintableTextAlone(t *testing.T) {
	t.Parallel()

	for _, s := range []string{
		"",
		"hello",
		"héllo → 世界",
		"a row with a space ",
	} {
		require.Equal(t, s, Escape(s))
	}
}

func TestEscapeKeepsSurroundingTextWhenReplacingControls(t *testing.T) {
	t.Parallel()

	require.Equal(t, "a\u240ab", Escape("a\nb"))
	require.Equal(t, "\u241b[31mred", Escape("\x1b[31mred"))
}
