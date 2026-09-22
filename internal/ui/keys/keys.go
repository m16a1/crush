// Package keys matches key events against key bindings. It extends the
// bubbles key package so shortcuts keep working when the keyboard layout is
// not the US PC-101 layout the bindings are written for.
package keys

import (
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Matches reports whether a key event matches any of the bindings.
//
// A key that the active layout turned into another character also matches the
// PC-101 key it was pressed on, so a shortcut keeps working on a non-Latin
// layout while the character the layout produced is still what gets typed.
// Only letters are mapped: a layout can move punctuation onto a key whose
// shortcut would otherwise fire while the user types.
func Matches(msg tea.KeyPressMsg, bindings ...key.Binding) bool {
	if key.Matches(msg, bindings...) {
		return true
	}
	base, ok := baseLayoutKey(msg)
	if !ok {
		return false
	}
	return key.Matches(base, bindings...)
}

// baseLayoutKey returns the event as the key it was pressed on, according to
// the standard PC-101 layout. Keys with Ctrl, Alt or another command modifier
// are left alone: the terminal reports those by their physical key already.
func baseLayoutKey(msg tea.KeyPressMsg) (tea.KeyPressMsg, bool) {
	if msg.Mod.Contains(tea.ModCtrl) || msg.Mod.Contains(tea.ModAlt) ||
		msg.Mod.Contains(tea.ModMeta) || msg.Mod.Contains(tea.ModSuper) {
		return msg, false
	}
	base := msg.BaseCode
	if !isASCIILetter(base) || base == msg.Code {
		return msg, false
	}
	if msg.Mod.Contains(tea.ModShift) || unicode.IsUpper(msg.Code) {
		base = unicode.ToUpper(base)
	}
	msg.Code = base
	msg.Text = string(base)
	return msg, true
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
