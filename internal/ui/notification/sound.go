package notification

import (
	"os"
	"runtime"
)

// Environment variables that override the alert sound for each notification
// outcome. They are read from the resolved config env (the "env" map in
// crush.json) first, then the process environment. Setting one to an empty
// string disables sound for that outcome. Because they are plain environment
// variables, mainline Crush ignores them harmlessly.
const (
	EnvSoundSuccess = "CRUSH_NOTIFICATION_SOUND_SUCCESS"
	EnvSoundError   = "CRUSH_NOTIFICATION_SOUND_ERROR"
	EnvSoundInfo    = "CRUSH_NOTIFICATION_SOUND_INFO"
)

// EnvKeyForKind returns the environment variable that overrides the alert sound
// for kind.
func EnvKeyForKind(kind Kind) string {
	switch normalizeKind(kind) {
	case KindError:
		return EnvSoundError
	case KindSuccess:
		return EnvSoundSuccess
	default:
		return EnvSoundInfo
	}
}

// SoundCommand resolves the shell command that plays the alert for a
// notification of the given kind. It consults the outcome's environment
// variable through lookup; when unset, a platform default is used. An
// explicitly empty value disables sound for that outcome, and an empty return
// means nothing should be played. A nil lookup falls back to the process
// environment.
func SoundCommand(kind Kind, lookup func(string) (string, bool)) string {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if value, ok := lookup(EnvKeyForKind(kind)); ok {
		return value
	}
	return defaultSoundCommand(normalizeKind(kind))
}

func normalizeKind(kind Kind) Kind {
	if kind == "" {
		return KindInfo
	}
	return kind
}

// defaultSoundCommand is the built-in fallback per platform. macOS ships a
// stable set of system sounds, so it plays a distinct file per outcome for an
// audible difference between success and failure. Everywhere else falls back
// to terminal bell patterns, which need no external tools.
func defaultSoundCommand(kind Kind) string {
	if runtime.GOOS == "darwin" {
		switch kind {
		case KindError:
			return "afplay /System/Library/Sounds/Basso.aiff"
		case KindSuccess:
			return "afplay /System/Library/Sounds/Glass.aiff"
		default:
			return "afplay /System/Library/Sounds/Tink.aiff"
		}
	}
	if kind == KindError {
		return `printf '\a\a\a'`
	}
	return `printf '\a'`
}
