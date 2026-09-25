package herdr

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// socketRecorder is a minimal stand-in for a herdr pane socket. It accepts
// newline-delimited JSON-RPC requests and publishes them on a channel.
type socketRecorder struct {
	path string
	msgs chan reportRequest
}

func newSocketRecorder(t *testing.T) *socketRecorder {
	t.Helper()
	// macOS caps Unix socket paths at ~104 bytes, so the socket goes in a
	// short /tmp directory rather than the long per-test temp dir.
	dir, err := os.MkdirTemp("/tmp", "hs")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	r := &socketRecorder{path: path, msgs: make(chan reportRequest, 32)}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					var req reportRequest
					if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
						continue
					}
					r.msgs <- req
				}
			}()
		}
	}()
	return r
}

func (r *socketRecorder) next(t *testing.T) reportRequest {
	t.Helper()
	select {
	case req := <-r.msgs:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a herdr request")
		return reportRequest{}
	}
}

func (r *socketRecorder) expectNone(t *testing.T) {
	t.Helper()
	select {
	case req := <-r.msgs:
		t.Fatalf("unexpected request: %+v", req)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestClientReportsStateOverAUnixSocket(t *testing.T) {
	rec := newSocketRecorder(t)

	c := &Client{
		socketPath: rec.path,
		paneID:     "pane-1",
		state:      stateIdle,
		seq:        1,
		snd:        newUnixSender(rec.path),
	}
	c.SetSessionID("sess-1")

	c.HandleEvent(AssistantMessage{SessionID: "sess-1"})
	working := rec.next(t)
	require.Equal(t, "pane.report_agent", working.Method)
	require.Equal(t, stateWorking, working.Params.State)
	require.Equal(t, "pane-1", working.Params.PaneID)
	require.Equal(t, "sess-1", working.Params.AgentSessionID)

	c.HandleEvent(PermissionRequested{})
	blocked := rec.next(t)
	require.Equal(t, stateBlocked, blocked.Params.State)

	c.HandleEvent(PermissionResolved{})
	resolved := rec.next(t)
	require.Equal(t, stateWorking, resolved.Params.State)

	c.HandleEvent(RunComplete{SessionID: "sess-1"})
	idle := rec.next(t)
	require.Equal(t, stateIdle, idle.Params.State)

	require.Greater(t, working.Params.Seq, uint64(1))

	c.Close()
	release := rec.next(t)
	require.Equal(t, "pane.release_agent", release.Method)
	require.Empty(t, release.Params.State)
}

func TestRegisterInitialSendsIdle(t *testing.T) {
	rec := newSocketRecorder(t)

	c := &Client{
		socketPath: rec.path,
		paneID:     "pane-2",
		state:      stateIdle,
		snd:        newUnixSender(rec.path),
	}
	c.registerInitial()

	req := rec.next(t)
	require.Equal(t, "pane.report_agent", req.Method)
	require.Equal(t, stateIdle, req.Params.State)
	require.Contains(t, req.ID, "crush:init:")

	c.Close()
}

func TestReportLockedDeduplicates(t *testing.T) {
	rec := newSocketRecorder(t)

	c := &Client{
		socketPath: rec.path,
		paneID:     "pane-3",
		state:      stateWorking,
		snd:        newUnixSender(rec.path),
	}
	require.NoError(t, c.snd.send(reportRequest{}))
	rec.next(t)

	// Reporting the same state must not emit anything.
	c.HandleEvent(PermissionResolved{})
	rec.expectNone(t)

	c.snd.close()
}

func TestDialSendToMissingSocketFails(t *testing.T) {
	err := dialSend(filepath.Join(t.TempDir(), "nope.sock"), reportRequest{ID: "x"})
	require.Error(t, err)
}

func TestReleaseAgentSurvivesAMissingSocket(t *testing.T) {
	c := &Client{
		socketPath: filepath.Join(t.TempDir(), "nope.sock"),
		paneID:     "pane-4",
		snd:        newUnixSender(filepath.Join(t.TempDir(), "nope.sock")),
	}

	require.NotPanics(t, c.releaseAgent)
	require.NotPanics(t, c.Close)
}

func TestNilClientMethodsAreSafe(t *testing.T) {
	var c *Client
	require.NotPanics(t, func() {
		c.HandleEvent(AssistantMessage{})
		c.SetSessionID("x")
		c.Close()
		c.registerInitial()
	})
}

func TestBridgeLocalIgnoresNilClient(t *testing.T) {
	require.NotPanics(t, func() {
		BridgeLocal(t.Context(), nil, BridgeSources{})
	})
}
