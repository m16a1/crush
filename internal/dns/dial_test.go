package dns

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func startListener(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	return ln.Addr().String()
}

func TestDialWithFallbackFallsThroughToAWorkingNameserver(t *testing.T) {
	t.Parallel()

	good := startListener(t)
	dial := dialWithFallback([]string{"127.0.0.1:1", good})

	conn, err := dial(t.Context(), "tcp", "")
	require.NoError(t, err, "a dead first nameserver must not stop the fallback")
	require.NotNil(t, conn)
	require.NoError(t, conn.Close())
}

func TestDialWithFallbackUsesTheFirstNameserverWhenItWorks(t *testing.T) {
	t.Parallel()

	first := startListener(t)
	dial := dialWithFallback([]string{first, "127.0.0.1:1"})

	conn, err := dial(t.Context(), "tcp", "")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestDialWithFallbackReportsTheLastFailureWhenAllFail(t *testing.T) {
	t.Parallel()

	dial := dialWithFallback([]string{"127.0.0.1:1", "127.0.0.1:2"})

	conn, err := dial(t.Context(), "tcp", "")
	require.Error(t, err)
	require.Nil(t, conn)
}

func TestDialWithFallbackHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dial := dialWithFallback([]string{"127.0.0.1:1"})

	_, err := dial(ctx, "tcp", "")
	require.Error(t, err)
}
