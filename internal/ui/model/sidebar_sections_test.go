package model

import (
	"image"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// sidebarWorkspace is a workspace stub whose config can carry MCP servers.
type sidebarWorkspace struct {
	prismWorkspace
	cfg config.Config
}

func (w *sidebarWorkspace) Config() *config.Config { return &w.cfg }

// newSidebarTestUI builds a UI with a laid-out sidebar, primed for
// updateSidebarScrollState.
func newSidebarTestUI() (*UI, *sidebarWorkspace) {
	ws := &sidebarWorkspace{cfg: config.Config{Options: &config.Options{}}}
	m := newPrismTestUI()
	m.com = common.DefaultCommon(ws)
	m.status = NewStatus(m.com, nil)
	m.chat = NewChat(m.com, config.ScrollbarDefault)
	m.session = &session.Session{ID: "s1", Title: "a session"}
	m.sidebarLogo = "logo"
	m.layout.sidebar = image.Rect(0, 0, 32, 40)
	return m, ws
}

// sidebarText renders the sidebar content the way the draw path sees it.
func sidebarText(m *UI) string {
	m.updateSidebarScrollState()
	return ansi.Strip(m.sidebarContent)
}

// TestSidebarHidesEmptyResourceSections: the sidebar only carries the
// resource blocks that have something to report. An idle session shows no
// empty "None" blocks at all.
func TestSidebarHidesEmptyResourceSections(t *testing.T) {
	m, ws := newSidebarTestUI()

	content := sidebarText(m)
	require.NotContains(t, content, "Modified Files")
	require.NotContains(t, content, "LSPs")
	require.NotContains(t, content, "MCPs")
	require.NotContains(t, content, "None")
	require.Contains(t, content, "a session", "the session header stays")

	// A tracked file with no diff is not a change either.
	m.sessionFiles = []SessionFile{{FirstVersion: history.File{Path: "unchanged.go"}}}
	require.NotContains(t, sidebarText(m), "Modified Files")

	m.sessionFiles = []SessionFile{{FirstVersion: history.File{Path: "changed.go"}, Additions: 3}}
	content = sidebarText(m)
	require.Contains(t, content, "Modified Files")
	require.Contains(t, content, "changed.go")
	require.NotContains(t, content, "LSPs")
	require.NotContains(t, content, "MCPs")

	m.lspStates = map[string]workspace.LSPClientInfo{"gopls": {Name: "gopls"}}
	content = sidebarText(m)
	require.Contains(t, content, "LSPs")
	require.Contains(t, content, "gopls")
	require.NotContains(t, content, "MCPs")

	ws.cfg.MCP = config.MCPs{"github": {}}
	m.mcpStates = map[string]mcp.ClientInfo{"github": {Name: "github", State: mcp.StateConnected}}
	content = sidebarText(m)
	require.Contains(t, content, "MCPs")
	require.Contains(t, content, "github")

	// Sections are still separated by exactly one blank line, with no blank
	// line left dangling at the end.
	require.NotContains(t, content, "\n\n\n")
	require.Equal(t, strings.TrimRight(content, "\n"), content)
}
