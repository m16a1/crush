package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeLauncher struct {
	launched []*PlanNode
	verifies []*PlanNode
}

func (l *fakeLauncher) LaunchNode(_ *Supervisor, node *PlanNode, _ string) {
	l.launched = append(l.launched, node)
}

func (l *fakeLauncher) VerifyNode(_ *Supervisor, node *PlanNode, _ string, _ VerifyEvidence) {
	l.verifies = append(l.verifies, node)
}

func (l *fakeLauncher) launchedIDs() []string {
	var ids []string
	for _, n := range l.launched {
		ids = append(ids, n.ID)
	}
	return ids
}

func testSupervisor(t *testing.T, maxParallel int) (*Supervisor, *fakeLauncher, *[]string) {
	t.Helper()
	store := &planStore{dir: t.TempDir()}
	launcher := &fakeLauncher{}
	var wakes []string
	sup := newSupervisor(&Plan{SessionID: "main-session"}, store, supervisorHooks{
		launcher: launcher,
		wake: func(report string) {
			wakes = append(wakes, report)
		},
		maxParallel: func() int { return maxParallel },
	})
	// Timers must not fire on their own: tests flush explicitly so wake
	// content is asserted deterministically.
	sup.debounce = time.Hour
	return sup, launcher, &wakes
}

func nodeInput(id string, deps ...string) PlanNodeInput {
	return PlanNodeInput{ID: id, Title: "node " + id, Prompt: "do " + id, DependsOn: deps}
}

func TestSupervisorSchedulingRespectsDAGAndSlots(t *testing.T) {
	t.Parallel()
	sup, launcher, _ := testSupervisor(t, 2)

	require.NoError(t, sup.AddNodes([]PlanNodeInput{
		nodeInput("a"),
		nodeInput("b"),
		nodeInput("c", "a"),
	}))

	require.ElementsMatch(t, []string{"a", "b"}, launcher.launchedIDs())

	// A verified dependency unlocks its dependent once a slot frees.
	sup.NodeDone("a", 1, "done", nil, VerifyEvidence{})
	require.Contains(t, launcher.launchedIDs(), "c")
	require.Equal(t, NodeVerified, sup.find("a").State)
}

func TestSupervisorPriorityOrder(t *testing.T) {
	t.Parallel()
	sup, launcher, _ := testSupervisor(t, 1)

	low := nodeInput("low")
	high := nodeInput("high")
	high.Priority = 5
	require.NoError(t, sup.AddNodes([]PlanNodeInput{low, high}))

	require.Equal(t, []string{"high"}, launcher.launchedIDs())
}

func TestSupervisorVerifyCmdFailureStalls(t *testing.T) {
	t.Parallel()
	sup, launcher, wakes := testSupervisor(t, 2)

	in := nodeInput("a")
	in.VerifyCmd = "false"
	require.NoError(t, sup.AddNodes([]PlanNodeInput{in}))

	sup.NodeDone("a", 1, "all good", nil, VerifyEvidence{Cmd: "false", ExitCode: 1, Output: "boom"})
	require.Equal(t, NodeStalled, sup.find("a").State)

	sup.FlushWakes()
	require.Len(t, *wakes, 1)
	require.Contains(t, (*wakes)[0], "verify_cmd failed")

	// Adjudication: an update reopts the stalled node and relaunches it.
	require.NoError(t, sup.UpdateNodes([]PlanNodeInput{{ID: "a", VerifyCmd: "true"}}))
	require.Len(t, launcher.launched, 2)
	require.Equal(t, NodeRunning, sup.find("a").State)
}

func TestSupervisorVerifierChildPath(t *testing.T) {
	t.Parallel()
	sup, launcher, wakes := testSupervisor(t, 1)

	in := nodeInput("a")
	in.VerifyCriteria = "the flag works"
	require.NoError(t, sup.AddNodes([]PlanNodeInput{in}))

	sup.NodeDone("a", 1, "shipped it", nil, VerifyEvidence{})
	require.Equal(t, NodeVerifying, sup.find("a").State)
	require.Len(t, launcher.verifies, 1)
	require.Contains(t, launcher.verifies[0].LastResult, "shipped it")

	// While verifying, the slot is already free (a claim frees a slot).
	require.Equal(t, 0, len(sup.running))

	sup.ReportVerdict("a", 1, false, "flag is ignored on Windows")
	require.Equal(t, NodeStalled, sup.find("a").State)
	sup.FlushWakes()
	require.Contains(t, (*wakes)[0], "verifier rejected")

	// Retry with a fresh budget verifies on a green verdict.
	sup.RetryNodes([]string{"a"})
	sup.NodeDone("a", 2, "fixed windows", nil, VerifyEvidence{})
	sup.ReportVerdict("a", 2, true, "criteria met")
	require.Equal(t, NodeVerified, sup.find("a").State)
}

func TestSupervisorRestartBudget(t *testing.T) {
	t.Parallel()
	sup, launcher, wakes := testSupervisor(t, 1)

	require.NoError(t, sup.AddNodes([]PlanNodeInput{nodeInput("a")}))

	// First death: auto-restart within the default budget of 1.
	sup.NodeDone("a", 1, "", errors.New("stream reset"), VerifyEvidence{})
	require.Equal(t, NodeRunning, sup.find("a").State)
	require.Equal(t, 2, sup.find("a").Attempts)
	require.Len(t, launcher.launched, 2)

	// Second death: budget exhausted, node stalls for adjudication.
	sup.NodeDone("a", 2, "", errors.New("stream reset"), VerifyEvidence{})
	require.Equal(t, NodeStalled, sup.find("a").State)

	sup.FlushWakes()
	require.Contains(t, strings.Join(*wakes, "\n"), "restart budget is exhausted")
}

func TestSupervisorFileDisjointness(t *testing.T) {
	t.Parallel()
	sup, launcher, _ := testSupervisor(t, 2)

	a := nodeInput("a")
	a.Files = []string{"internal/x"}
	b := nodeInput("b")
	b.Files = []string{"internal/x/deep.go"} // overlaps a
	c := nodeInput("c")
	c.Files = []string{"internal/y"}

	require.NoError(t, sup.AddNodes([]PlanNodeInput{a, b, c}))
	require.ElementsMatch(t, []string{"a", "c"}, launcher.launchedIDs())

	require.NoError(t, sup.RemoveNodes([]string{"a"}))
	require.Contains(t, launcher.launchedIDs(), "b")
}

func TestSupervisorDeadlockEscalation(t *testing.T) {
	t.Parallel()
	sup, _, wakes := testSupervisor(t, 2)

	a := nodeInput("a")
	a.MaxRestarts = ptrInt(0)
	require.NoError(t, sup.AddNodes([]PlanNodeInput{a, nodeInput("b", "a")}))

	// a dies with no restart budget; b is blocked on a stalled dep.
	sup.NodeDone("a", 1, "", errors.New("boom"), VerifyEvidence{})
	require.Equal(t, NodeStalled, sup.find("a").State)

	sup.FlushWakes()
	report := strings.Join(*wakes, "\n")
	require.Contains(t, report, "deadlocked")
	require.Contains(t, report, "b (dependency a is stalled)")
}

func TestSupervisorCycleRejected(t *testing.T) {
	t.Parallel()
	sup, _, _ := testSupervisor(t, 2)

	err := sup.AddNodes([]PlanNodeInput{
		nodeInput("a", "b"),
		nodeInput("b", "a"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cycle")
	require.Empty(t, sup.plan.Nodes)
}

func TestSupervisorCompletionWake(t *testing.T) {
	t.Parallel()
	sup, _, wakes := testSupervisor(t, 1)

	require.NoError(t, sup.AddNodes([]PlanNodeInput{nodeInput("a")}))
	sup.NodeDone("a", 1, "done", nil, VerifyEvidence{})

	sup.FlushWakes()
	require.Contains(t, strings.Join(*wakes, "\n"), "Plan complete")
}

func TestSupervisorBudgetPausesPlan(t *testing.T) {
	t.Parallel()
	sup, _, wakes := testSupervisor(t, 2)
	spent := 0.0
	sup.hooks.spent = func() float64 { return spent }

	a := nodeInput("a")
	b := nodeInput("b")
	require.NoError(t, sup.AddNodes([]PlanNodeInput{a, b}))

	spent = 5.0
	sup.SetLimits(0, 1.0)
	require.True(t, sup.plan.Paused)
	require.Equal(t, NodePending, sup.find("a").State)
	sup.FlushWakes()
	require.Contains(t, strings.Join(*wakes, "\n"), "budget exhausted")
}

func TestSupervisorMastermindDeathRecovery(t *testing.T) {
	t.Parallel()
	sup, _, wakes := testSupervisor(t, 2)

	require.NoError(t, sup.AddNodes([]PlanNodeInput{nodeInput("a")}))

	sup.NoteTurnEnded("provider stream error")
	sup.FlushWakes()
	require.Contains(t, strings.Join(*wakes, "\n"), "ended with an error")

	// A clean turn clears the streak; repeated deaths escalate once.
	sup.NoteTurnEnded("")
	sup.NoteTurnEnded("boom")
	sup.NoteTurnEnded("boom")
	sup.NoteTurnEnded("boom")
	sup.NoteTurnEnded("boom")
	sup.FlushWakes()
	require.Contains(t, strings.Join(*wakes, "\n"), "died 4 times in a row")

	// A fifth death must not re-escalate until a clean turn resets it.
	before := len(*wakes)
	sup.NoteTurnEnded("boom")
	sup.FlushWakes()
	require.Len(t, *wakes, before)
}

func TestSupervisorPauseResume(t *testing.T) {
	t.Parallel()
	sup, launcher, _ := testSupervisor(t, 2)

	require.NoError(t, sup.AddNodes([]PlanNodeInput{nodeInput("a")}))
	sup.Pause()
	require.Equal(t, NodePending, sup.find("a").State)

	sup.Resume()
	require.Equal(t, NodeRunning, sup.find("a").State)
	require.Len(t, launcher.launched, 2)
}

func TestSupervisorStatusRendering(t *testing.T) {
	t.Parallel()
	sup, _, _ := testSupervisor(t, 1)

	require.NoError(t, sup.AddNodes([]PlanNodeInput{nodeInput("a"), nodeInput("b", "a")}))
	status := sup.Status()
	require.Contains(t, status, "[a] running")
	require.Contains(t, status, "[b] pending")
	require.Contains(t, status, "dependency a is running")
	require.Contains(t, status, "Slots: running 1/1")
}

func TestPlanStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store := &planStore{dir: t.TempDir()}

	missing, err := store.load("nope")
	require.NoError(t, err)
	require.Nil(t, missing)

	plan := &Plan{SessionID: "sess", MaxParallel: 3}
	plan.Nodes = []*PlanNode{{ID: "a", Title: "t", State: NodePending}}
	require.NoError(t, store.save(plan))

	loaded, err := store.load("sess")
	require.NoError(t, err)
	require.Equal(t, 3, loaded.MaxParallel)
	require.Equal(t, "a", loaded.Nodes[0].ID)
}

func TestSupervisorRestoresInterruptedStates(t *testing.T) {
	t.Parallel()
	plan := &Plan{SessionID: "s", Nodes: []*PlanNode{
		{ID: "a", State: NodeRunning},
		{ID: "b", State: NodeVerifying},
		{ID: "c", State: NodeVerified},
	}}
	sup := newSupervisor(plan, nil, supervisorHooks{})
	require.Equal(t, NodePending, sup.find("a").State)
	require.Equal(t, NodePending, sup.find("b").State)
	require.Equal(t, NodeVerified, sup.find("c").State)
}

func ptrInt(v int) *int { return &v }

func TestSupervisorWatchdogTimeout(t *testing.T) {
	sup, launcher, wakes := testSupervisor(t, 1)

	in := nodeInput("a")
	in.Timeout = 1
	require.NoError(t, sup.AddNodes([]PlanNodeInput{in}))

	// The fake launcher never reports back; the watchdog must fail the
	// node, which (budget 1) requeues and relaunches it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(launcher.launched) < 2 {
		time.Sleep(50 * time.Millisecond)
	}
	require.Len(t, launcher.launched, 2)
	require.Equal(t, NodeRunning, sup.find("a").State)

	sup.FlushWakes()
	require.Contains(t, strings.Join(*wakes, "\n"), "watchdog timeout")
}

func TestSupervisorUpdateCycleRollsBack(t *testing.T) {
	t.Parallel()
	sup, _, _ := testSupervisor(t, 1)

	require.NoError(t, sup.AddNodes([]PlanNodeInput{
		nodeInput("a"),
		nodeInput("b"),
		nodeInput("c"),
	}))
	// a holds the only slot; b and c are pending and updatable.

	err := sup.UpdateNodes([]PlanNodeInput{
		{ID: "b", DependsOn: []string{"c"}, Priority: 9},
		{ID: "c", DependsOn: []string{"b"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cycle")

	// The rejected update left no trace.
	require.Empty(t, sup.find("b").DependsOn)
	require.Equal(t, 0, sup.find("b").Priority)
	require.Equal(t, NodeRunning, sup.find("a").State)
}
