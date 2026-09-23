// Package export renders a session and its messages as portable Markdown and
// writes it to disk. It lives outside the TUI so the same renderer can back
// other frontends without depending on terminal styling.
package export

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// fileNameMaxSlug bounds the title-derived portion of a generated file name so
// long session titles do not produce unwieldy paths.
const fileNameMaxSlug = 60

// Markdown renders a session and its messages as Markdown. The output is
// meant to be readable both as a file and when pasted into an issue or pull
// request.
func Markdown(sess session.Session, msgs []message.Message) string {
	var b strings.Builder

	writeHeader(&b, sess, len(msgs))
	for _, msg := range msgs {
		writeMessage(&b, msg)
	}

	return b.String()
}

// Write renders the session as Markdown and writes it into dir, returning the
// path of the file it created. The name is derived from the session title and
// never overwrites an existing file.
func Write(dir string, sess session.Session, msgs []message.Message) (string, error) {
	if dir == "" {
		dir = "."
	}

	path := filepath.Join(dir, FileName(sess))
	stem := strings.TrimSuffix(path, ".md")
	for i := 2; ; i++ {
		_, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		path = fmt.Sprintf("%s-%d.md", stem, i)
	}

	if err := os.WriteFile(path, []byte(Markdown(sess, msgs)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// FileName returns the Markdown file name a session exports to. It falls back
// to the session ID when the title has no usable characters.
func FileName(sess session.Session) string {
	slug := slugify(sess.Title)
	if slug == "" {
		id := sess.ID
		if len(id) > 8 {
			id = id[:8]
		}
		if id == "" {
			id = "session"
		}
		slug = "session-" + id
	}
	return "crush-export-" + slug + ".md"
}

func writeHeader(b *strings.Builder, sess session.Session, count int) {
	title := strings.TrimSpace(sess.Title)
	if title == "" {
		title = "Untitled session"
	}
	b.WriteString("# " + title + "\n\n")

	fmt.Fprintf(b, "- **Session ID:** `%s`\n", sess.ID)
	if sess.CreatedAt > 0 {
		fmt.Fprintf(b, "- **Created:** %s\n", formatTime(sess.CreatedAt))
	}
	if sess.UpdatedAt > 0 {
		fmt.Fprintf(b, "- **Updated:** %s\n", formatTime(sess.UpdatedAt))
	}
	fmt.Fprintf(b, "- **Messages:** %d\n", count)
	if sess.PromptTokens > 0 || sess.CompletionTokens > 0 {
		fmt.Fprintf(b, "- **Tokens:** input %d, output %d\n", sess.PromptTokens, sess.CompletionTokens)
	}
	if sess.Cost > 0 {
		fmt.Fprintf(b, "- **Cost:** $%.4f\n", sess.Cost)
	}
	b.WriteString("\n---\n\n")
}

func writeMessage(b *strings.Builder, msg message.Message) {
	heading := roleHeading(msg.Role)
	if msg.IsSummaryMessage {
		heading += " (summary)"
	}
	b.WriteString("## " + heading + "\n\n")

	if msg.Role == message.Assistant && msg.Model != "" {
		fmt.Fprintf(b, "_Model: %s_\n\n", msg.Model)
	}

	for _, part := range msg.Parts {
		switch p := part.(type) {
		case message.TextContent:
			if p.Hidden || strings.TrimSpace(p.Text) == "" {
				continue
			}
			b.WriteString(strings.TrimRight(p.Text, "\n") + "\n\n")
		case message.ReasoningContent:
			if strings.TrimSpace(p.Thinking) == "" {
				continue
			}
			b.WriteString("<details>\n<summary>Reasoning</summary>\n\n")
			b.WriteString(strings.TrimRight(p.Thinking, "\n") + "\n\n")
			b.WriteString("</details>\n\n")
		case message.ToolCall:
			writeToolCall(b, p)
		case message.ToolResult:
			writeToolResult(b, p)
		case message.ShellCommand:
			writeShellCommand(b, p)
		case message.ImageURLContent:
			writeImageURL(b, p)
		case message.BinaryContent:
			writeBinary(b, p)
		case message.Finish:
			writeFinish(b, p)
		}
	}
}

func writeToolCall(b *strings.Builder, tc message.ToolCall) {
	name := strings.TrimSpace(tc.Name)
	if name == "" {
		name = "unknown"
	}
	fmt.Fprintf(b, "### Tool call: `%s`\n\n", name)

	input := strings.TrimSpace(tc.Input)
	if input == "" {
		return
	}
	b.WriteString(fenced(input, "json") + "\n\n")
}

func writeToolResult(b *strings.Builder, tr message.ToolResult) {
	name := strings.TrimSpace(tr.Name)
	if name == "" {
		name = "unknown"
	}
	label := fmt.Sprintf("### Tool result: `%s`", name)
	if tr.IsError {
		label += " (error)"
	}
	b.WriteString(label + "\n\n")

	content := strings.TrimRight(tr.Content, "\n")
	if strings.TrimSpace(content) != "" {
		b.WriteString(fenced(content, "") + "\n\n")
		return
	}
	if tr.Data != "" {
		mime := tr.MIMEType
		if mime == "" {
			mime = "unknown"
		}
		fmt.Fprintf(b, "[binary data: %s]\n\n", mime)
	}
}

func writeShellCommand(b *strings.Builder, sc message.ShellCommand) {
	label := "### Shell command"
	if sc.ExitCode != 0 {
		label += fmt.Sprintf(" (exit %d)", sc.ExitCode)
	}
	b.WriteString(label + "\n\n")
	b.WriteString(fenced(sc.Command, "sh") + "\n\n")
	if out := strings.TrimRight(sc.Output, "\n"); strings.TrimSpace(out) != "" {
		b.WriteString(fenced(out, "") + "\n\n")
	}
}

func writeImageURL(b *strings.Builder, img message.ImageURLContent) {
	url := strings.TrimSpace(img.URL)
	if url == "" {
		return
	}
	if strings.HasPrefix(url, "data:") {
		b.WriteString("[image: inline data]\n\n")
		return
	}
	fmt.Fprintf(b, "[image](%s)\n\n", url)
}

func writeBinary(b *strings.Builder, bc message.BinaryContent) {
	label := strings.TrimSpace(bc.Path)
	if label == "" {
		label = bc.MIMEType
	}
	if label == "" {
		label = "unknown"
	}
	fmt.Fprintf(b, "[binary: %s]\n\n", label)
}

func writeFinish(b *strings.Builder, f message.Finish) {
	if f.Reason != message.FinishReasonError && f.Reason != message.FinishReasonContentFilter {
		return
	}
	fmt.Fprintf(b, "**Stopped:** %s", f.Reason)
	if f.Message != "" {
		fmt.Fprintf(b, " %s", f.Message)
	}
	b.WriteString("\n\n")
}

// fenced wraps content in a code fence long enough to survive backticks
// already present in the content, optionally tagged with a language.
func fenced(content, lang string) string {
	longest := 0
	run := 0
	for _, r := range content {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + lang + "\n" + content + "\n" + fence
}

func roleHeading(role message.MessageRole) string {
	switch role {
	case message.User:
		return "User"
	case message.Assistant:
		return "Assistant"
	case message.Tool:
		return "Tool"
	case message.System:
		return "System"
	default:
		if role == "" {
			return "Message"
		}
		return string(role)
	}
}

func formatTime(unix int64) string {
	return time.Unix(unix, 0).Format(time.RFC3339)
}

// slugify reduces a title to a filesystem-friendly identifier, keeping ASCII
// letters and digits and collapsing everything else into single dashes.
func slugify(title string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > fileNameMaxSlug {
		slug = strings.Trim(slug[:fileNameMaxSlug], "-")
	}
	return slug
}
