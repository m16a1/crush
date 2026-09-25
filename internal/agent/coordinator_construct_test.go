package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

const (
	realCoordinatorProviderID = "test-openai-compat"
	realCoordinatorModelID    = "test-model"
)

// newRealCoordinator builds a coordinator through the production
// NewCoordinator path. The existing coordinator tests wire the struct by
// hand with mocks, which leaves the constructor and every one of its
// pass-through methods without direct coverage.
func newRealCoordinator(t *testing.T) Coordinator {
	t.Helper()

	env := testEnv(t)

	// Isolate from the developer's real ~/.config/crush so the test neither
	// depends on nor slows down on whatever providers they have configured.
	t.Setenv("CRUSH_GLOBAL_CONFIG", t.TempDir())
	t.Setenv("CRUSH_GLOBAL_DATA", t.TempDir())

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)

	cfg.Config().Providers.Set(realCoordinatorProviderID, config.ProviderConfig{
		ID:      realCoordinatorProviderID,
		Name:    "Test",
		Type:    openaicompat.Name,
		BaseURL: "http://127.0.0.1:0/v1",
		APIKey:  "test",
		Models:  []catwalk.Model{{ID: realCoordinatorModelID, DefaultMaxTokens: 4096}},
	})
	selected := config.SelectedModel{Provider: realCoordinatorProviderID, Model: realCoordinatorModelID}
	cfg.OverridePreferredModel(config.SelectedModelTypeLarge, selected)
	cfg.OverridePreferredModel(config.SelectedModelTypeSmall, selected)
	cfg.SetupAgents()

	// Keep tool construction cheap and free of sub-agent wiring, exactly as
	// the shared test helper does.
	for _, name := range []string{config.AgentCoder, config.AgentPlan} {
		agentCfg := cfg.Config().Agents[name]
		agentCfg.AllowedTools = nil
		cfg.Config().Agents[name] = agentCfg
	}

	coord, err := NewCoordinator(t.Context(), CoordinatorOptions{
		Config:      cfg,
		Sessions:    env.sessions,
		Messages:    env.messages,
		Permissions: env.permissions,
		History:     env.history,
		FileTracker: *env.filetracker,
	})
	require.NoError(t, err)
	return coord
}

func TestNewCoordinatorStartsWithTheCoderAgent(t *testing.T) {

	c := newRealCoordinator(t)
	require.NotNil(t, c)

	require.Equal(t, realCoordinatorModelID, c.Model().ModelCfg.Model)
	require.Equal(t, realCoordinatorProviderID, c.Model().ModelCfg.Provider)
}

func TestCoordinatorLivenessQueriesDelegateToTheCurrentAgent(t *testing.T) {

	c := newRealCoordinator(t)

	require.False(t, c.IsBusy())
	require.False(t, c.IsSessionBusy("no-such-session"))
	require.Zero(t, c.QueuedPrompts("no-such-session"))
	require.Empty(t, c.QueuedPromptsList("no-such-session"))
}

func TestCoordinatorCancelAndClearQueueAreSafeWithoutARun(t *testing.T) {

	c := newRealCoordinator(t)

	require.NotPanics(t, func() {
		c.Cancel("no-such-session")
		c.ClearQueue("no-such-session")
		c.CancelAll()
	})
}

func TestBeginAcceptedReservesAHandle(t *testing.T) {

	c := newRealCoordinator(t)

	handle := c.BeginAccepted("session-1")
	require.NotNil(t, handle)
}

func TestSetMainAgentSwitchesBetweenConfiguredAgents(t *testing.T) {

	c := newRealCoordinator(t)

	require.NoError(t, c.SetMainAgent(config.AgentPlan))
	require.NoError(t, c.SetMainAgent(config.AgentCoder))

	err := c.SetMainAgent("not-an-agent")
	require.ErrorIs(t, err, errMainAgentNotFound)
}

func TestRegenerateTitleFailsFastForAnUnknownSession(t *testing.T) {

	c := newRealCoordinator(t)

	err := c.RegenerateTitle(t.Context(), "no-such-session")
	require.ErrorContains(t, err, "failed to get session")
}

func TestSummarizeFailsFastForAnUnknownSession(t *testing.T) {
	c := newRealCoordinator(t)

	err := c.Summarize(t.Context(), "no-such-session")
	require.ErrorContains(t, err, "failed to get session")
}
