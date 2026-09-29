package dialog

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

func TestCommandsOfferNotificationSoundToggle(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	com := &common.Common{
		Workspace: &commandsWorkspace{cfg: config.Config{Options: &config.Options{TUI: &config.TUIOptions{}}}},
		Styles:    &sty,
	}

	tests := []struct {
		name  string
		muted bool
		want  string
	}{
		{name: "unmuted offers disable", muted: false, want: "Disable Notification Sounds"},
		{name: "muted offers enable", muted: true, want: "Enable Notification Sounds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, err := NewCommands(com, "s1", true, false, false, tt.muted, nil, nil)
			require.NoError(t, err)

			var found bool
			for _, it := range d.list.FilteredItems() {
				ci, ok := it.(*CommandItem)
				if !ok || ci == nil || ci.id != "toggle_sounds" {
					continue
				}
				require.Equal(t, tt.want, ci.title)
				require.IsType(t, ActionToggleNotificationSounds{}, ci.action)
				found = true
			}
			require.True(t, found, "toggle_sounds command must be present")
		})
	}
}
