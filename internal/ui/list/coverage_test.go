package list

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// plainItem is a single-line test item with a settable finished state.
type plainItem struct {
	*Versioned
	body     string
	finished bool
}

func newPlainItem(body string) *plainItem {
	return &plainItem{Versioned: NewVersioned(), body: body}
}

func (p *plainItem) Render(int) string { return p.body }
func (p *plainItem) Finished() bool    { return p.finished }

func newTestList(n int) (*List, []*plainItem) {
	items := make([]*plainItem, n)
	parts := make([]Item, n)
	for i := range n {
		items[i] = newPlainItem("item-" + string(rune('a'+i)))
		parts[i] = items[i]
	}
	l := NewList(parts...)
	l.SetSize(20, 3)
	return l, items
}

func TestListAccessors(t *testing.T) {
	t.Parallel()

	l, items := newTestList(3)

	require.Equal(t, 3, l.Len())
	require.Equal(t, 20, l.Width())
	require.Equal(t, 3, l.Height())

	l.SetGap(2)
	require.Equal(t, 2, l.Gap())

	require.False(t, l.Focused())
	l.Focus()
	require.True(t, l.Focused())
	l.Blur()
	require.False(t, l.Focused())

	// Version folds change when an item mutates.
	before := l.ItemsVersion()
	items[1].Bump()
	require.NotEqual(t, before, l.ItemsVersion())
}

func TestListScrollPositionAndOffset(t *testing.T) {
	t.Parallel()

	l, _ := newTestList(5)

	idx, line := l.ScrollPosition()
	require.Equal(t, 0, idx)
	require.Equal(t, 0, line)
	require.Equal(t, 0, l.Offset())

	l.ScrollToIndex(10)
	require.Equal(t, 4, l.offsetIdx, "index clamps to last item")
	require.Equal(t, 4, l.Offset())

	l.ScrollToIndex(-5)
	require.Equal(t, 0, l.offsetIdx, "negative index clamps to zero")

	l.ScrollToBottom()
	require.Equal(t, 1, l.offsetIdx)
	require.Equal(t, 1, l.offsetLine)
	require.Equal(t, 2, l.Offset())

	l.ScrollToTop()
	require.Equal(t, 0, l.Offset())
}

func TestListSelectionBasics(t *testing.T) {
	t.Parallel()

	l, items := newTestList(3)

	require.Equal(t, -1, l.Selected())
	require.Nil(t, l.SelectedItem())

	l.SetSelected(1)
	require.Equal(t, 1, l.Selected())
	require.Equal(t, items[1], l.SelectedItem())
	require.False(t, l.IsSelectedFirst())
	require.False(t, l.IsSelectedLast())

	l.SetSelected(0)
	require.True(t, l.IsSelectedFirst())

	l.SetSelected(2)
	require.True(t, l.IsSelectedLast())

	l.SetSelected(99)
	require.Equal(t, -1, l.Selected())
	require.Nil(t, l.SelectedItem())
}

func TestListSelectNavigation(t *testing.T) {
	t.Parallel()

	l, _ := newTestList(3)

	l.SetSelected(1)
	require.True(t, l.SelectPrev())
	require.Equal(t, 0, l.Selected())
	require.False(t, l.SelectPrev(), "already at top")

	require.True(t, l.SelectNext())
	require.Equal(t, 1, l.Selected())
	l.SetSelected(2)
	require.False(t, l.SelectNext(), "already at bottom")

	require.True(t, l.SelectLast())
	require.Equal(t, 2, l.Selected())
	require.True(t, l.SelectFirst())
	require.Equal(t, 0, l.Selected())
}

func TestListSelectNavigationReverse(t *testing.T) {
	t.Parallel()

	l, _ := newTestList(3)
	l.SetReverse(true)

	l.SetSelected(1)
	require.True(t, l.SelectPrev(), "reverse moves toward higher index")
	require.Equal(t, 2, l.Selected())
	require.False(t, l.SelectPrev())

	require.True(t, l.SelectNext(), "reverse moves toward lower index")
	require.Equal(t, 1, l.Selected())
}

func TestListSelectionEmpty(t *testing.T) {
	t.Parallel()

	l := NewList()
	require.False(t, l.SelectFirst())
	require.False(t, l.SelectLast())
	require.False(t, l.WrapToStart())
	require.False(t, l.WrapToEnd())
}

func TestListWrapNavigation(t *testing.T) {
	t.Parallel()

	l, _ := newTestList(3)
	require.True(t, l.WrapToEnd())
	require.Equal(t, 2, l.Selected())
	require.True(t, l.WrapToStart())
	require.Equal(t, 0, l.Selected())

	l.SetReverse(true)
	l.WrapToStart()
	require.Equal(t, 2, l.Selected())
	l.WrapToEnd()
	require.Equal(t, 0, l.Selected())
}

func TestListSelectionInView(t *testing.T) {
	t.Parallel()

	l, _ := newTestList(5)
	l.SetSelected(0)
	require.True(t, l.SelectedItemInView())

	l.SelectFirstInView()
	require.Equal(t, 0, l.Selected())
	l.SelectLastInView()
	require.Equal(t, 2, l.Selected())

	l.SetSelected(-1)
	require.False(t, l.SelectedItemInView())
}

func TestListVisibleItemIndicesEmpty(t *testing.T) {
	t.Parallel()

	l := NewList()
	start, end := l.VisibleItemIndices()
	require.Equal(t, 0, start)
	require.Equal(t, 0, end)
}

func TestListScrollToSelected(t *testing.T) {
	t.Parallel()

	t.Run("no selection is a no-op", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestList(5)
		l.ScrollToSelected()
		require.Equal(t, 0, l.offsetIdx)
	})

	t.Run("unsized list pins selection to top", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestList(5)
		l.SetSize(20, 0)
		l.SetSelected(3)
		l.ScrollToSelected()
		require.Equal(t, 3, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})

	t.Run("selection above viewport", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestList(5)
		l.ScrollToIndex(3)
		l.SetSelected(0)
		l.ScrollToSelected()
		require.Equal(t, 0, l.offsetIdx)
	})

	t.Run("selection below viewport", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestList(5)
		l.SetSelected(4)
		l.ScrollToSelected()
		require.True(t, l.SelectedItemInView())
	})
}

func TestListScrollBy(t *testing.T) {
	t.Parallel()

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()

		l := NewList()
		l.ScrollBy(5)
		require.Equal(t, 0, l.offsetIdx)
	})

	t.Run("zero scroll", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestList(5)
		l.ScrollBy(0)
		require.Equal(t, 0, l.offsetIdx)
	})

	t.Run("down and up", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestList(5)
		l.ScrollBy(1)
		require.Equal(t, 1, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)

		l.ScrollBy(-1)
		require.Equal(t, 0, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})

	t.Run("clamps at top", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestList(5)
		l.ScrollBy(-10)
		require.Equal(t, 0, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})

	t.Run("reverse inverts direction", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestList(5)
		l.SetReverse(true)
		l.ScrollBy(-1)
		require.True(t, l.offsetIdx > 0, "reverse scroll should move content")
	})
}

func TestListItemNavigation(t *testing.T) {
	t.Parallel()

	l, items := newTestList(5)

	require.Equal(t, items[2], l.ItemAt(2))
	require.Nil(t, l.ItemAt(-1))
	require.Nil(t, l.ItemAt(99))

	idx, y := l.ItemIndexAtPosition(0, 0)
	require.Equal(t, 0, idx)
	require.Equal(t, 0, y)

	idx, y = l.ItemIndexAtPosition(0, 2)
	require.Equal(t, 2, idx)
	require.Equal(t, 0, y)

	idx, y = l.ItemIndexAtPosition(0, 5)
	require.Equal(t, -1, idx)
	require.Equal(t, -1, y)

	idx, y = l.ItemIndexAtPosition(0, -1)
	require.Equal(t, -1, idx)
	require.Equal(t, -1, y)
}

func TestListRegisterRenderCallback(t *testing.T) {
	t.Parallel()

	l, items := newTestList(3)

	var calls int
	l.RegisterRenderCallback(func(idx, selectedIdx int, item Item) Item {
		calls++
		return item
	})

	l.getItem(0)
	require.Equal(t, 1, calls)

	// A callback returning nil keeps the original item.
	l2, _ := newTestList(1)
	l2.RegisterRenderCallback(func(int, int, Item) Item { return nil })
	require.NotEmpty(t, l2.getItem(0).content)
	_ = items
}

func TestListInvalidate(t *testing.T) {
	t.Parallel()

	item := newTrackedItem("a", "alpha", false)
	l := NewList(item)
	l.SetSize(40, 10)

	_ = l.getItem(0)
	require.Equal(t, 1, item.renderHits)

	l.Invalidate(item)
	_ = l.getItem(0)
	require.Equal(t, 2, item.renderHits)

	// Invalidate of an uncached item is a no-op.
	l.Invalidate(newTrackedItem("b", "bravo", false))
}

func TestListInvalidateFrozen(t *testing.T) {
	t.Parallel()

	item := newTrackedItem("a", "alpha", true)
	l := NewList(item)
	l.SetSize(40, 10)

	_ = l.getItem(0)
	require.Equal(t, 1, item.renderHits)

	// Frozen entries are served from cache without re-rendering.
	_ = l.getItem(0)
	require.Equal(t, 1, item.renderHits)

	l.InvalidateFrozen(item)
	_ = l.getItem(0)
	require.Equal(t, 2, item.renderHits)
}

func TestSpacerItem(t *testing.T) {
	t.Parallel()

	s := NewSpacerItem(3)
	require.Equal(t, 2, s.Height)
	require.True(t, s.Finished())
	require.Equal(t, "\n\n", s.Render(0))

	// Non-positive heights clamp to zero lines.
	require.Equal(t, 0, NewSpacerItem(0).Height)
	require.Empty(t, NewSpacerItem(0).Render(0))
}

func TestListRenderReverse(t *testing.T) {
	t.Parallel()

	l, _ := newTestList(5)
	normal := l.Render()

	l.SetReverse(true)
	reversed := l.Render()

	require.NotEqual(t, normal, reversed)
	require.Equal(t, strings.Join(reverseLines(strings.Split(normal, "\n")), "\n"), reversed)
}

func reverseLines(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[len(in)-1-i] = s
	}
	return out
}
