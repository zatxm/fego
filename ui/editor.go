package ui

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zatxm/fego/v26"
)

// Selection describes an editor range in Unicode rune offsets.
type Selection struct{ Anchor, Caret int }

// SourceEditor adds search, replace, folding and multiple selections to CodeEditor.
type SourceEditor struct {
	*Element
	Input                        *Element
	search, replacement, matches *Element
}

// Editor creates an editor with a find/replace bar. Alt+click adds a caret.
func Editor(source string) *SourceEditor {
	input := CodeEditor(source).Grow(1).Label("Source")
	x := &SourceEditor{Element: Column().Gap(6), Input: input}
	input.own().editor = &editorState{advanced: true}
	x.search = TextInput("").Placeholder("Find").Label("Find").Grow(1)
	x.replacement = TextInput("").Placeholder("Replace").Label("Replace").Grow(1)
	x.matches = Text("").FontSize(12).Muted()
	find := func() {
		if x.FindNext(x.search.Text()) {
			x.matches.SetText("")
		} else {
			x.matches.SetText("No match")
		}
	}
	x.search.OnSubmit(find)
	x.Children(Row(x.search, PrimaryButton("Find next", find), x.matches).Gap(6),
		Row(x.replacement, Button("Replace all", func() {
			n := x.ReplaceAll(x.search.Text(), x.replacement.Text())
			x.matches.SetText(strconv.Itoa(n) + " replaced")
		})).Gap(6), input)
	return x
}

func (x *SourceEditor) Text() string { return x.Input.Text() }

func (x *SourceEditor) SetText(source string) {
	x.Input.SetText(source)
	x.Input.editor.extra = nil
	x.Input.editor.folds = nil
	x.Input.editor.query = ""
}

func (x *SourceEditor) OnChange(fn func(string)) *SourceEditor {
	x.Input.OnChange(fn)
	return x
}

// Fold collapses lines after the zero-based header through last, inclusive.
// Calling Fold again with the same header expands that block.
func (x *SourceEditor) Fold(header, last int) bool {
	e := x.Input
	state := e.sourceIndex()
	if header < 0 || last <= header || last >= state.count {
		return false
	}
	if e.toggleExistingFold(header) {
		return false
	}
	return e.foldRange(header, last)
}

func (e *Element) foldRange(header, last int) bool {
	state := e.sourceIndex()
	for _, f := range state.folds {
		if header < f.last && last >= f.first {
			return false
		}
	}
	state.folds = append(state.folds, editorFold{header + 1, last + 1})
	slices.SortFunc(state.folds, func(a, b editorFold) int { return a.first - b.first })
	e.scroll = min(e.scroll, e.maxScroll())
	e.touch(false)
	return true
}

func (e *Element) toggleExistingFold(header int) bool {
	state := e.sourceIndex()
	for i, f := range state.folds {
		if f.first == header+1 {
			state.folds = slices.Delete(state.folds, i, i+1)
			e.touch(false)
			return true
		}
	}
	return false
}

func indent(line string) int { return len(line) - len(strings.TrimLeft(line, " \t")) }

func (e *Element) toggleGutter(line int) bool {
	state := e.sourceIndex()
	if e.toggleExistingFold(line) {
		return true
	}
	if !state.indentedBlock(e.text, line) {
		return false
	}
	limit := indent(state.line(e.text, line))
	last := line + 1
	for last+1 < state.count {
		text := state.line(e.text, last+1)
		if strings.TrimSpace(text) != "" && indent(text) <= limit {
			break
		}
		last++
	}
	return e.foldRange(line, last)
}

func (x *SourceEditor) SetSelections(selections ...Selection) {
	e := x.Input
	state := e.sourceIndex()
	state.extra = state.extra[:0]
	if len(selections) > 0 {
		n := state.runes
		for i, s := range selections {
			s = Selection{max(0, min(n, s.Anchor)), max(0, min(n, s.Caret))}
			if i == 0 {
				e.own().anchor, e.own().cursor = s.Anchor, s.Caret
			} else {
				state.extra = append(state.extra, s)
			}
		}
	}
	e.touch(false)
}

func (x *SourceEditor) FindNext(query string) bool {
	e := x.Input
	state := e.sourceIndex()
	state.query = query
	if query == "" {
		e.touch(false)
		return false
	}
	start := state.byteAt(e.text, max(e.cursor, e.anchor))
	p := strings.Index(e.text[start:], query)
	if p >= 0 {
		p += start
	} else {
		p = strings.Index(e.text, query)
	}
	if p < 0 {
		e.touch(false)
		return false
	}
	prefix := e.text[:p]
	anchor := utf8.RuneCountInString(prefix)
	state.folds = slices.DeleteFunc(state.folds, func(f editorFold) bool {
		line, _ := state.locate(e.text, anchor)
		return line >= f.first && line < f.last
	})
	x.SetSelections(Selection{anchor, anchor + utf8.RuneCountInString(query)})
	if e.view != nil {
		e.view.revealEditorCaret(e)
	}
	return true
}

func (x *SourceEditor) ReplaceAll(query, replacement string) int {
	if query == "" || len(replacement) > 4<<20 {
		return 0
	}
	e := x.Input
	n := strings.Count(e.text, query)
	if n == 0 || int64(len(e.text))+int64(n)*int64(len(replacement)-len(query)) > 4<<20 ||
		int64(e.sourceIndex().runes)+int64(n)*int64(utf8.RuneCountInString(replacement)-utf8.RuneCountInString(query)) > 1<<20 {
		return 0
	}
	state := e.sourceIndex()
	e.remember()
	e.text = strings.ReplaceAll(e.text, query, replacement)
	e.markEditorEdit()
	state.extra = nil
	if strings.Contains(query, "\n") || strings.Contains(replacement, "\n") {
		state.folds = nil
	}
	e.own().cursor = min(e.cursor, utf8.RuneCountInString(e.text))
	e.own().anchor = e.cursor
	e.touch(false)
	if e.onChange != nil {
		e.onChange(e.text)
	}
	return n
}

type editorLine struct{ pos, rune int }
type editorFold struct{ first, last int } // omitted line indices [first,last)

type editorState struct {
	lines           []editorLine
	count, runes    int
	stale, advanced bool
	extra           []Selection
	folds           []editorFold
	query           string
}

// A sparse index bounds a seek to a small block of lines and keeps large
// documents from retaining a pair of machine integers per line.
const editorIndexStep = 16

func (e *Element) sourceIndex() *editorState {
	w := e.own()
	if w.editor == nil {
		w.editor = &editorState{}
	}
	s := w.editor
	if len(s.lines) > 0 && !s.stale {
		return s
	}
	s.count = strings.Count(e.text, "\n") + 1
	slots := 1 + (s.count-1)/editorIndexStep
	if cap(s.lines) < slots || cap(s.lines)/4 > slots {
		s.lines = make([]editorLine, 1, slots)
	} else {
		s.lines = s.lines[:1]
	}
	row, base, end, offset := 0, 0, 0, 0
	for line := range strings.SplitSeq(e.text, "\n") {
		row++
		end += len(line) + 1
		if row%editorIndexStep == 0 && row < s.count {
			offset += utf8.RuneCountInString(e.text[base:end])
			s.lines = append(s.lines, editorLine{end, offset})
			base = end
		}
	}

	s.runes, s.stale = offset+utf8.RuneCountInString(e.text[base:]), false
	return s
}

func (s *editorState) head(text string, row int) editorLine {
	checkpoint := s.lines[row/editorIndexStep]
	pos := checkpoint.pos
	for range row % editorIndexStep {
		pos += strings.IndexByte(text[pos:], '\n') + 1
	}
	return editorLine{pos, checkpoint.rune + utf8.RuneCountInString(text[checkpoint.pos:pos])}
}

func (s *editorState) locate(text string, offset int) (int, editorLine) {
	block := max(0, sort.Search(len(s.lines), func(i int) bool { return s.lines[i].rune > offset })-1)
	line := s.lines[block]
	row := block * editorIndexStep
	for row+1 < min(s.count, (block+1)*editorIndexStep) {
		length := strings.IndexByte(text[line.pos:], '\n') + 1
		next := line.rune + utf8.RuneCountInString(text[line.pos:line.pos+length])
		if next > offset {
			break
		}
		line.pos += length
		line.rune = next
		row++
	}
	return row, line
}

func (s *editorState) byteAt(text string, offset int) int {
	_, line := s.locate(text, offset)
	return line.pos + runeByteOffset(text[line.pos:], offset-line.rune)
}

func lineEnd(text string, pos int) int {
	if i := strings.IndexByte(text[pos:], '\n'); i >= 0 {
		return pos + i
	}
	return len(text)
}

func (s *editorState) line(text string, i int) string {
	pos := s.head(text, i).pos
	return text[pos:lineEnd(text, pos)]
}

// splice shifts checkpoints after a same-line edit. A newline changes which
// lines are indexed, so rebuild the sparse index once and preserve other folds.
func (s *editorState) splice(old string, a, b, start, end int, text string, runes int) {
	removedLines, insertedLines := strings.Count(old[start:end], "\n"), strings.Count(text, "\n")
	if removedLines != 0 || insertedLines != 0 {
		delta := insertedLines - removedLines
		if len(s.folds) > 0 {
			first, _ := s.locate(old, a)
			last := first + removedLines
			folds := s.folds[:0]
			for _, f := range s.folds {
				if f.last <= first+1 {
					folds = append(folds, f)
				} else if f.first > last+1 {
					folds = append(folds, editorFold{f.first + delta, f.last + delta})
				}
			}
			s.folds = folds
		}
		s.stale = true
		return
	}
	bytes, characters := len(text)-(end-start), runes-(b-a)
	if bytes != 0 || characters != 0 {
		next := sort.Search(len(s.lines), func(i int) bool { return s.lines[i].rune > a })
		for i := next; i < len(s.lines); i++ {
			s.lines[i].pos += bytes
			s.lines[i].rune += characters
		}
		s.runes += characters
	}
}

func (s *editorState) indentedBlock(text string, line int) bool {
	if !s.advanced || line+1 >= s.count {
		return false
	}
	current, next := s.line(text, line), s.line(text, line+1)
	return strings.TrimSpace(current) != "" && strings.TrimSpace(next) != "" && indent(next) > indent(current)
}

func (s *editorState) visual(i int) int {
	row := i
	for _, f := range s.folds {
		if i < f.first {
			break
		}
		row -= min(f.last, i+1) - f.first
	}
	return max(0, row)
}
func (s *editorState) source(row int) int {
	for _, f := range s.folds {
		if row < f.first {
			break
		}
		row += f.last - f.first
	}
	return max(0, min(s.count-1, row))
}
func (s *editorState) visibleCount() int {
	n := s.count
	for _, f := range s.folds {
		n -= f.last - f.first
	}
	return n
}

func (e *Element) markEditorEdit() {
	if e.editor != nil {
		e.editor.stale = true
	}
}

func (v *view) revealEditorCaret(e *Element) {
	inner := v.inputInner(e)
	_, y := v.caretPosition(e, inner)
	lineHeight := v.text.LineHeight(v.font(e), false)
	switch {
	case y < inner.Y:
		e.scroll = max(0, e.scroll+y-inner.Y)
	case y+lineHeight > inner.Y+inner.H:
		e.scroll = min(e.maxScroll(), e.scroll+y+lineHeight-inner.Y-inner.H)
	}
	e.touch(false)
}

func (v *view) paintSource(p *Painter, e *Element, inner Rect, font float32, ink Color) {
	s := e.sourceIndex()
	lineH := v.text.LineHeight(font, false)
	allClip := p.clip
	contentClip := allClip.Intersect(inner.pixels(v.scale))
	gutterClip := allClip.Intersect(Rect{e.rect.X + 4, inner.Y, 40, inner.H}.pixels(v.scale))
	x := inner.X - e.scrollX
	yBase := inner.Y - e.scroll
	first := max(0, int((max(inner.Y, float32(allClip.Min.Y)/v.scale)-yBase)/lineH)-1)
	stop := min(s.visibleCount(), int((min(inner.Y+inner.H, float32(allClip.Max.Y)/v.scale)-yBase)/lineH)+2)
	lastLine := -2
	var head editorLine
	for row := first; row < stop; row++ {
		line := s.source(row)
		y := yBase + float32(row)*lineH
		if y+lineH <= inner.Y || y >= inner.Y+inner.H {
			continue
		}
		p.clip = gutterClip
		p.Label(strconv.Itoa(line+1), e.rect.X+7, y, font-2, v.theme.Muted, false)
		if s.advanced {
			marker := ""
			for _, f := range s.folds {
				if f.first == line+1 {
					marker = "›"
					break
				}
			}
			if marker == "" && s.indentedBlock(e.text, line) {
				marker = "⌄"
			}
			if marker != "" {
				p.Label(marker, e.rect.X+36, y, font-2, v.theme.Muted, false)
			}
		}
		p.clip = contentClip
		if line != lastLine+1 {
			head = s.head(e.text, line)
		}
		end := lineEnd(e.text, head.pos)
		text, start := e.text[head.pos:end], head.rune
		head.pos = end + 1
		if v.focused == e || s.query != "" {
			head.rune += utf8.RuneCountInString(text) + 1
		}
		lastLine = line
		drawRange := func(a, b int, caret bool) {
			lo, hi := min(a, b)-start, max(a, b)-start
			if hi < 0 || (hi == 0 && lo < 0) {
				return
			}
			n := utf8.RuneCountInString(text)
			if lo > n || (caret && hi > n) {
				return
			}
			left, _ := v.text.Measure(text[:runeByteOffset(text, max(0, lo))], font, false)
			right, _ := v.text.Measure(text[:runeByteOffset(text, min(n, hi))], font, false)
			if caret {
				p.Rect(Rect{x + left, y, 1.4, lineH}, v.theme.Accent)
			} else if right > left {
				p.Rect(Rect{x + left, y, right - left, lineH}, mix(v.theme.Surface, v.theme.Accent, .2))
			}
		}
		if s.query != "" {
			for at := 0; at < len(text); {
				i := strings.Index(text[at:], s.query)
				if i < 0 {
					break
				}
				at += i
				n := utf8.RuneCountInString(text[:at])
				at += len(s.query)
				drawRange(start+n, start+utf8.RuneCountInString(text[:at]), false)
			}
		}
		if v.focused == e {
			drawRange(e.anchor, e.cursor, e.anchor == e.cursor)
			for _, ss := range s.extra {
				drawRange(ss.Anchor, ss.Caret, ss.Anchor == ss.Caret)
			}
		}
		if e.text == "" && e.placeholder != "" {
			p.Label(e.placeholder, x, y, font, v.theme.Muted, false)
		} else {
			p.Label(text, x, y, font, ink, false)
		}
	}
	p.clip = allClip
}

func (v *view) editCarets(e *Element, text, key string) bool {
	if e.kind != kEditor || e.editor == nil || len(e.editor.extra) == 0 {
		return false
	}
	state := e.sourceIndex()
	selections := make([]Selection, 0, len(state.extra)+1)
	for _, s := range append([]Selection{{e.anchor, e.cursor}}, state.extra...) {
		a, b := min(s.Anchor, s.Caret), max(s.Anchor, s.Caret)
		if a == b {
			if key == "Backspace" {
				a = max(0, a-1)
			}
			if key == "Delete" {
				b = min(state.runes, b+1)
			}
		}
		selections = append(selections, Selection{a, b})
	}
	slices.SortFunc(selections, func(a, b Selection) int { return a.Anchor - b.Anchor })
	normal := selections[:0]
	for _, s := range selections {
		if n := len(normal); n > 0 && (s.Anchor < normal[n-1].Caret || s.Anchor == normal[n-1].Anchor) {
			normal[n-1].Caret = max(normal[n-1].Caret, s.Caret)
		} else {
			normal = append(normal, s)
		}
	}
	n := state.runes
	bytes := len(e.text)
	runes := utf8.RuneCountInString(text)
	for _, s := range normal {
		n += runes - (s.Caret - s.Anchor)
		bytes += len(text) - (state.byteAt(e.text, s.Caret) - state.byteAt(e.text, s.Anchor))
	}
	if n > 1<<20 || bytes > 4<<20 {
		return true
	}
	var out strings.Builder
	out.Grow(bytes)
	state.extra = state.extra[:0]
	prev, delta := 0, 0
	for i, s := range normal {
		start, end := state.byteAt(e.text, s.Anchor), state.byteAt(e.text, s.Caret)
		out.WriteString(e.text[prev:start])
		out.WriteString(text)
		prev = end
		at := s.Anchor + delta + runes
		delta += runes - (s.Caret - s.Anchor)
		if i == 0 {
			e.own().anchor, e.own().cursor = at, at
		} else {
			state.extra = append(state.extra, Selection{at, at})
		}
	}
	out.WriteString(e.text[prev:])
	e.remember()
	e.text = out.String()
	e.markEditorEdit()
	state.folds = state.folds[:0]
	v.edited(e)
	return true
}

func (v *view) sourceKey(e *Element, ev fego.Event) bool {
	if ev.Key == "Backspace" || ev.Key == "Delete" {
		return v.editCarets(e, "", ev.Key)
	}
	return false
}

// MarkdownEditor pairs an editable document with its rendered preview. The
// preview refreshes when selected so typing does not rebuild the document.
func MarkdownEditor(source string, onLink func(string)) *Element {
	editor := Editor(source)
	var preview *Element
	var rendered string
	body := Column(editor.Element.Grow(1)).Grow(1)
	mode := Segmented([]string{"Edit", "Preview"}, 0).OnSelect(func(selected int) {
		if selected == 1 {
			if next := editor.Text(); preview == nil || next != rendered {
				rendered = next
				var blocks []markdownBlock
				markdownBlocks(rendered, func(block markdownBlock) { blocks = append(blocks, block) })
				list := VirtualList(VirtualListOptions{Count: len(blocks), EstimatedHeight: 30}, func(i int) *Element {
					return blocks[i].element(onLink)
				})
				preview = list.Element.Grow(1)
			}
			body.Children(preview)
		} else {
			body.Children(editor.Element)
		}
	})
	return Column(mode.Element, body).Gap(8)
}
