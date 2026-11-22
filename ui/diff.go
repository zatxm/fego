package ui

import (
	"slices"
	"sort"
	"strconv"
	"strings"
)

type lineChange struct{ from, to, start, end int }

// lineChanges returns replacements in ascending order using newline-preserving lines.
func lineChanges(base, next []string) []lineChange {
	budget := int64(4 << 20)
	return appendLineChanges(nil, base, next, 0, 0, &budget, true)
}

func appendLineChanges(out []lineChange, base, next []string, x, y int, budget *int64, anchors bool) []lineChange {
	a, b := 0, 0
	for a < len(base) && a < len(next) && base[a] == next[a] {
		a++
	}
	b = a
	n, m := len(base), len(next)
	for n > a && m > b && base[n-1] == next[m-1] {
		n--
		m--
	}
	if n == a && m == b {
		return out
	}
	cells := int64(n-a+1) * int64(m-b+1)
	if n == a || m == b || cells > min(1<<20, *budget) {
		if n > a && m > b && anchors {
			// Unique equal lines partition large copies without quadratic memory.
			if matches := uniqueLines(base[a:n], next[b:m]); len(matches) > 0 {
				left, right := a, b
				for _, match := range matches {
					i, j := a+match[0], b+match[1]
					out = appendLineChanges(out, base[left:i], next[right:j], x+left, y+right, budget, false)
					left, right = i+1, j+1
				}
				return appendLineChanges(out, base[left:n], next[right:m], x+left, y+right, budget, false)
			}
		}
		// shortcut: treat large repeated regions as a replacement after
		// the comparison budget is spent.
		return append(out, lineChange{x + a, x + n, y + b, y + m})
	}
	*budget -= cells
	stride := m - b + 1
	lengths := make([]uint16, (n-a+1)*stride)
	for i := n - 1; i >= a; i-- {
		for j := m - 1; j >= b; j-- {
			k := (i-a)*stride + j - b
			if base[i] == next[j] {
				lengths[k] = 1 + lengths[k+stride+1]
			} else {
				lengths[k] = max(lengths[k+stride], lengths[k+1])
			}
		}
	}
	i, j := a, b
	for i < n || j < m {
		if i < n && j < m && base[i] == next[j] {
			i++
			j++
			continue
		}
		from, start := i, j
		for i < n || j < m {
			if i < n && j < m && base[i] == next[j] {
				break
			}
			k := (i-a)*stride + j - b
			if j == m || (i < n && lengths[k+stride] >= lengths[k+1]) {
				i++
			} else {
				j++
			}
		}
		out = append(out, lineChange{x + from, x + i, y + start, y + j})
	}
	return out
}

func uniqueLines(base, next []string) [][2]int {
	type positions struct{ left, right int }
	loc := make(map[string]positions, len(base))
	for i, line := range base {
		if _, ok := loc[line]; ok {
			loc[line] = positions{left: -1}
		} else {
			loc[line] = positions{left: i + 1}
		}
	}
	for j, line := range next {
		if p, ok := loc[line]; ok && p.left > 0 {
			if p.right == 0 {
				p.right = j + 1
			} else {
				p.right = -1
			}
			loc[line] = p
		}
	}
	type candidate struct{ x, y, previous int }
	var candidates []candidate
	var ends []int
	for i, line := range base {
		p := loc[line]
		if p.left != i+1 || p.right <= 0 {
			continue
		}
		j := p.right - 1
		slot := sort.Search(len(ends), func(k int) bool { return candidates[ends[k]].y >= j })
		previous := -1
		if slot > 0 {
			previous = ends[slot-1]
		}
		if slot == len(ends) {
			ends = append(ends, len(candidates))
		} else {
			ends[slot] = len(candidates)
		}
		candidates = append(candidates, candidate{i, j, previous})
	}
	if len(ends) == 0 {
		return nil
	}
	out := make([][2]int, len(ends))
	k := ends[len(ends)-1]
	for i := len(out) - 1; i >= 0; i-- {
		c := candidates[k]
		out[i] = [2]int{c.x, c.y}
		k = c.previous
	}
	return out
}

func sourceLines(value string) []string {
	lines := strings.SplitAfter(value, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type diffRow struct {
	left, right      string
	oldLine, newLine int
	changed          bool
}

// Diff displays a virtualized side-by-side line comparison.
func Diff(before, after string) *Element { return diff(before, after, "Before", "After") }

func diff(before, after, beforeLabel, afterLabel string) *Element {
	left, right := sourceLines(before), sourceLines(after)
	rows := make([]diffRow, 0, max(len(left), len(right)))
	a, b := 0, 0
	for _, change := range lineChanges(left, right) {
		for a < change.from {
			rows = append(rows, diffRow{left[a], right[b], a + 1, b + 1, false})
			a++
			b++
		}
		for i := range max(change.to-change.from, change.end-change.start) {
			row := diffRow{changed: true}
			if a+i < change.to {
				row.left, row.oldLine = left[a+i], a+i+1
			}
			if b+i < change.end {
				row.right, row.newLine = right[b+i], b+i+1
			}
			rows = append(rows, row)
		}
		a, b = change.to, change.end
	}
	for a < len(left) {
		rows = append(rows, diffRow{left[a], right[b], a + 1, b + 1, false})
		a++
		b++
	}
	list := VirtualList(VirtualListOptions{Count: len(rows), EstimatedHeight: 28}, func(index int) *Element {
		row := rows[index]
		return Row(diffCell(row.left, row.oldLine, row.changed), diffCell(row.right, row.newLine, row.changed)).Gap(8)
	})
	return diffView(list, beforeLabel, afterLabel)
}

func diffView(list *VirtualListView, left, right string) *Element {
	return Column(Row(Text(left).Bold().Grow(1), Text(right).Bold().Grow(1)).Padding(8),
		list.Element.Grow(1).FillWidth()).Gap(4).MinHeight(160)
}

func diffCell(value string, number int, changed bool) *Element {
	// Rows remain single-line, including for files without a trailing newline.
	prefix := ""
	if number > 0 {
		prefix = strconv.Itoa(number)
	}
	if changed {
		prefix += " ●"
	}
	cell := Row(Text(prefix).Muted().Width(46), Text(strings.TrimRight(value, "\r\n")).FontSize(13).Label(value)).Padding(4, 8).Grow(1)
	if number == 0 {
		cell.Children()
	}
	return cell
}

type MergeChoice uint8

const (
	KeepUnresolved MergeChoice = iota
	KeepOurs
	KeepTheirs
	KeepBoth
)

type mergeBlock struct {
	ours, theirs []string
	conflict     bool
	choice       MergeChoice
}

// MergeView holds the side-by-side conflicts and their resolutions.
type MergeView struct {
	*Element
	blocks    []mergeBlock
	conflicts []int
	active    int
	heading   *Element
}

func patchLines(base, replacement []string, from, to int, changes []lineChange) []string {
	if len(changes) == 0 {
		return base[from:to]
	}
	var lines []string
	for _, c := range changes {
		lines = append(lines, base[from:c.from]...)
		lines = append(lines, replacement[c.start:c.end]...)
		from = c.to
	}
	return append(lines, base[from:to]...)
}

func mergeLines(base, ours, theirs []string) []mergeBlock {
	left, right := lineChanges(base, ours), lineChanges(base, theirs)
	if len(left) == 0 {
		return []mergeBlock{{ours: theirs}}
	}
	if len(right) == 0 {
		return []mergeBlock{{ours: ours}}
	}
	var blocks []mergeBlock
	at := 0
	for len(left)+len(right) > 0 {
		from := len(base)
		if len(left) > 0 {
			from = left[0].from
		}
		if len(right) > 0 {
			from = min(from, right[0].from)
		}
		if at < from {
			blocks = append(blocks, mergeBlock{ours: base[at:from]})
		}
		end, a, b := from, 0, 0
		for {
			old := end
			for a < len(left) && (left[a].from == from || left[a].from < end) {
				end = max(end, left[a].to)
				a++
			}
			for b < len(right) && (right[b].from == from || right[b].from < end) {
				end = max(end, right[b].to)
				b++
			}
			if end == old {
				break
			}
		}
		l, r := patchLines(base, ours, from, end, left[:a]), patchLines(base, theirs, from, end, right[:b])
		if a == 0 {
			l = r
		}
		conflict := a > 0 && b > 0 && !slices.Equal(l, r)
		blocks = append(blocks, mergeBlock{ours: l, theirs: r, conflict: conflict})
		left, right, at = left[a:], right[b:], end
	}
	return append(blocks, mergeBlock{ours: base[at:]})
}

// Merge automatically combines independent changes and presents overlapping ones.
func Merge(base, ours, theirs string) *MergeView {
	m := &MergeView{Element: Column().Gap(8), blocks: mergeLines(sourceLines(base), sourceLines(ours), sourceLines(theirs))}
	for i, block := range m.blocks {
		if block.conflict {
			m.conflicts = append(m.conflicts, i)
		}
	}
	if len(m.conflicts) == 0 {
		merged, _ := m.Text()
		m.Append(Diff(base, merged).Grow(1))
		return m
	}
	m.heading = Text("").Bold().Grow(1)
	content := Column().Grow(1)
	show := func() {
		b := m.blocks[m.conflicts[m.active]]
		m.updateHeading()
		content.Children(diff(strings.Join(b.ours, ""), strings.Join(b.theirs, ""), "Ours", "Theirs").Grow(1))
	}
	move := func(step int) func() {
		return func() {
			n := min(len(m.conflicts)-1, max(0, m.active+step))
			if n != m.active {
				m.active = n
				show()
			}
		}
	}
	pick := func(choice MergeChoice) func() { return func() { m.Resolve(m.active, choice) } }
	m.Children(Row(Button("Previous", move(-1)), m.heading,
		Button("Next", move(1))).Gap(6), content,
		Row(Button("Use ours", pick(KeepOurs)), Button("Use theirs", pick(KeepTheirs)), Button("Use both", pick(KeepBoth))).Gap(6))
	show()
	return m
}

func (m *MergeView) Conflicts() int { return len(m.conflicts) }

func (m *MergeView) Resolve(index int, choice MergeChoice) bool {
	if index < 0 || index >= len(m.conflicts) || choice > KeepBoth {
		return false
	}
	m.blocks[m.conflicts[index]].choice = choice
	if index == m.active {
		m.updateHeading()
	}
	return true
}

func (m *MergeView) updateHeading() {
	choice := m.blocks[m.conflicts[m.active]].choice
	label := [...]string{"Unresolved", "Using ours", "Using theirs", "Using both"}[choice]
	m.heading.SetText("Conflict " + strconv.Itoa(m.active+1) + " / " + strconv.Itoa(len(m.conflicts)) + " · " + label)
}

// Text returns the merged document and reports whether all conflicts are resolved.
func (m *MergeView) Text() (string, bool) {
	var out strings.Builder
	resolved := true
	write := func(lines []string) {
		for _, line := range lines {
			out.WriteString(line)
		}
	}
	for _, b := range m.blocks {
		if !b.conflict {
			write(b.ours)
			continue
		}
		switch b.choice {
		case KeepOurs:
			write(b.ours)
		case KeepTheirs:
			write(b.theirs)
		case KeepBoth:
			write(b.ours)
			if len(b.ours) > 0 && len(b.theirs) > 0 && !strings.HasSuffix(b.ours[len(b.ours)-1], "\n") {
				out.WriteByte('\n')
			}
			write(b.theirs)
		default:
			resolved = false
			out.WriteString("<<<<<<< ours\n")
			write(b.ours)
			if len(b.ours) > 0 && !strings.HasSuffix(b.ours[len(b.ours)-1], "\n") {
				out.WriteByte('\n')
			}
			out.WriteString("=======\n")
			write(b.theirs)
			if len(b.theirs) > 0 && !strings.HasSuffix(b.theirs[len(b.theirs)-1], "\n") {
				out.WriteByte('\n')
			}
			out.WriteString(">>>>>>> theirs\n")
		}
	}
	return out.String(), resolved
}
