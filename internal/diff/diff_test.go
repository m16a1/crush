package diff

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateDiff(t *testing.T) {
	t.Parallel()

	t.Run("no changes", func(t *testing.T) {
		t.Parallel()

		out, additions, removals := GenerateDiff("a\nb\n", "a\nb\n", "file.txt")
		require.Empty(t, out)
		require.Equal(t, 0, additions)
		require.Equal(t, 0, removals)
	})

	t.Run("addition", func(t *testing.T) {
		t.Parallel()

		out, additions, removals := GenerateDiff("a\nb\n", "a\nb\nc\n", "file.txt")
		require.Equal(t, 1, additions)
		require.Equal(t, 0, removals)
		require.Contains(t, out, "+c")
		require.Contains(t, out, "--- a/file.txt")
		require.Contains(t, out, "+++ b/file.txt")
	})

	t.Run("removal", func(t *testing.T) {
		t.Parallel()

		out, additions, removals := GenerateDiff("a\nb\nc\n", "a\nc\n", "file.txt")
		require.Equal(t, 0, additions)
		require.Equal(t, 1, removals)
		require.Contains(t, out, "-b")
	})

	t.Run("leading slash is trimmed from file name", func(t *testing.T) {
		t.Parallel()

		out, _, _ := GenerateDiff("a\n", "b\n", "/path/to/file.txt")
		require.Contains(t, out, "a/path/to/file.txt")
		require.Contains(t, out, "b/path/to/file.txt")
		require.False(t, strings.Contains(out, "a//path"))
	})

	t.Run("file headers are not counted", func(t *testing.T) {
		t.Parallel()

		// A single added line should not have its "+++ b/file" header
		// counted as an addition.
		_, additions, removals := GenerateDiff("", "one\n", "f")
		require.Equal(t, 1, additions)
		require.Equal(t, 0, removals)
	})
}
