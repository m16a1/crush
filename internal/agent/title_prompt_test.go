package agent

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestRegenerateTitleBoundsALongFirstPrompt: a session that opens with a pasted
// file or log must still get a title, so only the opening of that prompt is
// sent to the title model instead of the whole thing.
func TestRegenerateTitleBoundsALongFirstPrompt(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &titleAnswerModel{}
	agent := testSessionAgent(env, model, model, "test system prompt")

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	const head = "investigate why the sidebar shows empty resource blocks"
	prompt := head + " " + strings.Repeat("lorem ipsum dolor sit amet ", 1000) + "TAILMARKER"
	userTextMessage(t, env, session.ID, prompt)

	require.NoError(t, agent.RegenerateTitle(t.Context(), session.ID))

	sent := model.lastPrompt()
	require.Contains(t, sent, head, "the title is based on the start of the prompt")
	require.NotContains(t, sent, "TAILMARKER", "the end of a long prompt is not sent")

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Sidebar metrics", updated.Title)
}

// TestGenerateTitleNamesSessionAfterALongPrompt: when the title model cannot
// name the session, the session is named after the prompt instead of keeping
// the placeholder name, so a long first message still ends up titled.
func TestGenerateTitleNamesSessionAfterALongPrompt(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := testSessionAgent(env, titleErrorModel{}, titleErrorModel{}, "test system prompt")

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	const prompt = "make the sidebar metrics easier to read"
	agent.GenerateTitle(t.Context(), session.ID, prompt)

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, prompt, updated.Title)
	require.NotEqual(t, DefaultSessionName, updated.Title)
}

// TestGenerateTitleTruncatesThePromptFallback: naming a session after a very
// long prompt still produces a title of the usual length rather than a paragraph.
func TestGenerateTitleTruncatesThePromptFallback(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := testSessionAgent(env, titleErrorModel{}, titleErrorModel{}, "test system prompt")

	session, err := env.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)

	prompt := strings.Repeat("refactor ", 500)
	agent.GenerateTitle(t.Context(), session.ID, prompt)

	updated, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.NotEqual(t, DefaultSessionName, updated.Title)
	require.LessOrEqual(t, ansi.StringWidth(updated.Title), maxTitleChars)
	require.True(t, strings.HasPrefix(prompt, strings.TrimSuffix(updated.Title, "…")),
		"the title is the start of the prompt, got %q", updated.Title)
}
