package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestJobEventsStreamOutputAndDone verifies that a background job pushes its
// output as it runs and a single terminal event when it exits, carrying the
// owning session so the conversation can be told the job finished.
func TestJobEventsStreamOutputAndDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := SubscribeJobEvents(ctx)
	manager := newBackgroundShellManager()
	job, err := manager.StartSession(ctx, t.TempDir(), nil, "echo hello && sleep 0.2 && echo bye", "greeter", "sess-42")
	require.NoError(t, err)

	var output strings.Builder
	var done *JobEvent
	deadline := time.After(10 * time.Second)
	for done == nil {
		select {
		case ev := <-events:
			if ev.Payload.ShellID != job.ID {
				continue
			}
			switch ev.Payload.Type {
			case JobEventOutput:
				output.WriteString(ev.Payload.Chunk)
			case JobEventDone:
				done = &ev.Payload
			}
		case <-deadline:
			t.Fatal("timed out waiting for job done event")
		}
	}

	require.Contains(t, output.String(), "hello")
	require.Contains(t, output.String(), "bye")
	require.Equal(t, "sess-42", done.SessionID)
	require.Equal(t, "greeter", done.Description)
	require.Equal(t, 0, done.ExitCode)

	// The terminal event must be last: no further output may arrive for
	// this job after it, or the UI would append output to a closed job.
	select {
	case ev := <-events:
		if ev.Payload.ShellID == job.ID && ev.Payload.Type == JobEventOutput {
			t.Fatalf("output event arrived after the done event: %q", ev.Payload.Chunk)
		}
	case <-time.After(300 * time.Millisecond):
	}
}
