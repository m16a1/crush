package list

import (
	"image"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
)

type filterItem struct {
	*plainItem
	value string
	match fuzzy.Match
}

func newFilterItem(value string) *filterItem {
	return &filterItem{plainItem: newPlainItem(value), value: value}
}

func (f *filterItem) Filter() string         { return f.value }
func (f *filterItem) SetMatch(m fuzzy.Match) { f.match = m }

func TestFilterableList(t *testing.T) {
	t.Parallel()

	a := newFilterItem("alpha")
	b := newFilterItem("bravo")
	c := newFilterItem("charlie")

	l := NewFilterableList(a, b, c)
	l.SetSize(20, 5)
	require.Equal(t, 3, l.Len())

	// Empty query returns everything and clears stale matches.
	all := l.FilteredItems()
	require.Len(t, all, 3)
	require.Equal(t, fuzzy.Match{}, a.match)

	// A query narrows the visible items.
	l.SetFilter("br")
	filtered := l.FilteredItems()
	require.Len(t, filtered, 1)
	require.Equal(t, "bravo", filtered[0].(*filterItem).Filter())

	require.NotEmpty(t, l.Render())

	// Clearing the filter restores every item.
	l.SetFilter("")
	require.Len(t, l.FilteredItems(), 3)
}

func TestFilterableListMutations(t *testing.T) {
	t.Parallel()

	a := newFilterItem("alpha")
	b := newFilterItem("bravo")

	l := NewFilterableList(a)
	require.Equal(t, 1, l.Len())

	l.AppendItems(b)
	require.Equal(t, 2, l.Len())
	require.Len(t, l.FilteredItems(), 2)

	c := newFilterItem("charlie")
	l.PrependItems(c)
	require.Equal(t, 3, l.Len())
	require.Equal(t, c, l.FilteredItems()[0])

	l.SetItems(c, a)
	require.Equal(t, 2, l.Len())
}

func TestFilterableItemsSource(t *testing.T) {
	t.Parallel()

	a := newFilterItem("alpha")
	b := newFilterItem("bravo")
	src := FilterableItemsSource{a, b}

	require.Equal(t, 2, src.Len())
	require.Equal(t, "alpha", src.String(0))
	require.Equal(t, "bravo", src.String(1))
}

type focusItem struct {
	*plainItem
	focused bool
}

func (f *focusItem) SetFocused(focused bool) { f.focused = focused }

func TestFocusedRenderCallback(t *testing.T) {
	t.Parallel()

	l, items := newTestList(2)
	cb := FocusedRenderCallback(l)

	// Non-focusable items pass through untouched.
	require.Equal(t, items[0], cb(0, 0, items[0]))

	fi := &focusItem{plainItem: newPlainItem("focus")}
	l.Focus()
	require.Equal(t, fi, cb(0, 0, fi))
	require.True(t, fi.focused)

	require.Equal(t, fi, cb(1, 0, fi))
	require.False(t, fi.focused)
}

func TestHighlight(t *testing.T) {
	t.Parallel()

	area := image.Rect(0, 0, 10, 3)

	require.NotEmpty(t, Highlight("hello", area, 0, 0, 0, 3, nil))

	// Negative start returns the content unchanged.
	require.Equal(t, "hello", Highlight("hello", area, -1, 0, 0, 0, nil))

	buf := HighlightBuffer("hello", area, 0, 0, -1, -1, nil)
	require.NotNil(t, buf)

	require.Nil(t, HighlightBuffer("hello", area, 0, -1, 0, 0, nil))
}

func TestToHighlighter(t *testing.T) {
	t.Parallel()

	h := ToHighlighter(lipgloss.NewStyle().Bold(true))
	cell := &uv.Cell{Content: "a"}
	require.NotNil(t, h(0, 0, cell))
	require.NotZero(t, cell.Style.Attrs&uv.AttrBold)

	// Nil cells are passed through.
	require.Nil(t, h(0, 0, nil))
}

func TestToStyle(t *testing.T) {
	t.Parallel()

	lg := lipgloss.NewStyle().
		Bold(true).
		Italic(true).
		Underline(true).
		Strikethrough(true).
		Faint(true).
		Blink(true).
		Reverse(true).
		Foreground(lipgloss.Color("1")).
		Background(lipgloss.Color("2"))

	sty := ToStyle(lg)

	require.NotZero(t, sty.Attrs&uv.AttrBold)
	require.NotZero(t, sty.Attrs&uv.AttrItalic)
	require.NotZero(t, sty.Attrs&uv.AttrStrikethrough)
	require.NotZero(t, sty.Attrs&uv.AttrFaint)
	require.NotZero(t, sty.Attrs&uv.AttrBlink)
	require.NotZero(t, sty.Attrs&uv.AttrReverse)
	require.Equal(t, uv.UnderlineSingle, sty.Underline)
	require.NotNil(t, sty.Fg)
	require.NotNil(t, sty.Bg)
}

func TestAdjustArea(t *testing.T) {
	t.Parallel()

	area := image.Rect(0, 0, 100, 50)
	sty := lipgloss.NewStyle().
		Margin(1, 2, 3, 4).
		Padding(1, 1, 1, 1).
		Border(lipgloss.NormalBorder())

	got := AdjustArea(area, sty)
	require.Equal(t, image.Rect(6, 3, 96, 45), got)
}
