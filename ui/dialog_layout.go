package ui

import (
	"sort"
	"strings"
	"unicode"
)

func (v *view) labelText(e *Element) string {
	if wrapped, ok := v.dialogLines[e]; ok {
		return wrapped
	}
	return e.text
}

func (v *view) fitDialog(bounds, screen Rect) Rect {
	panel := v.dialog
	if len(panel.children) < 2 {
		return bounds
	}
	header, body := panel.children[0], panel.children[1]
	clear(v.dialogLines)
	body.scrollable, body.style.h = false, 0
	width := max(1, bounds.W-panel.style.padding[1]-panel.style.padding[3])
	if len(panel.children) == 3 {
		bar := panel.children[2]
		bar.style.dir = horizontal
		if natural, _ := v.measure(bar); natural > width {
			var widest float32
			for _, action := range bar.children {
				w, _ := v.measure(action)
				widest = max(widest, w)
			}
			bar.style.dir = grid
			bar.style.columns = max(1, int((width+bar.style.gap)/(widest+bar.style.gap)))
		}
	}
	// Lay out once for the actual widths allocated to nested labels, including
	// growing columns and the title beside its close button.
	v.layout(panel, bounds, screen)
	v.wrapDialogLabels(panel, bounds.X+bounds.W-panel.style.padding[1])
	_, natural := v.measure(panel)
	bounds.H = min(natural, max(0, screen.H-32))
	bounds.Y = (screen.H - bounds.H) / 2
	_, titleH := v.measure(header)
	available := bounds.H - panel.style.padding[0] - panel.style.padding[2] - panel.style.gap - titleH
	if len(panel.children) == 3 {
		_, actionsH := v.measure(panel.children[2])
		available -= panel.style.gap + actionsH
	}
	if _, h := v.measure(body); body.growth(false) == 0 && h > available {
		body.scrollable, body.style.h = true, max(1, available)
	} else {
		body.scroll = 0
	}
	return bounds
}

func (v *view) wrapDialogLabels(e *Element, right float32) {
	if e.hidden {
		return
	}
	right = min(right, e.rect.X+e.rect.W)
	if e.kind == kText {
		w := max(1, right-e.rect.X)
		font := v.font(e)
		if natural, _ := v.text.Measure(e.text, font, e.style.bold); natural > w {
			if v.dialogLines == nil {
				v.dialogLines = make(map[*Element]string)
			}
			v.dialogLines[e] = v.wrapLabel(e.text, w, font, e.style.bold)
		}
		return
	}
	for _, child := range e.children {
		v.wrapDialogLabels(child, right-e.style.padding[1])
	}
}

func (v *view) wrapLabel(text string, width, font float32, bold bool) string {
	var out strings.Builder
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		r := []rune(line)
		for len(r) > 0 {
			fits := func(n int) bool {
				w, _ := v.text.Measure(string(r[:n]), font, bold)
				return w <= width
			}
			n := 1
			for n < len(r) && fits(n) {
				n = min(n*2, len(r))
			}
			end := max(1, sort.Search(n, func(i int) bool { return !fits(i + 1) }))
			if end < len(r) {
				for j := end; j > 0; j-- {
					if unicode.IsSpace(r[j-1]) {
						end = j
						break
					}
				}
			}
			out.WriteString(string(r[:end]))
			r = r[end:]
			if len(r) != 0 {
				out.WriteByte('\n')
			}
		}
	}
	return out.String()
}
