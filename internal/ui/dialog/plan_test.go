package dialog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
)

func TestAvgTokensPerSecond(t *testing.T) {
	t.Parallel()
	now := time.Now().Unix()

	// 100 tokens over 10 seconds -> 10 t/s for this message.
	assistant := message.Message{
		Role:      message.Assistant,
		CreatedAt: now - 10,
		Parts: []message.ContentPart{
			message.Finish{Reason: message.FinishReasonEndTurn, Time: now, CompletionTokens: 100},
		},
	}
	// 40 tokens over 20 seconds -> 2 t/s.
	slow := message.Message{
		Role:      message.Assistant,
		CreatedAt: now - 20,
		Parts: []message.ContentPart{
			message.Finish{Reason: message.FinishReasonEndTurn, Time: now, CompletionTokens: 40},
		},
	}
	// User messages and unfinished messages are ignored.
	user := message.Message{Role: message.User, CreatedAt: now - 1}
	unfinished := message.Message{Role: message.Assistant, CreatedAt: now - 5}

	got := avgTokensPerSecond([]message.Message{assistant, slow, user, unfinished})
	// 140 tokens over 30 seconds.
	require.InDelta(t, 140.0/30.0, got, 0.01)

	require.Zero(t, avgTokensPerSecond(nil))
	require.Zero(t, avgTokensPerSecond([]message.Message{user, unfinished}))
}

func TestAvgTokensPerSecondReasoningStart(t *testing.T) {
	t.Parallel()
	now := time.Now().Unix()
	// Reasoning started 30s ago; the message record was created 5s ago.
	// The 100 tokens were produced over the 30s window -> ~3.33 t/s.
	m := message.Message{
		Role:      message.Assistant,
		CreatedAt: now - 5,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "hmm", StartedAt: now - 30, FinishedAt: now - 10},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: now, CompletionTokens: 100},
		},
	}
	got := avgTokensPerSecond([]message.Message{m})
	require.InDelta(t, 100.0/30.0, got, 0.01)
}
