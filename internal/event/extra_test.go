package event

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsEven(t *testing.T) {
	t.Parallel()

	require.True(t, isEven(0))
	require.True(t, isEven(2))
	require.False(t, isEven(1))
	require.False(t, isEven(3))
}

func TestHashStringIsDeterministicHex(t *testing.T) {
	t.Parallel()

	got := hashString("aa:bb:cc:dd:ee:ff")
	require.Len(t, got, 64)
	_, err := hex.DecodeString(got)
	require.NoError(t, err)
	require.Equal(t, got, hashString("aa:bb:cc:dd:ee:ff"))
	require.NotEqual(t, got, hashString("11:22:33:44:55:66"))
}

func TestGetMacAddr(t *testing.T) {
	t.Parallel()

	addr, err := getMacAddr()
	if err != nil {
		require.Empty(t, addr)
		return
	}
	require.NotEmpty(t, addr)
}

func TestGetDistinctIdIsNonEmpty(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, getDistinctId())
}

// TestNoopTelemetryWhenClientIsUnset exercises the telemetry entry points
// with no PostHog client configured, which is how they behave whenever
// telemetry has not been initialized. They must all be no-ops.
func TestNoopTelemetryWhenClientIsUnset(t *testing.T) {
	original := client
	t.Cleanup(func() { client = original })
	client = nil

	AppInitialized()
	AppExited()
	SessionCreated()
	SessionDeleted()
	SessionSwitched()
	FilePickerOpened()
	PromptSent("model", "x")
	PromptResponded("tokens", 1)
	TokensUsed("count", 2)
	StatsViewed()
	SessionListed(true)
	SessionShown(true)
	SessionLastShown(true)
	SessionDeletedCommand(true)
	SessionRenamed(true)
	Flush()
}

func TestGetIDReturnsDistinctID(t *testing.T) {
	original := distinctId
	t.Cleanup(func() { distinctId = original })

	distinctId = "machine-1"
	require.Equal(t, "machine-1", GetID())
}

func TestAliasIsANoopWithoutAClient(t *testing.T) {
	originalClient := client
	originalID := distinctId
	t.Cleanup(func() {
		client = originalClient
		distinctId = originalID
	})

	client = nil
	distinctId = "machine-1"
	Alias("user-1")
}

func TestAliasSkipsUnknownOrEmptyIdentity(t *testing.T) {
	originalClient := client
	originalID := distinctId
	t.Cleanup(func() {
		client = originalClient
		distinctId = originalID
	})

	// A non-nil client would make the fallback/empty guards the only thing
	// standing between the caller and a real enqueue request.
	client = nil
	distinctId = fallbackId
	Alias("user-1")

	distinctId = ""
	Alias("user-1")

	distinctId = "machine-1"
	Alias("")
}

func TestContinueSetters(t *testing.T) {
	originalByID := baseProps[continueSessionByIDAttrName]
	originalLast := baseProps[continueLastSessionAttrName]
	t.Cleanup(func() {
		baseProps = baseProps.
			Set(continueSessionByIDAttrName, originalByID).
			Set(continueLastSessionAttrName, originalLast)
	})

	SetContinueBySessionID(true)
	require.Equal(t, true, baseProps[continueSessionByIDAttrName])

	SetContinueLastSession(true)
	require.Equal(t, true, baseProps[continueLastSessionAttrName])
}

func TestLoggerDelegatesToSlog(t *testing.T) {
	t.Parallel()

	// These must not panic; they are here to keep the posthog.Logger
	// implementation honest.
	l := logger{}
	l.Debugf("debug %d", 1)
	l.Logf("log %s", "x")
	l.Warnf("warn")
	l.Errorf("error %v", "y")
}
