package dialog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
)

// PlanID is the identifier for the plan screen dialog.
const PlanID = "plan"

// planStatsRefreshThrottle bounds how often the plan screen recomputes
// per-child statistics while the user navigates.
const planStatsRefreshThrottle = time.Second

// planNodeStats is the per-node derived data shown beside the plan state.
type planNodeStats struct {
	sessionTitle string
	cost         float64
	avgTPS       float64 // output tokens per second across the session's turns
	hasSession   bool
}

// Plan is the full-screen plan view: every node of the current
// session's orchestration plan, its live state, and per-worker
// statistics. Enter opens the selected node's child session.
type Plan struct {
	com       *common.Common
	sessionID string

	plan    *agent.Plan
	planErr error
	stats   map[string]planNodeStats
	statsAt time.Time

	selected int
	scroll   int

	keyMap struct {
		Close   key.Binding
		Up      key.Binding
		Down    key.Binding
		Select  key.Binding
		Refresh key.Binding
	}
}

var _ Dialog = (*Plan)(nil)

// NewPlan creates the plan screen for the given session. A missing plan
// is not an error: the screen renders an explanatory empty state.
func NewPlan(com *common.Common, sessionID string) *Plan {
	p := &Plan{
		com:       com,
		sessionID: sessionID,
		stats:     map[string]planNodeStats{},
	}
	p.keyMap.Close = key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "close"),
	)
	p.keyMap.Up = key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "previous node"),
	)
	p.keyMap.Down = key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "next node"),
	)
	p.keyMap.Select = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "open worker session"),
	)
	p.keyMap.Refresh = key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	)
	p.reloadPlan()
	p.refreshStats(true)
	return p
}

// ID implements Dialog.
func (p *Plan) ID() string { return PlanID }

// reloadPlan re-reads the persisted plan from disk so the screen
// reflects the Supervisor's latest state.
func (p *Plan) reloadPlan() {
	dataDir := ""
	if cfg := p.com.Config(); cfg != nil && cfg.Options != nil {
		dataDir = cfg.Options.DataDirectory
	}
	p.plan, p.planErr = agent.LoadPlanFile(dataDir, p.sessionID)
	if p.plan != nil && p.selected >= len(p.plan.Nodes) {
		p.selected = max(0, len(p.plan.Nodes)-1)
	}
}

// refreshStats recomputes per-node worker statistics: session cost and
// average output tokens per second. Throttled unless forced.
func (p *Plan) refreshStats(force bool) {
	if p.plan == nil {
		return
	}
	if !force && time.Since(p.statsAt) < planStatsRefreshThrottle {
		return
	}
	p.statsAt = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, n := range p.plan.Nodes {
		if n.ChildSessionID == "" {
			continue
		}
		st := planNodeStats{}
		if sess, err := p.com.Workspace.GetSession(ctx, n.ChildSessionID); err == nil {
			st.hasSession = true
			st.sessionTitle = sess.Title
			st.cost = sess.Cost
		}
		if msgs, err := p.com.Workspace.ListMessages(ctx, n.ChildSessionID); err == nil {
			st.avgTPS = avgTokensPerSecond(msgs)
		}
		p.stats[n.ID] = st
	}
}

// avgTokensPerSecond returns total completion tokens divided by total
// generation time across a session's finished assistant messages. It
// is a session-level throughput figure, not a per-token guarantee.
func avgTokensPerSecond(msgs []message.Message) float64 {
	var (
		tokens     int64
		secondsSum float64
	)
	for i := range msgs {
		m := &msgs[i]
		if m.Role != message.Assistant {
			continue
		}
		finish := m.FinishPart()
		if finish == nil || finish.CompletionTokens <= 0 || finish.Time <= 0 {
			continue
		}
		start := m.CreatedAt
		if rc := m.ReasoningContent(); rc.StartedAt > 0 && rc.StartedAt < start {
			start = rc.StartedAt
		}
		dur := finish.Time - start
		if dur <= 0 {
			continue
		}
		tokens += finish.CompletionTokens
		secondsSum += float64(dur)
	}
	if secondsSum <= 0 {
		return 0
	}
	return float64(tokens) / secondsSum
}

// HandleMsg implements Dialog.
func (p *Plan) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		p.reloadPlan()
		switch {
		case key.Matches(msg, p.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, p.keyMap.Refresh):
			p.refreshStats(true)
		case key.Matches(msg, p.keyMap.Up):
			if p.plan != nil && len(p.plan.Nodes) > 0 {
				p.selected = (p.selected - 1 + len(p.plan.Nodes)) % len(p.plan.Nodes)
			}
		case key.Matches(msg, p.keyMap.Down):
			if p.plan != nil && len(p.plan.Nodes) > 0 {
				p.selected = (p.selected + 1) % len(p.plan.Nodes)
			}
		case key.Matches(msg, p.keyMap.Select):
			if p.plan == nil || len(p.plan.Nodes) == 0 {
				return nil
			}
			n := p.plan.Nodes[p.selected]
			if n.ChildSessionID == "" {
				return ActionCmd{util.CmdHandler(util.NewInfoMsg("This node has not run yet - no worker session to open"))}
			}
			return ActionSelectSession{Session: session.Session{ID: n.ChildSessionID}}
		default:
			p.refreshStats(false)
		}
	}
	return nil
}

// Draw implements Dialog. The plan screen renders nearly full-screen.
func (p *Plan) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := p.com.Styles

	width := max(0, min(area.Dx()-4, 110))
	height := max(0, min(area.Dy()-2, 42))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	rc := NewRenderContext(t, width)
	rc.Title = "Plan"
	if p.plan != nil && p.plan.Paused {
		rc.TitleInfo = " [PAUSED]"
	}

	switch {
	case p.planErr != nil:
		rc.AddPart(t.Dialog.PrimaryText.Render("Failed to load the plan: " + p.planErr.Error()))
	case p.plan == nil || len(p.plan.Nodes) == 0:
		rc.AddPart(t.Dialog.SecondaryText.Render("No plan for this session yet."))
		rc.AddPart(t.Dialog.SecondaryText.Render(
			"When the agent orchestrates work (options.orchestration), its nodes and workers appear here."))
	default:
		rc.AddPart(p.renderHeader(t, innerWidth))
		rc.AddPart(p.renderNodes(t, innerWidth, height))
	}
	rc.Help = renderPlanHelp(t, innerWidth)

	DrawCenter(scr, area, rc.Render())
	return nil
}

// renderPlanHelp renders the key hints footer for the plan screen.
func renderPlanHelp(t *styles.Styles, innerWidth int) string {
	hint := func(k, d string) string {
		return t.Dialog.Help.ShortKey.Render(k) + " " + t.Dialog.Help.ShortDesc.Render(d)
	}
	line := hint("↑↓", "select") + "  " +
		hint("enter", "open worker session") + "  " +
		hint("r", "refresh") + "  " +
		hint("esc", "close")
	return t.Dialog.HelpView.
		Width(innerWidth).
		Render(ansi.Truncate(line, innerWidth, "…"))
}

// renderHeader renders the plan-level line: limits, spend, counts.
func (p *Plan) renderHeader(t *styles.Styles, innerWidth int) string {
	spent := 0.0
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if sess, err := p.com.Workspace.GetSession(ctx, p.sessionID); err == nil {
		spent = sess.Cost
	}
	running, verified, stalled := 0, 0, 0
	for _, n := range p.plan.Nodes {
		switch n.State {
		case agent.NodeRunning:
			running++
		case agent.NodeVerified:
			verified++
		case agent.NodeStalled:
			stalled++
		}
	}
	line := fmt.Sprintf("max_parallel %d   budget $%.2f   spent $%.2f   running %d   verified %d   stalled %d   nodes %d",
		p.plan.MaxParallelOrDefault(), p.plan.BudgetUSD, spent,
		running, verified, stalled, len(p.plan.Nodes))
	return t.Dialog.SecondaryText.
		Width(innerWidth).
		Render(ansi.Truncate(line, innerWidth, "…"))
}

// renderNodes renders the selectable node rows in a simple viewport
// sized to the available height.
func (p *Plan) renderNodes(t *styles.Styles, innerWidth, height int) string {
	rows := make([]string, 0, len(p.plan.Nodes))
	for i, n := range p.plan.Nodes {
		rows = append(rows, p.renderNodeRow(t, innerWidth, i, n))
	}
	if len(rows) == 0 {
		return ""
	}

	visible := max(1, height-6)
	p.clampScroll(visible)

	window := rows[p.scroll:min(p.scroll+visible, len(rows))]
	lines := make([]string, 0, len(window)+4)
	lines = append(lines, window...)

	if sel := p.plan.Nodes[p.selected]; sel != nil {
		lines = append(lines, "", p.renderNodeDetail(t, innerWidth, sel))
	}
	return strings.Join(lines, "\n")
}

// clampScroll keeps the selected row inside the visible window.
func (p *Plan) clampScroll(visible int) {
	if p.selected < p.scroll {
		p.scroll = p.selected
	}
	if p.selected >= p.scroll+visible {
		p.scroll = p.selected - visible + 1
	}
	if p.plan != nil {
		p.scroll = max(0, min(p.scroll, len(p.plan.Nodes)-visible))
	}
}

// renderNodeRow renders one node line: badge, id, state, agent, stats.
func (p *Plan) renderNodeRow(t *styles.Styles, innerWidth, index int, n *agent.PlanNode) string {
	symbol, style := planStateBadge(t, n.State)

	base := t.Dialog.NormalItem
	if index == p.selected {
		base = t.Dialog.SelectedItem
	}

	id := fmt.Sprintf("%-12s", ansi.Truncate(n.ID, 12, "…"))
	title := ansi.Truncate(n.Title, max(8, innerWidth-46), "…")
	line := fmt.Sprintf("%s %s %-9s %s", symbol, id, n.State, title)

	// Right-hand info column: worker stats when a child session exists.
	info := ""
	if n.Agent == "supervisor" {
		info = "supervisor"
	} else if n.Agent == "task" {
		info = "task"
	}
	if st, ok := p.stats[n.ID]; ok && st.hasSession {
		tps := "-"
		if st.avgTPS > 0 {
			tps = fmt.Sprintf("%.0f t/s", st.avgTPS)
		}
		info = fmt.Sprintf("$%.2f  %s", st.cost, tps)
	}
	if info != "" {
		line += "  " + info
	}
	line = ansi.Truncate(line, innerWidth, "…")
	return base.Render(style.Render(symbol) + strings.TrimPrefix(line, symbol))
}

// renderNodeDetail renders the selected node's extra information.
func (p *Plan) renderNodeDetail(t *styles.Styles, innerWidth int, n *agent.PlanNode) string {
	var lines []string
	add := func(format string, args ...any) {
		lines = append(lines, ansi.Truncate(fmt.Sprintf(format, args...), innerWidth, "…"))
	}
	if n.Agent == "supervisor" {
		add("sub-plan supervisor - completes when its own plan completes")
	}
	if len(n.DependsOn) > 0 {
		add("depends on: %s", strings.Join(n.DependsOn, ", "))
	}
	if len(n.Files) > 0 {
		add("files: %s", strings.Join(n.Files, ", "))
	}
	add("attempts %d   restarts %d/%d   priority %d", n.Attempts, n.Restarts, n.MaxRestartsOrDefault(), n.Priority)
	if n.LastResult != "" {
		add("claim: %s", planFirstLine(n.LastResult))
	}
	if n.LastEvidence != "" {
		add("evidence: %s", planFirstLine(n.LastEvidence))
	}
	if st, ok := p.stats[n.ID]; ok && st.sessionTitle != "" {
		add("session: %s", st.sessionTitle)
	}
	return t.Dialog.SecondaryText.Render(strings.Join(lines, "\n"))
}

// planStateBadge maps a node state to a symbol and a themed style.
func planStateBadge(t *styles.Styles, state agent.PlanNodeState) (string, lipgloss.Style) {
	switch state {
	case agent.NodeVerified:
		return "✓", t.Tool.JobIconSuccess
	case agent.NodeStalled:
		return "×", t.Tool.JobIconError
	case agent.NodeRunning:
		return "●", t.Tool.JobToolName
	case agent.NodeVerifying:
		return "…", t.Tool.JobIconPending
	default:
		return "·", t.Dialog.SecondaryText
	}
}

// planFirstLine truncates a claim/evidence string to one short line.
func planFirstLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}
