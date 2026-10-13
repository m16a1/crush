package model

import (
	"runtime"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/notification"
)

func TestSelectNotificationBackendExplicitStyle(t *testing.T) {
	t.Parallel()

	t.Run("native", func(t *testing.T) {
		t.Parallel()
		if !notification.NativeSupported {
			t.Skip("native notifications unavailable on this platform")
		}
		cfg := &config.Config{Options: &config.Options{Notifications: "native"}}
		require.IsType(t, &notification.NativeBackend{}, selectNotificationBackend(common.Capabilities{}, cfg))
	})

	t.Run("osc", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{Options: &config.Options{Notifications: "osc"}}
		require.IsType(t, &notification.OSCBackend{}, selectNotificationBackend(common.Capabilities{}, cfg))
	})

	t.Run("bell", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{Options: &config.Options{Notifications: "bell"}}
		require.IsType(t, &notification.BellBackend{}, selectNotificationBackend(common.Capabilities{}, cfg))
	})

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{Options: &config.Options{Notifications: "disabled"}}
		require.IsType(t, notification.NoopBackend{}, selectNotificationBackend(common.Capabilities{}, cfg))
	})
}

func TestSelectNotificationBackendAutoDarwin(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skip("macOS auto selection")
	}

	t.Run("prefers OSC 99 when supported", func(t *testing.T) {
		t.Parallel()
		caps := common.Capabilities{OSC99Notifications: true}
		require.IsType(t, &notification.OSCBackend{}, selectNotificationBackend(caps, nil))
	})

	t.Run("falls back to native when OSC 99 unsupported", func(t *testing.T) {
		t.Parallel()
		if !notification.NativeSupported {
			t.Skip("native notifications unavailable on this platform")
		}
		require.IsType(t, &notification.NativeBackend{}, selectNotificationBackend(common.Capabilities{}, nil))
	})
}

func TestShouldSendNotification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		style string
		want  bool
	}{
		{"disabled suppresses", "disabled", false},
		{"auto sends regardless of focus", "auto", true},
		{"unset sends", "", true},
		{"explicit backend sends", "native", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{Options: &config.Options{Notifications: tt.style}}
			ui := newTestUIWithConfig(t, cfg)

			require.Equal(t, tt.want, ui.shouldSendNotification())
		})
	}
}

func TestNotificationSoundCmdHonorsEmptyEnv(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Options: &config.Options{},
		Env:     map[string]string{notification.EnvSoundSuccess: ""},
	}
	ui := newTestUIWithConfig(t, cfg)

	require.Nil(t, ui.notificationSoundCmd(notification.KindSuccess), "empty command must disable the outcome")
	require.NotNil(t, ui.notificationSoundCmd(notification.KindError), "absent outcome keeps the default")
}

func TestNotificationSoundsMutedGatesSound(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Options: &config.Options{}}
	ui := newTestUIWithConfig(t, cfg)

	require.NotNil(t, ui.notificationSoundCmd(notification.KindSuccess))

	ui.notificationSoundsMuted = true
	require.Nil(t, ui.notificationSoundCmd(notification.KindSuccess))
}

func TestSendNotificationRespectsDisabled(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Options: &config.Options{Notifications: "disabled"}}
	ui := newTestUIWithConfig(t, cfg)

	require.Nil(t, ui.sendNotification(notification.Notification{}))
}

// recordingNotificationBackend captures notification requests so tests can
// assert on what the UI would surface to the user.
type recordingNotificationBackend struct {
	mu   sync.Mutex
	sent []notification.Notification
}

func (b *recordingNotificationBackend) Send(n notification.Notification) tea.Cmd {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, n)
	return nil
}

func (b *recordingNotificationBackend) recorded() []notification.Notification {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]notification.Notification(nil), b.sent...)
}

// TestInternalAgentFinishedSkipsDesktopNotification pins the suppression
// rule for background-job plumbing: a turn-finished event marked Internal
// (a job-report turn, or a turn preempted by one) must not produce a
// desktop notification or sound, while a user-driven turn still does. Both
// must keep driving the busy-state refresh.
func TestInternalAgentFinishedSkipsDesktopNotification(t *testing.T) {
	t.Parallel()

	ui := newTestUIWithConfig(t, &config.Config{Options: &config.Options{}})
	backend := &recordingNotificationBackend{}
	ui.notifyBackend = backend

	cmd := ui.handleAgentNotification(notify.Notification{
		Type:      notify.TypeAgentFinished,
		SessionID: "s1",
		Internal:  true,
	})
	require.NotNil(t, cmd, "internal turns must still drive the busy-state refresh")
	require.Empty(t, backend.recorded(),
		"an internal turn completion must not notify the user")

	cmd = ui.handleAgentNotification(notify.Notification{
		Type:         notify.TypeAgentFinished,
		SessionID:    "s1",
		SessionTitle: "my session",
	})
	require.NotNil(t, cmd)
	sent := backend.recorded()
	require.Len(t, sent, 1, "a user-driven turn completion must notify the user")
	require.Equal(t, notification.KindSuccess, sent[0].Kind)
}
