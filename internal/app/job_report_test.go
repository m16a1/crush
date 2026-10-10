package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

// recordingCoordinator is a minimal agent.Coordinator that records the
// prompts Run is called with. When block is non-nil, Run waits for it to
// be closed (or the context to be cancelled) before returning; a closed
// channel makes later calls return immediately.
type recordingCoordinator struct {
	mu      sync.Mutex
	prompts []string
	block   chan struct{}
}

func (c *recordingCoordinator) Run(ctx context.Context, _ string, prompt string, _ ...message.Attachment) (*fantasy.AgentResult, error) {
	c.mu.Lock()
	c.prompts = append(c.prompts, prompt)
	block := c.block
	c.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	return &fantasy.AgentResult{}, nil
}

func (c *recordingCoordinator) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.prompts...)
}

func (c *recordingCoordinator) RunAccepted(context.Context, *agent.AcceptedRun, string, string, ...message.Attachment) (*fantasy.AgentResult, error) {
	return &fantasy.AgentResult{}, nil
}
func (c *recordingCoordinator) BeginAccepted(string) *agent.AcceptedRun       { return nil }
func (c *recordingCoordinator) Cancel(string)                                 {}
func (c *recordingCoordinator) CancelAll()                                    {}
func (c *recordingCoordinator) IsBusy() bool                                  { return false }
func (c *recordingCoordinator) IsSessionBusy(string) bool                     { return false }
func (c *recordingCoordinator) QueuedPrompts(string) int                      { return 0 }
func (c *recordingCoordinator) QueuedPromptsList(string) []string             { return nil }
func (c *recordingCoordinator) ClearQueue(string)                             {}
func (c *recordingCoordinator) Summarize(context.Context, string) error       { return nil }
func (c *recordingCoordinator) Model() agent.Model                            { return agent.Model{} }
func (c *recordingCoordinator) UpdateModels(context.Context) error            { return nil }
func (c *recordingCoordinator) SetMainAgent(string) error                     { return nil }
func (c *recordingCoordinator) GenerateTitle(context.Context, string, string) {}
func (c *recordingCoordinator) RegenerateTitle(context.Context, string) error {
	return nil
}

// startReportableJob starts a background job that prints "hello" and then
// keeps running, and marks it for reporting so reportJob will deliver it.
func startReportableJob(t *testing.T, ctx context.Context) *shell.BackgroundShell {
	t.Helper()
	manager := shell.GetBackgroundShellManager()
	bgShell, err := manager.Start(ctx, t.TempDir(), nil, "echo hello && while true; do :; done", "long")
	require.NoError(t, err)
	t.Cleanup(func() { manager.Kill(bgShell.ID) })
	bgShell.SetNotifyOnDone(true)

	require.Eventually(t, func() bool {
		stdout, _, _, _ := bgShell.GetOutput()
		return strings.Contains(stdout, "hello")
	}, 5*time.Second, 10*time.Millisecond)
	return bgShell
}

func TestReportJob_PeriodicUpdateIncludesOutput(t *testing.T) {
	ctx := t.Context()
	app := NewForTest(ctx)
	t.Cleanup(app.ShutdownForTest)
	coord := &recordingCoordinator{}
	app.AgentCoordinator = coord

	bgShell := startReportableJob(t, ctx)

	app.reportJob(shell.JobEvent{
		Type:      shell.JobEventTick,
		ShellID:   bgShell.ID,
		SessionID: "session-1",
		Command:   bgShell.Command,
	}, false)

	require.Eventually(t, func() bool { return len(coord.recorded()) == 1 }, 3*time.Second, 10*time.Millisecond)
	prompt := coord.recorded()[0]
	require.Contains(t, prompt, "still running")
	require.Contains(t, prompt, "hello", "periodic update must carry the command output")
}

func TestReportJob_CoalescesPeriodicUpdates(t *testing.T) {
	ctx := t.Context()
	app := NewForTest(ctx)
	t.Cleanup(app.ShutdownForTest)
	coord := &recordingCoordinator{block: make(chan struct{})}
	app.AgentCoordinator = coord

	bgShell := startReportableJob(t, ctx)
	tick := shell.JobEvent{
		Type:      shell.JobEventTick,
		ShellID:   bgShell.ID,
		SessionID: "session-1",
		Command:   bgShell.Command,
	}

	app.reportJob(tick, false)
	require.Eventually(t, func() bool { return len(coord.recorded()) == 1 }, 3*time.Second, 10*time.Millisecond)

	// A second update while the first is still being delivered is dropped,
	// but the terminal report always goes through.
	app.reportJob(tick, false)
	app.reportJob(shell.JobEvent{
		Type:      shell.JobEventDone,
		ShellID:   bgShell.ID,
		SessionID: "session-1",
		Command:   bgShell.Command,
	}, true)
	require.Eventually(t, func() bool { return len(coord.recorded()) == 2 }, 3*time.Second, 10*time.Millisecond)

	// Once the in-flight update finishes, a new one is accepted again.
	close(coord.block)
	base := len(coord.recorded())
	require.Eventually(t, func() bool {
		app.reportJob(tick, false)
		return len(coord.recorded()) > base
	}, 3*time.Second, 20*time.Millisecond)
}
