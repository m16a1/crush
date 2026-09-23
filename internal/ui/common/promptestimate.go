package common

import "github.com/charmbracelet/crush/internal/message"

// EstimateInputTokens approximates the prompt tokens a message adds to the
// conversation, using the same characters-per-token ratio as the agent's usage
// fallback (internal/agent/usage_fallback.go). Providers size a request only
// once it ends, so this is what lets the working indicator estimate the size of
// the request in flight from the inputs that have landed since the last one.
func EstimateInputTokens(msg message.Message) int64 {
	var chars int
	for _, part := range msg.Parts {
		switch p := part.(type) {
		case message.TextContent:
			chars += len(p.Text)
		case message.ToolResult:
			chars += len(p.Name) + len(p.Content) + len(p.Metadata) + len(p.Data)
		case message.ShellCommand:
			chars += len(p.Command) + len(p.Output)
		case message.ImageURLContent:
			chars += len(p.URL)
		case message.BinaryContent:
			chars += len(p.MIMEType) + len(p.Path) + len(p.Data)
		}
	}
	return int64((chars + estimatedTokenChars - 1) / estimatedTokenChars)
}
