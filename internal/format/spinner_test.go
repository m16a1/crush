package format

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/anim"
	"github.com/stretchr/testify/require"
)

func newModel(cancel func()) model {
	return model{anim: anim.New(anim.Settings{Size: 3, Label: "thinking"}), cancel: cancel}
}

func TestInitSchedulesTheFirstFrame(t *testing.T) {
	t.Parallel()

	require.NotNil(t, newModel(func() {}).Init())
}

func TestTickAdvancesTheAnimationAndSchedulesTheNextFrame(t *testing.T) {
	t.Parallel()

	cancelled := false
	m := newModel(func() { cancelled = true })

	next, cmd := m.Update(tickMsg{})

	require.NotNil(t, cmd, "a tick must schedule the following frame")
	require.False(t, cancelled)
	require.NotEmpty(t, next.(model).View().Content, "advancing must still render a frame")
}

func TestCtrlCAndEscapeCancelAndQuit(t *testing.T) {
	t.Parallel()

	keys := map[string]tea.KeyPressMsg{
		"ctrl+c": {Code: 'c', Mod: tea.ModCtrl},
		"esc":    {Code: tea.KeyEscape},
	}

	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, name, key.String(), "key construction must match the strings Update switches on")

			cancelled := false
			m := newModel(func() { cancelled = true })

			_, cmd := m.Update(key)

			require.True(t, cancelled, "the cancel func must run so the surrounding work stops")
			require.NotNil(t, cmd, "the program must be told to quit")
		})
	}
}

func TestOtherKeysDoNothing(t *testing.T) {
	t.Parallel()

	cancelled := false
	m := newModel(func() { cancelled = true })

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})

	require.False(t, cancelled)
	require.Nil(t, cmd)
	require.Same(t, m.anim, next.(model).anim, "an unrelated key must not replace the animation")
}

func TestNewSpinnerWiresUpAProgramAndDoneChannel(t *testing.T) {
	t.Parallel()

	s := NewSpinner(t.Context(), func() {}, anim.Settings{Size: 3})

	require.NotNil(t, s.prog)
	require.NotNil(t, s.done)
}
