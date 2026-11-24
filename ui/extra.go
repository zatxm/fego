package ui

import (
	"bytes"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// CalendarView is a month grid made of ordinary keyboard-focusable buttons.
// Tab moves between its controls; Enter or Space chooses a day.
type CalendarView struct {
	*Element
	date, month, today time.Time
	title              *Element
	days               [42]*Element
	dates              [42]time.Time
	selectedDay        int
	onDate             func(time.Time)
	onPick             func(time.Time)
}

func Calendar(date time.Time) *CalendarView {
	if date.IsZero() {
		date = time.Now()
	}
	c := &CalendarView{Element: Column().Gap(4).Width(248).Padding(12)}
	c.title = Text("").Bold()
	previous := iconButton(chevronLeft, "Previous month", func() { c.showMonth(c.month.AddDate(0, -1, 0)) })
	next := iconButton(chevronRight, "Next month", func() { c.showMonth(c.month.AddDate(0, 1, 0)) })
	header := Row(previous, Box(c.title).Center().Grow(1), next)
	weekdays := Grid(7).Gap(4)
	for _, day := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		weekdays.Append(Box(Text(day).FontSize(12).Muted()).Center().Height(24))
	}
	grid := Grid(7).Gap(0)
	for i := range c.days {
		i := i
		button := Button("", func() {
			chosen := c.dates[i]

			changed := !sameDate(c.date, chosen)
			c.SetDate(chosen)
			if changed && c.onDate != nil {
				c.onDate(c.date)
			}
			if c.onPick != nil {
				c.onPick(c.date)
			}
		}).Height(32).Radius(6)
		button.style.button = dayButton
		button.own().onNavigate = func(key string) {
			var days int
			switch key {
			case "ArrowLeft":
				days = -1
			case "ArrowRight":
				days = 1
			case "ArrowUp":
				days = -7
			case "ArrowDown":
				days = 7
			default:
				return
			}
			c.focusDay(c.dates[i].AddDate(0, 0, days))
		}
		c.days[i] = button
		grid.Append(button)
	}
	c.Children(header, weekdays, grid)
	return c.SetDate(date)
}

func dateOnly(date time.Time) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
}
func sameDate(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}
func (c *CalendarView) Date() time.Time { return c.date }
func (c *CalendarView) SetDate(date time.Time) *CalendarView {
	c.date = dateOnly(date)
	month := c.date.AddDate(0, 0, 1-c.date.Day())
	if c.month.IsZero() || !c.month.Equal(month) || c.month.Location() != month.Location() {
		c.showMonth(month)
		return c
	}
	index := (int(month.Weekday())+6)%7 + date.Day() - 1
	if c.selectedDay != index {
		if c.selectedDay >= 0 {
			previous := c.days[c.selectedDay]
			previous.primary = false
			if c.view != nil {
				c.view.repaint(previous, false)
			}
		}
		c.selectedDay = index
		selected := c.days[index]
		selected.primary = true
		if c.view != nil {
			c.view.repaint(selected, false)
		}
	}
	return c
}
func (c *CalendarView) OnChange(fn func(time.Time)) *CalendarView { c.onDate = fn; return c }
func (c *CalendarView) showMonth(month time.Time) {
	c.month = month
	c.today = dateOnly(time.Now().In(month.Location()))
	c.selectedDay = -1
	c.title.SetText(month.Format("January 2006"))
	start := (int(month.Weekday()) + 6) % 7
	for i, button := range c.days {
		date := month.AddDate(0, 0, i-start)
		c.dates[i] = date
		button.style.button = dayButton
		if date.Month() != month.Month() {
			button.style.button = outsideDayButton
		} else if sameDate(date, c.today) {
			button.style.button = todayButton
		}
		button.text = strconv.Itoa(date.Day())
		button.label = date.Format("2006-01-02")
		button.primary = sameDate(c.date, date)
		if button.primary {
			c.selectedDay = i
		}
	}
	c.touch(false)
}

func (c *CalendarView) focusDay(date time.Time) {
	if date.Month() != c.month.Month() || date.Year() != c.month.Year() {
		c.showMonth(time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, date.Location()))
	}
	if c.view != nil {
		for i, day := range c.dates {
			if sameDate(date, day) {
				c.view.focus(c.days[i])
				return
			}
		}
	}
}

// DateField accepts an ISO date and also opens a calendar popover.
// Invalid intermediate edits stay visible; Date returns the last valid date.
type DateField struct {
	*Element
	Input     *Element
	Calendar  *CalendarView
	date      time.Time
	valid     bool
	errorText *Element
	onDate    func(time.Time)
}

func DatePicker(date time.Time) *DateField {
	if date.IsZero() {
		date = time.Now()
	}
	d := &DateField{Element: Column().Gap(8).Width(320), date: dateOnly(date), valid: true}
	d.Input = TextInput("").Placeholder("YYYY-MM-DD").Label("Date").Grow(1)
	d.Calendar = Calendar(date)
	d.Calendar.Hidden(true)
	d.Calendar.style.card = true
	anchor := InputGroup(d.Input, nil, nil)
	toggle := iconButton(calendarIcon, "Choose date", func() {
		if v := anchor.view; v != nil {
			if v.popover.content != d.Calendar.Element {
				v.openPopover(anchor, d.Calendar.Element, nil)
				if v.focusVisible {
					d.Calendar.focusDay(d.date)
				}
			} else {
				v.closePopover()
			}
		}
	})
	anchor.Append(toggle)
	d.errorText = Text("Use a valid date: YYYY-MM-DD").FontSize(12).Hidden(true)
	d.Children(anchor, d.errorText)
	d.errorText.style.tone = dangerText
	d.Input.OnChange(func(text string) {
		date, err := time.ParseInLocation("2006-01-02", text, d.date.Location())
		d.valid = err == nil
		d.errorText.Hidden(d.valid)
		if !d.valid {
			return
		}
		changed := !sameDate(d.date, date)
		d.date = date
		d.Calendar.SetDate(date)
		if changed && d.onDate != nil {
			d.onDate(date)
		}
	})
	d.Calendar.onPick = func(date time.Time) {
		changed := !sameDate(d.date, date)
		d.SetDate(date)
		if v := d.Input.view; v != nil {
			v.closePopover()
			v.focus(d.Input)
		}
		if changed && d.onDate != nil {
			d.onDate(date)
		}
	}
	return d.SetDate(date)
}
func (d *DateField) Date() time.Time { return d.date }
func (d *DateField) Valid() bool     { return d.valid }
func (d *DateField) SetDate(date time.Time) *DateField {
	d.date = dateOnly(date)
	d.valid = true
	d.Input.SetText(d.date.Format("2006-01-02"))
	d.Calendar.SetDate(d.date)
	d.errorText.Hidden(true)
	return d
}
func (d *DateField) OnChange(fn func(time.Time)) *DateField { d.onDate = fn; return d }
func (d *DateField) Label(label string) *DateField {
	d.Element.Label(label)
	d.Input.Label(label)
	return d
}

// NumberField combines a text editor and step buttons. Invalid intermediate
// edits do not overwrite Value or invoke OnValue; Valid reports their status.
type NumberField struct {
	*Element
	Input                         *Element
	minus, plus, errorText        *Element
	value, minimum, maximum, step float64
	valid                         bool
	onNumber                      func(float64)
}

func NumberInput(value, minimum, maximum, step float64) *NumberField {
	if !finite(minimum) || !finite(maximum) || minimum >= maximum || !finite(step) || step <= 0 {
		panic("ui: invalid number input range or step")
	}
	n := &NumberField{Element: Column().Gap(6), minimum: minimum, maximum: maximum, step: step, valid: true}
	n.Input = TextInput("").Label("Number").Grow(1)
	n.minus = Button("−", func() { n.increment(-1) }).Width(36).Label("Decrease number")
	n.plus = Button("+", func() { n.increment(1) }).Width(36).Label("Increase number")
	n.errorText = Text("Enter a number from " + formatNumber(minimum) + " to " + formatNumber(maximum)).FontSize(12).Hidden(true)
	n.Children(Row(n.minus, n.Input, n.plus).Gap(6), n.errorText)
	n.errorText.style.tone = dangerText
	n.Input.OnChange(func(text string) {
		value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		n.valid = err == nil && finite(value) && value >= n.minimum && value <= n.maximum
		n.errorText.Hidden(n.valid)
		if !n.valid {
			return
		}
		changed := value != n.value
		n.value = value
		n.updateButtons()
		if changed && n.onNumber != nil {
			n.onNumber(value)
		}
	})
	return n.SetValue(value)
}
func finite(value float64) bool         { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func formatNumber(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
func (n *NumberField) increment(direction int) {
	// Add the displayed decimal values, rather than exposing binary float noise.
	step := n.step
	if direction < 0 {
		step = -step
	}
	value := max(n.minimum, min(n.maximum, decimalSum(n.value, step)))
	old := n.value
	n.SetValue(value)
	if n.value != old && n.onNumber != nil {
		n.onNumber(n.value)
	}
}

// decimalSum adds the shortest decimal forms of a and b exactly and rounds once.
func decimalSum(a, b float64) float64 {
	da, ea, na := decimalDigits(formatNumber(a))
	db, eb, nb := decimalDigits(formatNumber(b))
	e := min(ea, eb)
	da = append(da, bytes.Repeat([]byte{'0'}, ea-e)...)
	db = append(db, bytes.Repeat([]byte{'0'}, eb-e)...)
	if pad := len(da) - len(db); pad > 0 {
		db = append(bytes.Repeat([]byte{'0'}, pad), db...)
	} else if pad < 0 {
		da = append(bytes.Repeat([]byte{'0'}, -pad), da...)
	}
	sum := make([]byte, len(da)+1)
	negative := na
	if na == nb {
		carry := byte(0)
		for i := len(da) - 1; i >= 0; i-- {
			d := da[i] - '0' + db[i] - '0' + carry
			sum[i+1], carry = '0'+d%10, d/10
		}
		sum[0] = '0' + carry
	} else {
		if bytes.Compare(da, db) < 0 {
			da, db, negative = db, da, nb
		}
		borrow := byte(0)
		for i := len(da) - 1; i >= 0; i-- {
			d := 10 + da[i] - db[i] - borrow
			sum[i+1], borrow = '0'+d%10, 1-d/10
		}
		sum[0] = '0'
	}
	text := string(sum) + "e" + strconv.Itoa(e)
	if negative && strings.Trim(text[:len(sum)], "0") != "" {
		text = "-" + text
	}
	value, _ := strconv.ParseFloat(text, 64)
	return value
}

// decimalDigits splits strconv's decimal or exponent form into digits×10^exp.
func decimalDigits(s string) (digits []byte, exp int, negative bool) {
	s, negative = strings.CutPrefix(s, "-")
	mantissa, exponent, _ := strings.Cut(s, "e")
	if exponent != "" {
		exp, _ = strconv.Atoi(exponent)
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	return []byte(whole + fraction), exp - len(fraction), negative
}

func (n *NumberField) updateButtons() {
	n.minus.Disabled(n.value <= n.minimum)
	n.plus.Disabled(n.value >= n.maximum)
}
func (n *NumberField) SetValue(value float64) *NumberField {
	if !finite(value) {
		panic("ui: number input value must be finite")
	}
	n.value = max(n.minimum, min(n.maximum, value))
	n.valid = true
	n.Input.SetText(formatNumber(n.value))
	n.errorText.Hidden(true)
	n.updateButtons()
	return n
}
func (n *NumberField) Value() float64                        { return n.value }
func (n *NumberField) Valid() bool                           { return n.valid }
func (n *NumberField) OnValue(fn func(float64)) *NumberField { n.onNumber = fn; return n }
func (n *NumberField) Label(label string) *NumberField {
	n.Element.Label(label)
	n.Input.Label(label)
	n.minus.Label("Decrease " + label)
	n.plus.Label("Increase " + label)
	return n
}

// ComboBoxView supports free text and filtered suggestions. Tab reaches the
// list, arrows select suggestions, and Enter/Space accepts the selected row.
type ComboBoxView struct {
	*Element
	Input         *Element
	Choices       *Element
	options       []string
	matches       []int
	query         string
	all           bool
	noMatches     *Element
	anchor, panel *Element
	value         string
	onText        func(string)
	closeChoices  func()
}

func ComboBox(options []string, value string) *ComboBoxView {
	c := &ComboBoxView{Element: Column().Gap(6), options: append([]string(nil), options...), value: value}
	c.closeChoices = c.clearChoices
	c.Input = TextInput(value).Label("Editable choice").Grow(1)
	c.Choices = List(0, c.option).Label("Suggestions").Hidden(true)
	c.Choices.style.tone = suggestionSurface
	c.Choices.text = value
	c.noMatches = Text("No suggestions").FontSize(13).Muted().Padding(8).Hidden(true)
	c.panel = Card(c.Choices, c.noMatches).Gap(0).Padding(4).Radius(8).Hidden(true)
	toggle := iconButton(chevronDown, "Show choices", func() {
		if !c.panel.hidden {
			c.hideChoices()
			return
		}
		c.filter("")
	})
	c.anchor = InputGroup(c.Input, nil, toggle)
	c.Children(c.anchor)
	c.Input.OnChange(func(text string) { c.change(text); c.filter(text) })
	c.Input.own().onNavigate = func(key string) {
		if c.panel.hidden {
			c.filter(c.Input.Text())
		}
		if !c.Choices.hidden {
			next := c.Choices.Selected() + 1
			if key == "ArrowUp" {
				next = c.Choices.Selected() - 1
			}
			next = max(0, min(c.Choices.count-1, next))
			c.Choices.revealRow(next)
			c.Choices.SetSelected(next)
		}
	}
	c.Input.OnSubmit(func() {
		if !c.panel.hidden && c.Choices.Selected() >= 0 {
			if choose := c.Choices.onSelect; choose != nil {
				choose(c.Choices.Selected())
			}
			c.hideChoices()
		}
	})
	c.Choices.OnSelect(func(index int) {
		if index < 0 || index >= c.Choices.count {
			return
		}
		text := c.option(index)
		c.Input.SetText(text)
		c.change(text)
	})
	c.Choices.OnClick(func() {
		c.hideChoices()
		if c.Input.view != nil {
			c.Input.view.focus(c.Input)
		}
	})
	return c
}
func (c *ComboBoxView) change(text string) {
	if c.value != text {
		c.value = text
		c.Choices.text = text
		if c.onText != nil {
			c.onText(text)
		}
	}
}
func (c *ComboBoxView) option(index int) string {
	if !c.all {
		index = c.matches[index]
	}
	return c.options[index]
}

func (c *ComboBoxView) filter(query string) {
	query = strings.ToLower(query)
	previous := c.matches
	c.matches = previous[:0]
	count := len(c.options)
	switch {
	case query == "":
	case c.query != "" && strings.HasPrefix(query, c.query):
		for _, i := range previous {
			if containsFolded(c.options[i], query) {
				c.matches = append(c.matches, i)
			}
		}
		count = len(c.matches)
	default:
		for i, option := range c.options {
			if containsFolded(option, query) {
				c.matches = append(c.matches, i)
			}
		}
		count = len(c.matches)
	}
	c.query, c.all = query, query == ""
	c.Choices.SetCount(count).SetSelected(-1)
	c.Choices.scroll = 0
	height := float32(min(6, max(1, count))) * c.Choices.rowHeight()
	if c.Choices.style.h != height {
		c.Choices.Height(height)
	}
	c.Choices.Hidden(count == 0)
	c.noMatches.Hidden(count != 0)
	if v := c.anchor.view; v != nil {
		v.openPopover(c.anchor, c.panel, c.closeChoices)
	}
}

// Search ASCII options without allocating a lower-case copy for each key.
func containsFolded(option, lower string) bool {
	var folded [128]byte
	if len(option) > len(folded) {
		return strings.Contains(strings.ToLower(option), lower)
	}
	for i := 0; i < len(option); i++ {
		ch := option[i]
		if ch >= 0x80 {
			return strings.Contains(strings.ToLower(option), lower)
		}
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		folded[i] = ch
	}
	return bytes.Contains(folded[:len(option)], []byte(lower))
}

func (c *ComboBoxView) hideChoices() {
	if v := c.panel.view; v != nil {
		v.closePopover()
	} else {
		c.clearChoices()
	}
}

func (c *ComboBoxView) clearChoices() {
	c.Choices.Hidden(true).SetCount(0)
	c.noMatches.Hidden(true)
	c.matches = nil
	c.query = ""
}

func (c *ComboBoxView) Text() string { return c.value }
func (c *ComboBoxView) SetText(text string) *ComboBoxView {
	c.value = text
	c.Choices.text = text
	c.Input.SetText(text)
	c.hideChoices()
	return c
}
func (c *ComboBoxView) OnChange(fn func(string)) *ComboBoxView { c.onText = fn; return c }
func (c *ComboBoxView) Label(label string) *ComboBoxView {
	c.Element.Label(label)
	c.Input.Label(label)
	c.Choices.Label(label + " suggestions")
	return c
}

func Badge(text string) *Element {
	label := Text(text).FontSize(12)
	label.style.tone = primaryText
	badge := Box(label).Padding(2, 8).Radius(12)
	badge.style.tone = primarySurface
	return badge
}

// Avatar derives up to two Unicode initials and retains the full accessible name.
func Avatar(name string, size float32) *Element {
	if size <= 0 || math.IsNaN(float64(size)) || math.IsInf(float64(size), 0) {
		panic("ui: invalid avatar size")
	}
	words := strings.Fields(name)
	initials := "?"
	if len(words) > 0 {
		first, _ := utf8.DecodeRuneInString(words[0])
		initials = string(first)
		if len(words) > 1 {
			last, _ := utf8.DecodeRuneInString(words[len(words)-1])
			initials += string(last)
		} else {
			runes := []rune(words[0])
			if len(runes) > 1 {
				initials += string(runes[1])
			}
		}
	}
	avatar := Box(Text(strings.ToUpper(initials)).FontSize(size*.32).Bold()).Center().Size(size, size).Radius(size / 2).Label(name)
	avatar.style.tone = mutedSurface
	return avatar
}
