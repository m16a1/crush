package util

import (
	"os"
	"path/filepath"
	"testing"

	powernap "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func editAt(startLine, startChar, endLine, endChar uint32, newText string) protocol.TextEdit {
	return protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startChar},
			End:   protocol.Position{Line: endLine, Character: endChar},
		},
		NewText: newText,
	}
}

func TestApplyTextEditsRewritesFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		edit     protocol.TextEdit
		encoding powernap.OffsetEncoding
		want     string
	}{
		{
			name:     "single line replacement",
			content:  "hello world\n",
			edit:     editAt(0, 0, 0, 5, "goodbye"),
			encoding: powernap.UTF8,
			want:     "goodbye world\n",
		},
		{
			name:     "deletion drops the emptied line",
			content:  "abc\ndef\n",
			edit:     editAt(0, 0, 0, 3, ""),
			encoding: powernap.UTF8,
			want:     "def\n",
		},
		{
			name:     "deletion keeps surrounding text",
			content:  "abc\ndef\n",
			edit:     editAt(0, 1, 0, 2, ""),
			encoding: powernap.UTF8,
			want:     "ac\ndef\n",
		},
		{
			name:     "multi line replacement",
			content:  "a\nb\nc\n",
			edit:     editAt(0, 0, 1, 1, "X\nY"),
			encoding: powernap.UTF8,
			want:     "X\nY\nc\n",
		},
		{
			name:     "CRLF is preserved",
			content:  "a\r\nb\r\n",
			edit:     editAt(0, 0, 0, 1, "z"),
			encoding: powernap.UTF8,
			want:     "z\r\nb\r\n",
		},
		{
			name:     "file without trailing newline stays without one",
			content:  "abc",
			edit:     editAt(0, 0, 0, 1, "z"),
			encoding: powernap.UTF8,
			want:     "zbc",
		},
		{
			name:     "UTF32 codepoint offsets",
			content:  "你好\n",
			edit:     editAt(0, 0, 0, 2, "X"),
			encoding: powernap.UTF32,
			want:     "X\n",
		},
		{
			name:     "UTF16 offsets over multibyte text",
			content:  "你好world\n",
			edit:     editAt(0, 2, 0, 2, "!"),
			encoding: powernap.UTF16,
			want:     "你好!world\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeFile(t, filepath.Join(t.TempDir(), "f.txt"), tt.content)
			require.NoError(t, applyTextEdits(protocol.URIFromPath(path), []protocol.TextEdit{tt.edit}, tt.encoding))

			got, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
		})
	}
}

func TestApplyTextEditsErrors(t *testing.T) {
	t.Parallel()

	t.Run("invalid URI", func(t *testing.T) {
		t.Parallel()
		err := applyTextEdits(protocol.DocumentURI("relative/path"), nil, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid URI")
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		uri := protocol.URIFromPath(filepath.Join(t.TempDir(), "missing.txt"))
		err := applyTextEdits(uri, nil, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read file")
	})

	t.Run("overlapping edits", func(t *testing.T) {
		t.Parallel()
		path := writeFile(t, filepath.Join(t.TempDir(), "f.txt"), "abcdef\n")
		err := applyTextEdits(protocol.URIFromPath(path), []protocol.TextEdit{
			editAt(0, 0, 0, 3, "x"),
			editAt(0, 2, 0, 4, "y"),
		}, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "overlapping edits")
	})

	t.Run("adjacent edits are allowed", func(t *testing.T) {
		t.Parallel()
		path := writeFile(t, filepath.Join(t.TempDir(), "f.txt"), "abcdef\n")
		require.NoError(t, applyTextEdits(protocol.URIFromPath(path), []protocol.TextEdit{
			editAt(0, 0, 0, 3, "x"),
			editAt(0, 3, 0, 6, "y"),
		}, powernap.UTF8))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "xy\n", string(got))
	})
}

func TestApplyTextEditErrors(t *testing.T) {
	t.Parallel()

	_, err := applyTextEdit([]string{"only"}, editAt(5, 0, 5, 0, "x"), powernap.UTF8)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid start line")
}

func TestApplyTextEditClampsOutOfRangePositions(t *testing.T) {
	t.Parallel()

	lines := []string{"abc", "def"}
	got, err := applyTextEdit(lines, editAt(0, 99, 5, 99, "X"), powernap.UTF8)
	require.NoError(t, err)
	// The end line is clamped to the last line, and both offsets are
	// clamped to the line lengths, so the edit consumes the tail.
	require.Equal(t, []string{"abcX"}, got)
}

func TestUtf32ToByteOffset(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, utf32ToByteOffset("abc", 0))
	require.Equal(t, 1, utf32ToByteOffset("abc", 1))
	require.Equal(t, 3, utf32ToByteOffset("abc", 9))

	// "你" is 3 bytes, so codepoint 1 is byte 3.
	require.Equal(t, 3, utf32ToByteOffset("你好", 1))
	require.Equal(t, 6, utf32ToByteOffset("你好", 2))
}

func TestApplyDocumentChangeCreateFile(t *testing.T) {
	t.Parallel()

	t.Run("creates an empty file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "new.txt")
		err := applyDocumentChange(protocol.DocumentChange{
			CreateFile: &protocol.CreateFile{URI: protocol.URIFromPath(path)},
		}, powernap.UTF8)
		require.NoError(t, err)

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("ignore if exists leaves content untouched", func(t *testing.T) {
		t.Parallel()
		path := writeFile(t, filepath.Join(t.TempDir(), "new.txt"), "keep")
		err := applyDocumentChange(protocol.DocumentChange{
			CreateFile: &protocol.CreateFile{
				URI:     protocol.URIFromPath(path),
				Options: &protocol.CreateFileOptions{IgnoreIfExists: true},
			},
		}, powernap.UTF8)
		require.NoError(t, err)

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "keep", string(got))
	})

	t.Run("overwrite truncates", func(t *testing.T) {
		t.Parallel()
		path := writeFile(t, filepath.Join(t.TempDir(), "new.txt"), "old")
		err := applyDocumentChange(protocol.DocumentChange{
			CreateFile: &protocol.CreateFile{
				URI:     protocol.URIFromPath(path),
				Options: &protocol.CreateFileOptions{Overwrite: true},
			},
		}, powernap.UTF8)
		require.NoError(t, err)

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("invalid URI", func(t *testing.T) {
		t.Parallel()
		err := applyDocumentChange(protocol.DocumentChange{
			CreateFile: &protocol.CreateFile{URI: protocol.DocumentURI("relative")},
		}, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid URI")
	})
}

func TestApplyDocumentChangeDeleteFile(t *testing.T) {
	t.Parallel()

	t.Run("deletes a file", func(t *testing.T) {
		t.Parallel()
		path := writeFile(t, filepath.Join(t.TempDir(), "gone.txt"), "x")
		err := applyDocumentChange(protocol.DocumentChange{
			DeleteFile: &protocol.DeleteFile{URI: protocol.URIFromPath(path)},
		}, powernap.UTF8)
		require.NoError(t, err)
		require.NoFileExists(t, path)
	})

	t.Run("recursive deletes a directory tree", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755))
		err := applyDocumentChange(protocol.DocumentChange{
			DeleteFile: &protocol.DeleteFile{
				URI:     protocol.URIFromPath(dir),
				Options: &protocol.DeleteFileOptions{Recursive: true},
			},
		}, powernap.UTF8)
		require.NoError(t, err)
		require.NoDirExists(t, dir)
	})

	t.Run("missing file errors", func(t *testing.T) {
		t.Parallel()
		err := applyDocumentChange(protocol.DocumentChange{
			DeleteFile: &protocol.DeleteFile{
				URI: protocol.URIFromPath(filepath.Join(t.TempDir(), "missing.txt")),
			},
		}, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to delete file")
	})

	t.Run("invalid URI", func(t *testing.T) {
		t.Parallel()
		err := applyDocumentChange(protocol.DocumentChange{
			DeleteFile: &protocol.DeleteFile{URI: protocol.DocumentURI("relative")},
		}, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid URI")
	})
}

func TestApplyDocumentChangeRenameFile(t *testing.T) {
	t.Parallel()

	t.Run("renames", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		oldPath := writeFile(t, filepath.Join(dir, "old.txt"), "x")
		newPath := filepath.Join(dir, "new.txt")

		err := applyDocumentChange(protocol.DocumentChange{
			RenameFile: &protocol.RenameFile{
				OldURI: protocol.URIFromPath(oldPath),
				NewURI: protocol.URIFromPath(newPath),
			},
		}, powernap.UTF8)
		require.NoError(t, err)
		require.NoFileExists(t, oldPath)
		require.FileExists(t, newPath)
	})

	t.Run("refuses to clobber without overwrite", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		oldPath := writeFile(t, filepath.Join(dir, "old.txt"), "x")
		newPath := writeFile(t, filepath.Join(dir, "new.txt"), "y")

		err := applyDocumentChange(protocol.DocumentChange{
			RenameFile: &protocol.RenameFile{
				OldURI:  protocol.URIFromPath(oldPath),
				NewURI:  protocol.URIFromPath(newPath),
				Options: &protocol.RenameFileOptions{},
			},
		}, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "already exists")
	})

	t.Run("overwrite clobbers", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		oldPath := writeFile(t, filepath.Join(dir, "old.txt"), "x")
		newPath := writeFile(t, filepath.Join(dir, "new.txt"), "y")

		err := applyDocumentChange(protocol.DocumentChange{
			RenameFile: &protocol.RenameFile{
				OldURI:  protocol.URIFromPath(oldPath),
				NewURI:  protocol.URIFromPath(newPath),
				Options: &protocol.RenameFileOptions{Overwrite: true},
			},
		}, powernap.UTF8)
		require.NoError(t, err)

		got, err := os.ReadFile(newPath)
		require.NoError(t, err)
		require.Equal(t, "x", string(got))
	})

	t.Run("invalid old URI", func(t *testing.T) {
		t.Parallel()
		err := applyDocumentChange(protocol.DocumentChange{
			RenameFile: &protocol.RenameFile{OldURI: "relative", NewURI: "relative2"},
		}, powernap.UTF8)
		require.Error(t, err)
	})

	t.Run("invalid new URI", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		oldPath := writeFile(t, filepath.Join(dir, "old.txt"), "x")
		err := applyDocumentChange(protocol.DocumentChange{
			RenameFile: &protocol.RenameFile{OldURI: protocol.URIFromPath(oldPath), NewURI: "relative"},
		}, powernap.UTF8)
		require.Error(t, err)
	})
}

func TestApplyDocumentChangeTextDocumentEdit(t *testing.T) {
	t.Parallel()

	t.Run("applies text edits", func(t *testing.T) {
		t.Parallel()
		path := writeFile(t, filepath.Join(t.TempDir(), "f.txt"), "hello\n")
		err := applyDocumentChange(protocol.DocumentChange{
			TextDocumentEdit: &protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{URI: protocol.URIFromPath(path)},
				Edits: []protocol.Or_TextDocumentEdit_edits_Elem{
					{Value: editAt(0, 0, 0, 5, "bye")},
				},
			},
		}, powernap.UTF8)
		require.NoError(t, err)

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "bye\n", string(got))
	})

	t.Run("invalid edit type", func(t *testing.T) {
		t.Parallel()
		path := writeFile(t, filepath.Join(t.TempDir(), "f.txt"), "hello\n")
		err := applyDocumentChange(protocol.DocumentChange{
			TextDocumentEdit: &protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{URI: protocol.URIFromPath(path)},
				Edits:        []protocol.Or_TextDocumentEdit_edits_Elem{{Value: "nonsense"}},
			},
		}, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid edit type")
	})
}

func TestApplyDocumentChangeEmptyIsNoop(t *testing.T) {
	t.Parallel()

	require.NoError(t, applyDocumentChange(protocol.DocumentChange{}, powernap.UTF8))
}

func TestApplyWorkspaceEdit(t *testing.T) {
	t.Parallel()

	t.Run("applies Changes", func(t *testing.T) {
		t.Parallel()
		path := writeFile(t, filepath.Join(t.TempDir(), "f.txt"), "hello\n")
		err := ApplyWorkspaceEdit(protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentURI][]protocol.TextEdit{
				protocol.URIFromPath(path): {editAt(0, 0, 0, 5, "bye")},
			},
		}, powernap.UTF8)
		require.NoError(t, err)

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "bye\n", string(got))
	})

	t.Run("applies DocumentChanges", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "new.txt")
		err := ApplyWorkspaceEdit(protocol.WorkspaceEdit{
			DocumentChanges: []protocol.DocumentChange{
				{CreateFile: &protocol.CreateFile{URI: protocol.URIFromPath(path)}},
			},
		}, powernap.UTF8)
		require.NoError(t, err)
		require.FileExists(t, path)
	})

	t.Run("propagates change errors", func(t *testing.T) {
		t.Parallel()
		err := ApplyWorkspaceEdit(protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentURI][]protocol.TextEdit{
				"relative": {editAt(0, 0, 0, 1, "x")},
			},
		}, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to apply text edits")
	})

	t.Run("propagates document change errors", func(t *testing.T) {
		t.Parallel()
		err := ApplyWorkspaceEdit(protocol.WorkspaceEdit{
			DocumentChanges: []protocol.DocumentChange{
				{CreateFile: &protocol.CreateFile{URI: "relative"}},
			},
		}, powernap.UTF8)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to apply document change")
	})
}
