package keys

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// latin binds a shortcut the way Crush does, to the key on a US PC-101 layout.
func latin(keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...))
}

// cyrillicKey is a press of a key that a non-Latin layout turned into another
// letter. base is the PC-101 key the terminal reported for it.
func cyrillicKey(char rune, base rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{
		Code:     char,
		Text:     string(char),
		BaseCode: base,
	}
}

// TestNonLatinLetterMatchesItsPhysicalKey: a shortcut written for "j" must
// still fire when the layout puts another letter on that key.
func TestNonLatinLetterMatchesItsPhysicalKey(t *testing.T) {
	t.Parallel()

	down := latin("down", "ctrl+j", "j")

	require.False(t, key.Matches(cyrillicKey('о', 'j'), down), "the layout's letter is not the binding")

	require.True(t, Matches(cyrillicKey('о', 'j'), down), "the physical key is what the binding names")
	require.False(t, Matches(cyrillicKey('о', 'j'), latin("k")), "a different key is still a different key")
}

// TestNonLatinUppercaseMatchesShiftedBinding: shift plus a key on a non-Latin
// layout reaches the binding written as the shifted US key.
func TestNonLatinUppercaseMatchesShiftedBinding(t *testing.T) {
	t.Parallel()

	msg := cyrillicKey('О', 'j')
	msg.Mod = tea.ModShift

	require.True(t, Matches(msg, latin("J")))
	require.False(t, Matches(msg, latin("j")), "shift is part of what the binding asked for")
}

// TestNonLatinKeyWithoutBaseCodeDoesNotMatch: a terminal that does not report
// the physical key leaves the shortcut as it was, rather than guessing.
func TestNonLatinKeyWithoutBaseCodeDoesNotMatch(t *testing.T) {
	t.Parallel()

	require.False(t, Matches(cyrillicKey('о', 0), latin("j")))
}

// TestPunctuationIsNotRemapped: layouts move punctuation between keys, so a
// typed character must not trigger a shortcut for a different punctuation mark.
func TestPunctuationIsNotRemapped(t *testing.T) {
	t.Parallel()

	require.False(t, Matches(cyrillicKey('.', '/'), latin("/")), "typing a dot must not open the command palette")
	require.False(t, Matches(cyrillicKey(',', '/'), latin("/")))
}

// TestPlainUSKeyStillMatches: the common case is untouched.
func TestPlainUSKeyStillMatches(t *testing.T) {
	t.Parallel()

	msg := tea.KeyPressMsg{Code: 'j', Text: "j", BaseCode: 'j'}
	require.True(t, Matches(msg, latin("j")))
	require.False(t, Matches(msg, latin("k")))
}
