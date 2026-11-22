package ui

import (
	"maps"
	"math"
	"math/bits"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zatxm/fego/v26"
)

// VirtualListOptions describes an indexed collection. Build is called for visible
// rows and their immediate neighbors. Store data that must survive scrolling in
// the application, keyed by the item's identity.
type VirtualListOptions struct {
	Count int
	// EstimatedHeight is used until the row's content is measured. Zero uses 36.
	EstimatedHeight float32
	// Key returns a stable, unique identifier. Nil uses the index.
	Key func(index int) int
	// Name provides the accessible row label and keyboard type-ahead text.
	Name func(index int) string
	// Headers contains the sorted indices of section headers.
	Headers     []int
	MultiSelect bool
	// Reorder moves the selected indices before the given index in the original
	// order. Update the data and call Refresh from the callback.
	Reorder func(indices []int, before int)
}

type VirtualListView struct {
	*Element
	options      VirtualListOptions
	build        func(int) *Element
	rows         map[int]*Element
	heights      map[int]float64
	sums         map[int]float64
	chosen       map[int]struct{}
	first, last  int
	endRequested bool
	dragY        float32
	lead, anchor int
	onSelection  func([]int)
	onSubmit     func(int)
	lastClick    time.Time
	lastClickKey int
}

func VirtualList(options VirtualListOptions, build func(index int) *Element) *VirtualListView {
	if build == nil {
		panic("ui: virtual list requires a row builder")
	}
	l := &VirtualListView{
		Element: element(kVirtualList).Height(240), build: build,
		rows: make(map[int]*Element), first: -1, last: -1, lead: -1, anchor: -1,
	}
	l.own().virtual = l
	l.SetOptions(options)
	return l
}

// SetOptions replaces the collection configuration and invalidates visible rows.
func (l *VirtualListView) SetOptions(o VirtualListOptions) *VirtualListView {
	if o.Count < 0 || o.Count >= math.MaxInt32 || o.EstimatedHeight < 0 ||
		math.IsNaN(float64(o.EstimatedHeight)) || math.IsInf(float64(o.EstimatedHeight), 0) {
		panic("ui: invalid virtual list dimensions")
	}
	if o.EstimatedHeight == 0 {
		o.EstimatedHeight = 36
	}
	o.Headers = slices.Clone(o.Headers)
	for i, row := range o.Headers {
		if row < 0 || row >= o.Count || (i > 0 && row <= o.Headers[i-1]) {
			panic("ui: virtual list headers must be unique sorted indices")
		}
	}
	l.options = o
	return l.Refresh()
}

func (l *VirtualListView) key(i int) int {
	if l.options.Key != nil {
		return l.options.Key(i)
	}
	return i
}

func (l *VirtualListView) header(i int) bool {
	_, ok := slices.BinarySearch(l.options.Headers, i)
	return ok
}

// Refresh rebuilds visible row contents after the model changes. Selection
// follows keys. Width changes remeasure rows without rebuilding their contents.
func (l *VirtualListView) Refresh() *VirtualListView {
	for _, row := range l.rows {
		l.detach(row)
	}
	clear(l.rows)
	clear(l.children)
	l.children = l.children[:0]
	l.heights, l.sums = nil, nil
	l.first, l.last = -1, -1
	l.lead = min(l.lead, l.options.Count-1)
	l.anchor = -1
	l.touch(true)
	return l
}

func (l *VirtualListView) OnSelection(fn func(keys []int)) *VirtualListView {
	l.onSelection = fn
	return l
}

func (l *VirtualListView) OnSubmit(fn func(index int)) *VirtualListView {
	l.onSubmit = fn
	return l
}

func (l *VirtualListView) SelectedKeys() []int {
	keys := make([]int, 0, len(l.chosen))
	for key := range l.chosen {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (l *VirtualListView) resetSelection(count int) {
	if count < len(l.chosen)/4 {
		l.chosen = nil
	} else {
		clear(l.chosen)
	}
}

func (l *VirtualListView) SetSelectedKeys(keys ...int) *VirtualListView {
	l.resetSelection(len(keys))
	if len(keys) > 0 {
		if l.chosen == nil {
			l.chosen = make(map[int]struct{}, len(keys))
		}
		for _, key := range keys {
			l.chosen[key] = struct{}{}
			if !l.options.MultiSelect {
				break
			}
		}
	}
	l.touch(false)
	return l
}

func (l *VirtualListView) Visible() (first, last int) { return l.first, l.last }

// SetCount updates the collection size. If follow is true and the last row
// was visible, keep newly appended rows in view.
func (l *VirtualListView) SetCount(count int, follow bool) *VirtualListView {
	atEnd := l.AtEnd()
	if count < 0 || count >= math.MaxInt32 {
		panic("ui: invalid virtual list count")
	}
	if count < l.options.Count {
		headers := sort.SearchInts(l.options.Headers, count)
		l.options.Headers = l.options.Headers[:headers]
		if headers == 0 {
			l.options.Headers = nil
		} else if headers < cap(l.options.Headers)/4 {
			l.options.Headers = slices.Clone(l.options.Headers)
		}
		if count == 0 || count < len(l.heights)/4 {
			// Rebuild a small prefix without undoing every discarded measurement.
			heights := l.heights
			l.heights, l.sums = nil, nil
			for index := range count {
				l.setHeightDelta(index, heights[index])
			}
		} else {
			for index := range l.heights {
				if index >= count {
					l.setHeightDelta(index, 0)
				}
			}
		}
		if l.options.Key == nil {
			selected := len(l.chosen)
			for key := range l.chosen {
				if key >= count {
					delete(l.chosen, key)
				}
			}
			if len(l.chosen) == 0 {
				l.chosen = nil
			} else if len(l.chosen) < selected/4 {
				l.chosen = maps.Collect(maps.All(l.chosen))
			}
		}
		l.lead = min(l.lead, count-1)
		l.anchor = min(l.anchor, count-1)
	}
	l.options.Count = count
	l.touch(true)
	if follow && atEnd {
		l.ScrollToEnd()
	}
	return l
}

func (l *VirtualListView) AtEnd() bool {
	return l.endRequested || l.options.Count == 0 || l.scroll >= l.maxScroll()-.5
}

func (l *VirtualListView) ScrollToEnd() {
	l.endRequested = true
	l.touch(true)
}

func (l *VirtualListView) ScrollTo(index int) {
	if index >= 0 && index < l.options.Count {
		l.endRequested = false
		l.scroll = float32(l.top(index))
		l.touch(true)
	}
}

func (l *VirtualListView) ScrollIntoView(index int) {
	if index < 0 || index >= l.options.Count {
		return
	}
	top, bottom := float32(l.top(index)), float32(l.top(index+1))
	if top < l.scroll || bottom-top > l.rect.H {
		l.ScrollTo(index)
	} else if bottom > l.scroll+l.rect.H {
		l.scroll = bottom - l.rect.H
		l.touch(true)
	}
}

func (l *VirtualListView) top(i int) float64 {
	offset := float64(i) * float64(l.options.EstimatedHeight)
	for ; i > 0; i -= i & -i {
		offset += l.sums[i]
	}
	return offset
}

func (l *VirtualListView) index(y float32) int {
	count := l.options.Count
	estimate := float64(l.options.EstimatedHeight)
	if len(l.sums) == 0 {
		return min(count, max(0, int(float64(y)/estimate)))
	}
	i := 0
	offset := float64(0)
	for step := 1 << (bits.Len(uint(count)) - 1); step > 0; step >>= 1 {
		next := i + step
		nextOffset := offset + float64(step)*estimate + l.sums[next]
		if next <= count && nextOffset <= float64(y) {
			i, offset = next, nextOffset
		}
	}
	return i
}

func (l *VirtualListView) measure(i int, height float32) {
	l.setHeightDelta(i, float64(height)-float64(l.options.EstimatedHeight))
}

func (l *VirtualListView) setHeightDelta(i int, delta float64) {
	old := l.heights[i]
	if old == delta {
		return
	}
	if l.heights == nil {
		l.heights, l.sums = make(map[int]float64), make(map[int]float64)
	}
	if delta == 0 {
		delete(l.heights, i)
	} else {
		l.heights[i] = delta
	}
	for p := i + 1; p > 0 && p < math.MaxInt32; p += p & -p {
		l.sums[p] += delta - old
		if l.sums[p] == 0 {
			delete(l.sums, p)
		}
	}
}

func (l *VirtualListView) row(i int) *Element {
	if row := l.rows[i]; row != nil {
		return row
	}
	body := l.build(i)
	row := element(kVirtualRow).Children(body)
	row.parent = l.Element
	row.attach(l.view)
	row.rowIndex = int32(i)
	if l.options.Name != nil {
		row.label = l.options.Name(i)
	} else if body.label != "" {
		row.label = body.label
	} else if body.text != "" {
		row.label = body.text
	} else {
		row.label = "Row " + strconv.Itoa(i+1)
	}
	l.rows[i] = row
	return row
}

func (l *VirtualListView) detach(row *Element) {
	if l.view != nil {
		l.view.forget(row)
	}
	row.attach(nil)
	row.parent = nil
}

func (l *VirtualListView) measureRow(v *view, i int, remeasure bool) float32 {
	row := l.row(i)
	if remeasure || row.measuredHeight == 0 {
		_, h := v.measure(row)
		row.measuredHeight = max(1, h)
		l.measure(i, row.measuredHeight)
	}
	return row.measuredHeight
}

func (l *VirtualListView) layout(v *view, r, clip Rect, remeasure bool) {
	if l.endRequested {
		l.endRequested = false
		var total float32
		for i := l.options.Count - 1; i >= 0 && total < r.H+2*l.options.EstimatedHeight; i-- {
			total += l.measureRow(v, i, remeasure)
		}
		l.scroll = l.maxScroll()
	}
	l.scroll = max(0, min(l.scroll, l.maxScroll()))
	l.first, l.last = -1, -1
	clear(l.children)
	l.children = l.children[:0]
	first := l.index(l.scroll)
	end := first
	pinned := -1
	nextHeader, _ := slices.BinarySearch(l.options.Headers, first+1)
	if nextHeader > 0 {
		pinned = l.options.Headers[nextHeader-1]
	}
	clip = clip.intersect(r)
	left := max(0, first-2)
	anchor := float64(l.scroll) - l.top(first)
	for i := left; i < first; i++ {
		l.measureRow(v, i, remeasure)
	}
	l.scroll = max(0, min(float32(l.top(first)+anchor), l.maxScroll()))
	bottom := float64(l.scroll + r.H)
	for end < l.options.Count && (end < first+2 || l.top(end) < bottom) {
		l.measureRow(v, end, remeasure)
		end++
	}
	visibleEnd := end
	if r.H > 0 && first < l.options.Count {
		l.first, l.last = l.index(l.scroll), min(l.options.Count-1, l.index(math.Nextafter32(l.scroll+r.H, 0)))
	}
	for ; end < min(l.options.Count, visibleEnd+2); end++ {
		l.measureRow(v, end, remeasure)
	}
	bodyClip := clip
	var pinnedBox Rect
	if pinned >= 0 {
		h := l.measureRow(v, pinned, remeasure)
		y := r.Y
		if nextHeader < len(l.options.Headers) {
			y = min(y, r.Y+float32(l.top(l.options.Headers[nextHeader]))-l.scroll-h)
		}
		pinnedBox = Rect{r.X, y, r.W, max(1, h)}
		bodyClip = bodyClip.intersect(Rect{r.X, y + h, r.W, r.H})
	}
	for i := left; i < end; i++ {
		if i == pinned {
			continue
		}
		row := l.rows[i]
		y := float32(l.top(i) - float64(l.scroll))
		v.layout(row, Rect{r.X, r.Y + y, r.W, float32(l.top(i+1) - l.top(i))}, bodyClip)
		l.children = append(l.children, row)
	}
	if pinned >= 0 {
		row := l.rows[pinned]
		v.layout(row, pinnedBox, clip)
		l.children = append(l.children, row)
	}
	for i, row := range l.rows {
		if i != pinned && (i < left || i >= end) {
			l.detach(row)
			delete(l.rows, i)
		}
	}
}

func (l *VirtualListView) click(index int, ev fego.Event) {
	key := l.key(index)
	modified := ev.Shift || ev.Control || ev.Super
	l.choose(index, ev.Shift, ev.Control || ev.Super)
	if l.onSubmit == nil || modified {
		l.lastClick = time.Time{}
		return
	}
	now := time.Now()
	if key == l.lastClickKey && now.Sub(l.lastClick) <= 400*time.Millisecond {
		l.lastClick = time.Time{}
		// Selection callbacks may remove or reorder the row.
		if index < l.options.Count && key == l.key(index) {
			l.onSubmit(index)
		}
	} else {
		l.lastClick, l.lastClickKey = now, key
	}
}

func (l *VirtualListView) choose(i int, extend, toggle bool) {
	if i < 0 || i >= l.options.Count || l.header(i) {
		return
	}
	key := l.key(i)
	switch {
	case l.options.MultiSelect && extend && l.anchor >= 0:
		first, last := min(l.anchor, i), max(l.anchor, i)
		if !toggle {
			l.resetSelection(last - first + 1)
		}
		if l.chosen == nil {
			l.chosen = make(map[int]struct{})
		}
		for j := first; j <= last; j++ {
			if !l.header(j) {
				l.chosen[l.key(j)] = struct{}{}
			}
		}
	case l.options.MultiSelect && toggle:
		if l.chosen == nil {
			l.chosen = make(map[int]struct{})
		}
		if _, ok := l.chosen[key]; ok {
			delete(l.chosen, key)
		} else {
			l.chosen[key] = struct{}{}
		}
		l.anchor = i
	default:
		l.SetSelectedKeys(key)
		l.anchor = i
	}
	l.lead = i
	l.touch(false)
	if l.onSelection != nil {
		l.onSelection(l.SelectedKeys())
	}
}

func (l *VirtualListView) keyDown(ev fego.Event) {
	i, direction := l.lead, 1
	switch ev.Key {
	case "Enter":
		if i >= 0 && i < l.options.Count && l.onSubmit != nil {
			l.onSubmit(i)
		}
		return
	case "Space":
		if i >= 0 {
			l.choose(i, ev.Shift, ev.Control || ev.Super)
		}
		return

	case "ArrowDown":
		if ev.Alt && i >= 0 && i+1 < l.options.Count {
			l.drop(i, i+2)
			return
		}
		i++
	case "ArrowUp":
		if ev.Alt && i > 0 {
			l.drop(i, i-1)
			return
		}
		i--
		direction = -1
	case "Home":
		i = 0
	case "End":
		i = l.options.Count - 1
		direction = -1
	case "PageDown":
		i = l.index(l.scroll + l.rect.H)
	case "PageUp":
		i = l.index(max(0, l.scroll-l.rect.H))
		direction = -1
	default:
		if (ev.Key == "a" || ev.Key == "A") && l.options.MultiSelect && (ev.Control || ev.Super) {
			l.chosen = make(map[int]struct{})
			for j := range l.options.Count {
				if !l.header(j) {
					l.chosen[l.key(j)] = struct{}{}
				}
			}
			l.touch(false)
			if l.onSelection != nil {
				l.onSelection(l.SelectedKeys())
			}
			return
		}
		if l.options.Name != nil && !ev.Control && !ev.Super && utf8.RuneCountInString(ev.Key) == 1 {
			prefix := strings.ToLower(ev.Key)
			for distance := 1; distance <= l.options.Count; distance++ {
				row := (max(-1, i) + distance) % l.options.Count
				if !l.header(row) && strings.HasPrefix(strings.ToLower(l.options.Name(row)), prefix) {
					l.choose(row, false, false)
					l.ScrollIntoView(row)
					break
				}
			}
		}
		return
	}
	i = max(0, min(l.options.Count-1, i))
	for i >= 0 && i < l.options.Count && l.header(i) {
		i += direction
	}
	l.choose(i, ev.Shift, ev.Control || ev.Super)
	l.ScrollIntoView(i)
}

func (l *VirtualListView) drop(source, before int) {
	if l.options.Reorder == nil || l.header(source) || before == source || before == source+1 {
		return
	}
	rows := []int{source}
	if _, selected := l.chosen[l.key(source)]; selected && len(l.chosen) > 1 {
		rows = rows[:0]
		for i := range l.options.Count {
			if _, ok := l.chosen[l.key(i)]; ok && !l.header(i) {
				rows = append(rows, i)
			}
		}
	}
	l.options.Reorder(rows, before)
}
