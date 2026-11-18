package ui

import (
	"net/url"
	"strconv"
	"strings"
)

type Tab struct {
	Title   string
	Content *Element
}

// TabView retains every page; changing tabs preserves each page's input state.
type TabView struct {
	*Element
	pages, buttons []*Element
}

func Tabs(tabs ...Tab) *TabView {
	t := &TabView{Element: Column()}
	bar := Row().Gap(4)
	for i, tab := range tabs {
		i := i
		button := Button(tab.Title, func() {
			if t.selected != i {
				t.SetSelected(i)
				if t.onSelect != nil {
					t.onSelect(i)
				}
			}
		})
		button.own().tab = true
		t.buttons = append(t.buttons, button)
		t.pages = append(t.pages, tab.Content)
		bar.Append(button)
	}
	t.Children(append([]*Element{bar, Separator()}, t.pages...)...)
	if len(tabs) > 0 {
		t.SetSelected(0)
	}
	return t
}

func (t *TabView) SetSelected(index int) *TabView {
	if index < 0 || index >= len(t.pages) {
		panic("ui: tab selection outside pages")
	}
	t.own().selected = index
	for i, page := range t.pages {
		page.Hidden(i != index)
		t.buttons[i].own().primary = i == index
		t.buttons[i].touch(false)
	}
	return t
}

func (t *TabView) OnSelect(fn func(int)) *TabView { t.own().onSelect = fn; return t }

// Segments is a mutually exclusive group of ordinary keyboard-focusable buttons.
type Segments struct {
	*Element
	buttons []*Element
}

func Segmented(options []string, selected int) *Segments {
	s := &Segments{Element: Row().Gap(3).Padding(3)}
	for i, label := range options {
		i := i
		button := Button(label, func() {
			if s.selected != i {
				s.SetSelected(i)
				if s.onSelect != nil {
					s.onSelect(i)
				}
			}
		}).Radius(6)
		button.own().tab = true
		s.buttons = append(s.buttons, button)
	}
	s.Children(s.buttons...)
	if len(options) > 0 {
		s.SetSelected(selected)
	}
	return s
}

func (s *Segments) SetSelected(index int) *Segments {
	if index < -1 || index >= len(s.buttons) {
		panic("ui: segment selection outside options")
	}
	s.own().selected = index
	for i, button := range s.buttons {
		button.own().primary = i == index
		button.touch(false)
	}
	return s
}

func (s *Segments) OnSelect(fn func(int)) *Segments { s.own().onSelect = fn; return s }

type Section struct {
	Title   string
	Content *Element
	Open    bool
}

// Accordion sections expand independently and keep their child controls alive.
func Accordion(sections ...Section) *Element {
	root := Column().Gap(10)
	for _, section := range sections {
		content := section.Content.Hidden(!section.Open)
		header := Button("", nil)
		update := func() {
			prefix := "−  "
			if content.hidden {
				prefix = "+  "
			}
			header.SetText(prefix + section.Title)
		}
		header.OnClick(func() { content.Hidden(!content.hidden); update() })
		update()
		root.Append(Column(header, content).Gap(10))
	}
	return root
}

// Field associates a visible label with the control's label metadata.
func Field(label string, control *Element, help string) *Element {
	control.Label(label)
	children := []*Element{Text(label).FontSize(13).Bold(), control.FillWidth()}
	if help != "" {
		children = append(children, Text(help).FontSize(12))
	}
	return Column(children...).Gap(7)
}

func Form(fields ...*Element) *Element { return Column(fields...).Gap(18) }

type Breadcrumb struct {
	Label  string
	Action func()
}

func Breadcrumbs(items ...Breadcrumb) *Element {
	row := Row().Gap(8)
	for i, item := range items {
		if i > 0 {
			row.Append(Text("›"))
		}
		if item.Action != nil {
			row.Append(Button(item.Label, item.Action).Height(32))
		} else {
			row.Append(Text(item.Label).Bold())
		}
	}
	return row
}

type MenuItem struct {
	Label    string
	Action   func()
	Disabled bool
}

func Menu(label string, items ...MenuItem) *Element {
	items = append([]MenuItem(nil), items...)
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = item.Label
		if item.Disabled {
			labels[i] += " (disabled)"
		}
	}
	button := Button(label, nil)
	button.OnClick(func() {
		if button.view != nil {
			button.view.openPopup(button, labels, 0, func(index int) {
				item := items[index]
				if !item.Disabled && item.Action != nil {
					item.Action()
				}
			})
		}
	})
	return button
}

func Dropdown(label string, items ...MenuItem) *Element {
	return Menu(label+" ▾", items...)
}

// Dialog makes the detached panel passed to Document.ShowDialog. The document
// owns modal focus and Escape handling; action buttons close it explicitly.
func Dialog(title string, content *Element, actions ...*Element) *Element {
	children := []*Element{Text(title).FontSize(22).Bold(), content}
	if len(actions) > 0 {
		children = append(children, Row(actions...).Justify(End))
	}
	return Card(children...).Width(440).Padding(24).Gap(18).Radius(18)
}

// Markdown renders headings, lists, fenced code, bold text, inline code and
// links. HTML stays literal text. Links invoke onLink only; they never open
// a browser or execute a command. Only http, https and mailto are actionable.
// shortcut: paragraphs keep source line breaks; use width-aware text layout
// when adding document-style automatic paragraph reflow.
func Markdown(source string, onLink func(string)) *Element {
	root := Column().Gap(9)
	var code []string
	fenced := false
	flushCode := func() {
		root.Append(Card(Text(strings.Join(code, "\n")).FontSize(13)).Padding(12).Radius(8))
		code = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if fenced {
				flushCode()
			}
			fenced = !fenced
			continue
		}
		if fenced {
			code = append(code, line)
			continue
		}
		if trimmed == "" {
			root.Append(Box().Height(4))
			continue
		}
		level := 0
		for level < len(trimmed) && level < 6 && trimmed[level] == '#' {
			level++
		}
		if level > 0 && level < len(trimmed) && trimmed[level] == ' ' {
			root.Append(Text(strings.TrimSpace(trimmed[level:])).FontSize(float32(30 - level*2)).Bold())
			continue
		}
		if trimmed == "---" || trimmed == "***" {
			root.Append(Separator())
			continue
		}
		prefix, body := "", trimmed
		if strings.HasPrefix(body, "- ") || strings.HasPrefix(body, "* ") || strings.HasPrefix(body, "+ ") {
			prefix, body = "•", body[2:]
		} else if at := strings.Index(body, ". "); at > 0 {
			if _, err := strconv.ParseUint(body[:at], 10, 32); err == nil {
				prefix, body = body[:at+1], body[at+2:]
			}
		}
		text := RichText(markdownSpans(body)...)
		if onLink != nil {
			text.OnLink(onLink)
		}
		if prefix != "" {
			root.Append(Row(Text(prefix).Width(24), text.Grow(1)).AlignItems(Start))
		} else {
			root.Append(text)
		}
	}
	if fenced {
		flushCode()
	}
	return root
}

func markdownSpans(text string) []Span {
	var spans []Span
	last := 0
	var m markdownScanner
	for i := 0; i < len(text); {
		end, span, ok := m.match(text, i)
		if !ok {
			i++
			continue
		}
		if i > last {
			spans = append(spans, Span{Text: text[last:i]})
		}
		spans = append(spans, span)
		last, i = end, end
	}
	if last < len(text) {
		spans = append(spans, Span{Text: text[last:]})
	}
	return spans
}

// markdownScanner finds **bold**, `code` and [label](target) with regexp's
// leftmost-first results. Each form ends at its delimiter or a newline; caching
// the next delimiter keeps delimiter-heavy malformed input linear in size.
type markdownScanner struct{ star, tick, bracket, paren int }

func (m *markdownScanner) match(text string, i int) (int, Span, bool) {
	switch text[i] {
	case '*':
		if i+1 < len(text) && text[i+1] == '*' {
			c := nextDelimiter(text, i+2, &m.star, "*\n")
			if c > i+2 && c+1 < len(text) && text[c] == '*' && text[c+1] == '*' {
				return c + 2, Span{Text: text[i+2 : c], Bold: true}, true
			}
		}
	case '`':
		c := nextDelimiter(text, i+1, &m.tick, "`\n")
		if c > i+1 && c < len(text) && text[c] == '`' {
			return c + 1, Span{Text: text[i+1 : c], Bold: true}, true
		}
	case '[':
		c := nextDelimiter(text, i+1, &m.bracket, "]\n")
		if c > i+1 && c+1 < len(text) && text[c] == ']' && text[c+1] == '(' {
			d := nextDelimiter(text, c+2, &m.paren, ")\n")
			if d < len(text) && text[d] == ')' {
				span := Span{Text: text[i+1 : c]}
				target := text[c+2 : d]
				if u, err := url.Parse(target); err == nil {
					switch strings.ToLower(u.Scheme) {
					case "http", "https", "mailto":
						span.Link = target
					}
				}
				return d + 1, span, true
			}
		}
	}
	return 0, Span{}, false
}

// nextDelimiter returns the first index at or after from holding one of chars,
// or len(text). Queries for one cache never move backward.
func nextDelimiter(text string, from int, cached *int, chars string) int {
	if *cached < from {
		*cached = len(text)
		if j := strings.IndexAny(text[from:], chars); j >= 0 {
			*cached = from + j
		}
	}
	return *cached
}
