package notification

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeLookup builds a lookup function over the given map; unset keys report
// false, matching os.LookupEnv semantics.
func fakeLookup(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}
}

func TestSoundCommandEnvOverrideWins(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup(map[string]string{
		EnvSoundSuccess: "play-success.sh",
		EnvSoundError:   "play-error.sh",
	})

	require.Equal(t, "play-success.sh", SoundCommand(KindSuccess, lookup))
	require.Equal(t, "play-error.sh", SoundCommand(KindError, lookup))
}

func TestSoundCommandEmitsExpectedEnvKeys(t *testing.T) {
	t.Parallel()

	require.Equal(t, EnvSoundSuccess, EnvKeyForKind(KindSuccess))
	require.Equal(t, EnvSoundError, EnvKeyForKind(KindError))
	require.Equal(t, EnvSoundInfo, EnvKeyForKind(KindInfo))
	require.Equal(t, EnvSoundInfo, EnvKeyForKind(""), "empty kind maps to info")
}

func TestSoundCommandExplicitEmptyDisablesOutcome(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup(map[string]string{EnvSoundError: ""})

	require.Empty(t, SoundCommand(KindError, lookup))
	require.NotEmpty(t, SoundCommand(KindSuccess, lookup), "untouched outcomes keep the default")
}

func TestSoundCommandDefaultsDifferPerOutcome(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup(nil)
	success := SoundCommand(KindSuccess, lookup)
	failure := SoundCommand(KindError, lookup)
	info := SoundCommand(KindInfo, lookup)

	require.NotEmpty(t, success)
	require.NotEmpty(t, failure)
	require.NotEmpty(t, info)
	require.NotEqual(t, success, failure)
}

func TestSoundCommandDarwinUsesSystemSounds(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific default")
	}

	lookup := fakeLookup(nil)
	require.Contains(t, SoundCommand(KindSuccess, lookup), "afplay")
	require.Contains(t, SoundCommand(KindError, lookup), "afplay")
}

func TestSoundCommandNonDarwinUsesBell(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "darwin" {
		t.Skip("non-macOS default")
	}

	lookup := fakeLookup(nil)
	require.Contains(t, SoundCommand(KindSuccess, lookup), `\a`)
	require.Contains(t, SoundCommand(KindError, lookup), `\a\a\a`)
}
