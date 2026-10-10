package model

import (
	"context"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/attachments"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestCurrentModelSupportsImages(t *testing.T) {
	t.Parallel()

	t.Run("returns false when config is nil", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns false when coder agent is missing", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Providers: csync.NewMap[string, config.ProviderConfig](),
			Agents:    map[string]config.Agent{},
		}
		ui := newTestUIWithConfig(t, cfg)
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns false when model is not found", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Providers: csync.NewMap[string, config.ProviderConfig](),
			Agents: map[string]config.Agent{
				config.AgentCoder: {Model: config.SelectedModelTypeLarge},
			},
		}
		ui := newTestUIWithConfig(t, cfg)
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns true when current model supports images", func(t *testing.T) {
		t.Parallel()

		providers := csync.NewMap[string, config.ProviderConfig]()
		providers.Set("test-provider", config.ProviderConfig{
			ID: "test-provider",
			Models: []catwalk.Model{
				{ID: "test-model", SupportsImages: true},
			},
		})

		cfg := &config.Config{
			Models: map[config.SelectedModelType]config.SelectedModel{
				config.SelectedModelTypeLarge: {
					Provider: "test-provider",
					Model:    "test-model",
				},
			},
			Providers: providers,
			Agents: map[string]config.Agent{
				config.AgentCoder: {Model: config.SelectedModelTypeLarge},
			},
		}

		ui := newTestUIWithConfig(t, cfg)
		require.True(t, ui.currentModelSupportsImages())
	})
}

func TestMouseMode(t *testing.T) {
	t.Parallel()

	t.Run("returns no mouse mode when disabled", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, tea.MouseModeNone, mouseMode(false, false))
		require.Equal(t, tea.MouseModeNone, mouseMode(false, true))
	})

	t.Run("returns cell motion when enabled and no inline editor is active", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, tea.MouseModeCellMotion, mouseMode(true, false))
	})

	t.Run("returns all motion when enabled and an inline editor is active", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, tea.MouseModeAllMotion, mouseMode(true, true))
	})
}

func newTestUIWithConfig(t *testing.T, cfg *config.Config) *UI {
	t.Helper()

	return &UI{
		com: &common.Common{
			Workspace: &testWorkspace{cfg: cfg},
		},
	}
}

// testWorkspace is a minimal [workspace.Workspace] stub for unit tests.
type testWorkspace struct {
	workspace.Workspace
	cfg               *config.Config
	setMainCalledWith string
	updateCalls       int
	agentReady        bool
	agentBusy         bool
	runPrompts        []string
	yolo              bool
	runHidden         []bool
	compactCalls      []bool
}

func (w *testWorkspace) Config() *config.Config {
	return w.cfg
}

func (w *testWorkspace) WorkingDir() string {
	return "/tmp/crush-test"
}

func (w *testWorkspace) AgentSetMain(agentID string) error {
	w.setMainCalledWith = agentID
	return nil
}

func (w *testWorkspace) UpdateAgentModel(context.Context) error {
	w.updateCalls++
	return nil
}

func (w *testWorkspace) PermissionSkipRequests() bool { return w.yolo }

func (w *testWorkspace) PermissionSetSkipRequests(skip bool) { w.yolo = skip }

func (w *testWorkspace) AgentIsReady() bool {
	return w.agentReady
}

func (w *testWorkspace) AgentIsBusy() bool {
	return w.agentBusy
}

func (w *testWorkspace) AgentReadyErr() error {
	if !w.agentReady {
		return workspace.ErrAgentNotInitialized
	}
	return nil
}

func (w *testWorkspace) AgentRun(ctx context.Context, _ string, prompt string, _ ...message.Attachment) error {
	w.runPrompts = append(w.runPrompts, prompt)
	w.runHidden = append(w.runHidden, message.HiddenUserMessage(ctx))
	return nil
}

func (w *testWorkspace) SetCompactMode(scope config.Scope, compact bool) error {
	w.compactCalls = append(w.compactCalls, compact)
	return nil
}

func TestDefaultKeyMapHasShiftTab(t *testing.T) {
	t.Parallel()

	km := DefaultKeyMap()
	require.Equal(t, []string{"shift+tab"}, km.ShiftTab.Keys())
}

func TestToggleInputMode(t *testing.T) {
	t.Parallel()
	ui, ws := newPlanUI(t, "sess-1")
	ui.mode = uiInputModeCode
	for _, want := range []struct {
		mode    uiInputMode
		yolo    bool
		updates int
	}{
		{uiInputModePlan, false, 1},
		{uiInputModeCode, true, 2},
		{uiInputModeCode, false, 2},
	} {
		applyModeSwitchMsg(ui, ui.toggleInputMode())
		require.Equal(t, want.mode, ui.mode)
		require.Equal(t, want.yolo, ws.yolo)
		require.Equal(t, want.updates, ws.updateCalls)
	}
}

func newPlanUI(t *testing.T, sessionID string) (*UI, *testWorkspace) {
	t.Helper()
	sty := styles.CharmtonePantera()
	cfg := &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}
	ws := &testWorkspace{cfg: cfg}
	var sess *session.Session
	if sessionID != "" {
		s := session.Session{ID: sessionID}
		sess = &s
	}
	com := &common.Common{
		Workspace: ws,
		Styles:    &sty,
	}
	u := &UI{
		com:      com,
		mode:     uiInputModePlan,
		textarea: textarea.New(),
		dialog:   dialog.NewOverlay(),
		session:  sess,
		chat:     NewChat(com, config.ScrollbarDefault),
	}
	return u, ws
}

// applyModeSwitchMsg runs a setInputMode command to completion the way the
// real event loop does: execute the async switch, then feed the resulting
// modeSwitchedMsg through the finalizer. Returns the finalizer's cmds so
// tests can execute them when they care about the queued work.
func applyModeSwitchMsg(u *UI, cmd tea.Cmd) []tea.Cmd {
	t := cmd()
	switched, ok := t.(modeSwitchedMsg)
	if !ok {
		return nil
	}
	u.modeSwitching = false
	return u.applyModeSwitch(switched)
}

func TestToggleSidebarKeyBinding(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	ws := &testWorkspace{cfg: &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}}
	com := &common.Common{Workspace: ws, Styles: &sty}
	keyMap := DefaultKeyMap()
	att := attachments.New(nil, attachments.Keymap{
		DeleteMode: keyMap.Editor.AttachmentDeleteMode,
		DeleteAll:  keyMap.Editor.DeleteAllAttachments,
		Escape:     keyMap.Editor.Escape,
	})
	u := &UI{
		com:         com,
		keyMap:      keyMap,
		state:       uiChat,
		focus:       uiFocusSidebar,
		session:     &session.Session{ID: "sess-1"},
		chat:        NewChat(com, config.ScrollbarDefault),
		textarea:    textarea.New(),
		dialog:      dialog.NewOverlay(),
		attachments: att,
		width:       140,
		height:      45,
	}
	u.status = NewStatus(com, u)

	// Ctrl+b hides the sidebar and moves focus off it, persisting compact
	// mode.
	u.handleKeyPressMsg(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.True(t, u.forceCompactMode)
	require.True(t, u.isCompact)
	require.Equal(t, uiFocusEditor, u.focus)
	require.Equal(t, []bool{true}, ws.compactCalls)

	// Ctrl+b again shows the sidebar.
	u.handleKeyPressMsg(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.False(t, u.forceCompactMode)
	require.False(t, u.isCompact)
	require.Equal(t, []bool{true, false}, ws.compactCalls)

	// The binding is advertised in the help bar while the sidebar is
	// available.
	var shortHelp []string
	for _, b := range u.ShortHelp() {
		shortHelp = append(shortHelp, b.Help().Desc)
	}
	require.Contains(t, shortHelp, "toggle sidebar")

	var fullHelp []string
	for _, row := range u.FullHelp() {
		for _, b := range row {
			fullHelp = append(fullHelp, b.Help().Desc)
		}
	}
	require.Contains(t, fullHelp, "toggle sidebar")
}

func TestSetInputMode_SwitchesToCode(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
	ws := &testWorkspace{cfg: cfg}
	u := &UI{
		com:      &common.Common{Workspace: ws},
		mode:     uiInputModePlan,
		textarea: textarea.New(),
	}
	applyModeSwitchMsg(u, u.setInputMode(uiInputModeCode))
	require.Equal(t, uiInputModeCode, u.mode)
	require.Equal(t, config.AgentCoder, ws.setMainCalledWith)
}

func TestSetInputMode_SwitchesToPlan(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
	ws := &testWorkspace{cfg: cfg}
	u := &UI{
		com:      &common.Common{Workspace: ws},
		mode:     uiInputModeCode,
		textarea: textarea.New(),
	}
	applyModeSwitchMsg(u, u.setInputMode(uiInputModePlan))
	require.Equal(t, uiInputModePlan, u.mode)
	require.Equal(t, config.AgentPlan, ws.setMainCalledWith)
}

func TestToggleInputMode_BlockedWhileAgentBusy(t *testing.T) {
	t.Parallel()
	u, ws := newPlanUI(t, "sess-1")
	ws.agentReady = true
	ws.agentBusy = true
	// isAgentBusy reads the memoized cache, so seed it with the same value
	// the workspace stub reports.
	u.agentBusyCache.set(true)

	msg := u.toggleInputMode()()
	require.Equal(t, uiInputModePlan, u.mode, "mode must not change while the agent is busy")
	require.Empty(t, ws.setMainCalledWith)
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

func TestSetInputMode_TracksModeSwitching(t *testing.T) {
	t.Parallel()
	u, _ := newPlanUI(t, "sess-1")

	cmd := u.setInputMode(uiInputModeCode)
	require.True(t, u.modeSwitching, "flag must be set until the async model update completes")

	msg, ok := cmd().(modeSwitchedMsg)
	require.True(t, ok)
	require.NoError(t, msg.err)
	require.Equal(t, uiInputModeCode, msg.mode)
	require.False(t, msg.yolo)
}

func TestResetPlanModeState(t *testing.T) {
	t.Parallel()
	u, ws := newPlanUI(t, "sess-1")

	cmd := u.resetPlanModeState()
	require.NotNil(t, cmd)
	applyModeSwitchMsg(u, cmd)
	require.Equal(t, uiInputModeCode, u.mode)
	require.Equal(t, config.AgentCoder, ws.setMainCalledWith)
}

func TestResetPlanModeState_NoopInCodeMode(t *testing.T) {
	t.Parallel()
	u, ws := newPlanUI(t, "sess-1")
	u.mode = uiInputModeCode

	cmd := u.resetPlanModeState()
	require.Nil(t, cmd)
	require.Equal(t, uiInputModeCode, u.mode)
	require.Empty(t, ws.setMainCalledWith)
}

func TestPlanPromptIgnoresYOLO(t *testing.T) {
	t.Parallel()
	u, _ := newPlanUI(t, "sess-1")
	u.textarea.SetWidth(40)
	u.textarea.Focus()
	u.setEditorPrompt(true)
	require.Contains(t, u.textarea.View(), "⏸")
	require.NotContains(t, u.textarea.View(), " ! ")
}

func TestGeneratedPlanContinuationIsHidden(t *testing.T) {
	t.Parallel()
	u, ws := newPlanUI(t, "sess-1")
	ws.agentReady = true
	for _, hidden := range []bool{true, false} {
		batch := u.sendMessageInternal("Implement the plan.", hidden)().(tea.BatchMsg)
		for _, cmd := range batch {
			if cmd != nil {
				cmd()
			}
		}
	}
	require.Equal(t, []bool{true, false}, ws.runHidden)
	require.Equal(t, []string{"Implement the plan.", "Implement the plan."}, ws.runPrompts)
}

func TestToggleInputModePreservesExistingYOLOOnEntry(t *testing.T) {
	t.Parallel()
	u, ws := newPlanUI(t, "sess-1")
	u.mode = uiInputModeCode
	ws.yolo = true
	applyModeSwitchMsg(u, u.toggleInputMode())
	require.Equal(t, uiInputModePlan, u.mode)
	require.True(t, ws.yolo)
}

func TestSwitchPlanToYolo(t *testing.T) {
	t.Parallel()
	for _, carriedYolo := range []bool{false, true} {
		u, ws := newPlanUI(t, "sess-1")
		ws.yolo = carriedYolo
		u.cycleYolo = carriedYolo

		applyModeSwitchMsg(u, u.switchPlanToYolo())
		require.Equal(t, uiInputModeCode, u.mode, "activating YOLO leaves plan mode")
		require.True(t, ws.yolo, "YOLO ends up enabled regardless of the carried state")
		require.False(t, u.cycleYolo, "explicit activation must not be undone by the Shift+Tab cycle")
		require.Equal(t, config.AgentCoder, ws.setMainCalledWith)
	}
}
