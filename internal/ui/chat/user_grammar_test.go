package chat

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestUserMessageItem_GrammarVerdictBadge pins the grammar police
// rendering: a verdict attached to a user message renders as an
// addendum — ✅ for correct prose, ❌ with the corrected variant and
// explanation for incorrect prose — and never as part of the message
// text itself.
func TestUserMessageItem_GrammarVerdictBadge(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("incorrect shows cross with correction", func(t *testing.T) {
		t.Parallel()

		msg := &message.Message{
			ID:   "u1",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "helo werld"},
				message.GrammarVerdict{Correct: false, Corrected: "hello world", Explanation: "misspelled words"},
			},
		}
		out := ansi.Strip(NewUserMessageItem(&sty, msg, nil).RawRender(120))
		require.Contains(t, out, "helo werld", "the original message text stays visible")
		require.Contains(t, out, "❌ grammar")
		require.Contains(t, out, "Corrected: hello world")
		require.Contains(t, out, "misspelled words")
	})

	t.Run("correct shows only the check", func(t *testing.T) {
		t.Parallel()

		msg := &message.Message{
			ID:   "u2",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "hello world"},
				message.GrammarVerdict{Correct: true},
			},
		}
		out := ansi.Strip(NewUserMessageItem(&sty, msg, nil).RawRender(120))
		require.Contains(t, out, "✅ grammar ok")
		require.NotContains(t, out, "❌")
	})

	t.Run("late verdict appears after SetMessage", func(t *testing.T) {
		t.Parallel()

		msg := &message.Message{
			ID:   "u3",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "helo werld"},
			},
		}
		item := NewUserMessageItem(&sty, msg, nil)
		require.NotContains(t, ansi.Strip(item.RawRender(120)), "grammar",
			"no verdict means no badge")

		updated := &message.Message{
			ID:   "u3",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "helo werld"},
				message.GrammarVerdict{Correct: false, Corrected: "hello world", Explanation: "misspelled words"},
			},
		}
		userItem, ok := item.(*UserMessageItem)
		require.True(t, ok)
		userItem.SetMessage(updated)
		out := ansi.Strip(item.RawRender(120))
		require.Contains(t, out, "❌ grammar", "the badge must appear once the verdict lands")
		require.Contains(t, out, "Corrected: hello world")
	})
}
