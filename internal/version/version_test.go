package version

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionIsNeverEmpty(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, Version, "Version must always have a usable value, even without -ldflags")
}

func TestBuildIDIsSetAndStable(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, BuildID)
	require.Equal(t, BuildID, deriveBuildID(), "the build fingerprint must not change within a process")
}

func TestDerivedBuildIDIsABase36Timestamp(t *testing.T) {
	t.Parallel()

	id := deriveBuildID()
	require.NotEqual(t, "unknown", id, "the test binary's own modification time is available")
	_, err := strconv.ParseInt(id, 36, 64)
	require.NoError(t, err, "deriveBuildID must produce base36")
}
