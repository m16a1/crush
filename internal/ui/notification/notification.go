// Package notification provides desktop notification support for the UI.
//
// This package supports multiple notification backends:
//   - NativeBackend: Uses the native OS notification system (macOS, Windows, Linux)
//   - OSCBackend: Uses OSC escape sequences with automatic protocol detection.
//     Prefers OSC 99 (modern standard with rich notifications) if supported,
//     falling back to OSC 777 (urxvt extension, widely supported). Used for SSH sessions.
//   - BellBackend: Triggers the terminal bell character (\x07), causing an audible
//     beep or visual flash. Works in virtually all terminals but provides no message text.
//   - NoopBackend: A no-op backend that silently discards notifications. Used when
//     notifications are disabled or no suitable backend is available.
//
// Alert sounds are independent of the visual backend: [SoundCommand] resolves a
// shell command per outcome (success/error/info) so a failure can sound
// different from a success, and users can override or disable each one.
//
// Backend selection is based on terminal capabilities, environment, and user config:
//   - Users can explicitly set notifications in crushrc or crush.json
//     (auto/native/osc/bell/disabled)
//   - Auto mode: SSH sessions use OSC backend (auto-detects OSC 99 vs 777)
//   - Auto mode: Local sessions use OSC 99 when the terminal supports it and
//     otherwise the native OS notifications (macOS included)
//   - Notifications fire regardless of whether the terminal window is focused;
//     only "disabled" suppresses them
package notification

import tea "charm.land/bubbletea/v2"

// Kind classifies a notification by outcome. It lets backends and sound
// selection distinguish a successful turn from a failure without parsing
// the title or message text.
type Kind string

const (
	// KindInfo is the default for notifications with no stronger outcome,
	// such as permission and question prompts.
	KindInfo Kind = "info"
	// KindSuccess marks a turn that completed without error.
	KindSuccess Kind = "success"
	// KindError marks a turn that terminated with an error.
	KindError Kind = "error"
)

// Notification represents a desktop notification request.
type Notification struct {
	Title   string
	Message string
	// Kind classifies the outcome. The empty value is treated as KindInfo.
	Kind Kind
}

// Backend defines the interface for sending desktop notifications.
// Implementations return a tea.Cmd that performs the notification, allowing
// each backend to choose between synchronous (native OS) and asynchronous
// (terminal escape sequences) delivery. Policy decisions (config checks,
// focus state) are handled by the caller.
type Backend interface {
	Send(n Notification) tea.Cmd
}
