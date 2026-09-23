package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func sampleSession() session.Session {
	return session.Session{
		ID:               "sess-123",
		Title:            "Fix the parser bug",
		CreatedAt:        1700000000,
		UpdatedAt:        1700000100,
		PromptTokens:     1200,
		CompletionTokens: 340,
		Cost:             0.0123,
	}
}

func sampleMessages() []message.Message {
	return []message.Message{
		{
			ID:    "m1",
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "Why does parsing fail?"}},
		},
		{
			ID:    "m2",
			Role:  message.Assistant,
			Model: "test-model",
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "Look at the tokenizer."},
				message.TextContent{Text: "The tokenizer drops the last byte."},
				message.ToolCall{ID: "t1", Name: "view", Input: `{"file_path":"parser.go"}`},
			},
		},
		{
			ID:   "m3",
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "t1", Name: "view", Content: "func parse() {}"},
			},
		},
		{
			ID:   "m4",
			Role: message.User,
			Parts: []message.ContentPart{
				message.ShellCommand{Command: "go test ./...", Output: "ok", ExitCode: 0},
			},
		},
	}
}

func TestMarkdownRendersSessionAndMessages(t *testing.T) {
	t.Parallel()

	out := Markdown(sampleSession(), sampleMessages())

	require.Contains(t, out, "# Fix the parser bug\n")
	require.Contains(t, out, "- **Session ID:** `sess-123`\n")
	require.Contains(t, out, "- **Messages:** 4\n")
	require.Contains(t, out, "- **Tokens:** input 1200, output 340\n")
	require.Contains(t, out, "- **Cost:** $0.0123\n")

	require.Contains(t, out, "## User\n\nWhy does parsing fail?\n")
	require.Contains(t, out, "## Assistant\n\n_Model: test-model_\n")
	require.Contains(t, out, "<details>\n<summary>Reasoning</summary>\n\nLook at the tokenizer.\n\n</details>\n")
	require.Contains(t, out, "The tokenizer drops the last byte.\n")
	require.Contains(t, out, "### Tool call: `view`\n\n```json\n{\"file_path\":\"parser.go\"}\n```\n")
	require.Contains(t, out, "### Tool result: `view`\n\n```\nfunc parse() {}\n```\n")
	require.Contains(t, out, "### Shell command\n\n```sh\ngo test ./...\n```\n\n```\nok\n```\n")
}

func TestMarkdownFallsBackToUntitledTitle(t *testing.T) {
	t.Parallel()

	out := Markdown(session.Session{ID: "x"}, nil)
	require.Contains(t, out, "# Untitled session\n")
	require.Contains(t, out, "- **Messages:** 0\n")
}

func TestMarkdownMarksErrorResultsAndStoppedTurns(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			ID:   "m1",
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{Name: "bash", Content: "boom", IsError: true},
			},
		},
		{
			ID:   "m2",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.Finish{Reason: message.FinishReasonError, Message: "rate limited"},
			},
		},
	}

	out := Markdown(session.Session{ID: "x"}, msgs)
	require.Contains(t, out, "### Tool result: `bash` (error)\n")
	require.Contains(t, out, "**Stopped:** error rate limited\n")
}

func TestMarkdownSkipsHiddenText(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{{
		ID:   "m1",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "visible"},
			message.TextContent{Text: "internal continuation", Hidden: true},
		},
	}}

	out := Markdown(session.Session{ID: "x"}, msgs)
	require.Contains(t, out, "visible")
	require.NotContains(t, out, "internal continuation")
}

func TestFileNameSlugifiesTitle(t *testing.T) {
	t.Parallel()

	require.Equal(t, "crush-export-fix-the-parser-bug.md", FileName(session.Session{Title: "Fix the  parser bug!"}))
	require.Equal(t, "crush-export-session-abc12345.md", FileName(session.Session{ID: "abc12345-6789"}))
	require.Equal(t, "crush-export-session-session.md", FileName(session.Session{}))
}

func TestWriteNeverOverwritesAnExistingExport(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sess := sampleSession()
	msgs := sampleMessages()

	first, err := Write(dir, sess, msgs)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "crush-export-fix-the-parser-bug.md"), first)

	second, err := Write(dir, sess, msgs)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.Equal(t, filepath.Join(dir, "crush-export-fix-the-parser-bug-2.md"), second)

	content, err := os.ReadFile(first)
	require.NoError(t, err)
	require.Equal(t, Markdown(sess, msgs), string(content))
}

func TestFencedEscalatesWhenContentContainsFences(t *testing.T) {
	t.Parallel()

	out := fenced("before\n```\ncode\n```\nafter", "")
	require.True(t, strings.HasPrefix(out, "````\n"), "fence grows past the backticks in the content")
	require.Contains(t, out, "\n````")
}
