package ui

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
	"time"
)

func TestCalendarLeapMonthAndKeyboard(t *testing.T) {
	selected := time.Date(2024, time.February, 28, 17, 0, 0, 0, time.FixedZone("local", 8*3600))
	calendar := Calendar(selected)
	var changes []time.Time
	calendar.OnChange(func(date time.Time) { changes = append(changes, date) })
	tester := NewTester(Column(calendar.Element).Padding(12), 360, 360)
	defer tester.Close()
	count := 0
	var leap *Element
	for i, day := range calendar.days {
		if day.style.button != outsideDayButton {
			count++
		}
		if calendar.dates[i].Day() == 29 && !day.hidden {
			leap = day
		}
	}
	if count != 29 || leap == nil {
		t.Fatalf("February has %d days", count)
	}
	clickElement(tester, leap)
	if calendar.Date().Day() != 29 || calendar.Date().Hour() != 0 || len(changes) != 1 {
		t.Fatalf("selection=%v changes=%v", calendar.Date(), changes)
	}
	if calendar.Date().Location() != selected.Location() {
		t.Fatal("calendar lost date location")
	}
	previous := calendar.children[0].children[0]
	clickElement(tester, previous)
	if calendar.month.Month() != time.January || calendar.Date().Month() != time.February {
		t.Fatal("browsing changed selected date")
	}
	tester.Key("Enter", false, false)
	if calendar.month.Month() != time.December || calendar.month.Year() != 2023 {
		t.Fatalf("keyboard month navigation=%v", calendar.month)
	}
	calendar.SetDate(time.Date(2023, time.February, 28, 0, 0, 0, 0, time.UTC))
	tester.Step()
	count = 0
	for _, day := range calendar.days {
		if day.style.button != outsideDayButton {
			count++
		}
	}
	if count != 28 {
		t.Fatalf("non-leap February has %d days", count)
	}
	if len(changes) != 1 {
		t.Fatal("programmatic SetDate invoked user callback")
	}
}

func TestDatePickerInvalidEditAndChoosingCurrentDate(t *testing.T) {
	date := time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC)
	picker := DatePicker(date)
	changed := 0
	picker.OnChange(func(time.Time) { changed++ })
	below := Button("Below", nil)
	tester := NewTester(Column(picker.Element, below).Padding(10), 380, 500)
	defer tester.Close()
	clickElement(tester, picker.Input)
	tester.Key("a", false, true)
	tester.Type("2023-02-29")
	if picker.Valid() || !sameDate(picker.Date(), date) || changed != 0 {
		t.Fatalf("invalid edit committed: %v %v", picker.Valid(), picker.Date())
	}
	rest := below.rect
	toggle := picker.children[0].children[1]
	clickElement(tester, toggle)
	if below.rect != rest || picker.Calendar.hidden {
		t.Fatal("opening the popover shifted the surrounding form")
	}
	tester.Key("Escape", false, false)
	if !picker.Calendar.hidden || tester.Document.live.focused != toggle {
		t.Fatal("Escape did not dismiss the popover and restore focus")
	}
	clickElement(tester, toggle)
	var current *Element
	for i, day := range picker.Calendar.days {
		if sameDate(picker.Calendar.dates[i], date) {
			current = day
		}
	}
	if current == nil {
		t.Fatal("selected day missing")
	}
	clickElement(tester, current)
	if !picker.Valid() || picker.Input.Text() != "2024-02-29" || !picker.Calendar.hidden {
		t.Fatal("choosing current day did not restore invalid input and close picker")
	}
	tester.Key("a", false, true)
	tester.Type("2025-12-31")
	if !picker.Valid() || picker.Date().Year() != 2025 || changed != 1 {
		t.Fatalf("valid typed date: %v changes=%d", picker.Date(), changed)
	}
	tester.Key("a", false, true)
	tester.Type("0001-01-01")
	if !picker.Valid() || picker.Calendar.month.Year() != 1 {
		t.Fatal("year one normalized to today")
	}
}

func TestNumberInputValidationStepAndKeyboard(t *testing.T) {
	input := NumberInput(.2, 0, 1, .1).Label("Opacity")
	values := []float64{}
	input.OnValue(func(value float64) { values = append(values, value) })
	tester := NewTester(Column(input.Element).Padding(10), 400, 180)
	defer tester.Close()
	clickElement(tester, input.plus)
	if input.Value() != .3 || input.Input.Text() != "0.3" {
		t.Fatalf("decimal step=%v text=%q", input.Value(), input.Input.Text())
	}
	tester.Key("Enter", false, false)
	if input.Value() != .4 {
		t.Fatalf("keyboard step=%v", input.Value())
	}
	clickElement(tester, input.Input)
	tester.Key("a", false, true)
	tester.Type("NaN")
	if input.Valid() || input.Value() != .4 || len(values) != 2 {
		t.Fatalf("invalid number committed: %v %v", input.Valid(), input.Value())
	}
	tester.Key("a", false, true)
	tester.Type("0.95")
	if !input.Valid() || input.Value() != .95 {
		t.Fatal("valid decimal rejected")
	}
	clickElement(tester, input.plus)
	if input.Value() != 1 || !input.plus.disabled {
		t.Fatal("upper bound did not clamp and disable increment")
	}
	input.SetValue(-4)
	if input.Value() != 0 || !input.minus.disabled || !input.Valid() {
		t.Fatal("programmatic lower-bound clamp failed")
	}
	if input.Input.label != "Opacity" {
		t.Fatal("number label did not reach text editor")
	}
}

func TestDecimalSumMatchesExactRational(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	values := []float64{0, 1, -1, .1, .2, -.3, 1e-15, 1e308, -math.MaxFloat64, math.MaxFloat64, 5e-324, 123.456, 1e21, -1e-7}
	for range 20000 {
		values = append(values, math.Float64frombits(rng.Uint64()), float64(rng.IntN(2000)-1000)/float64(1+rng.IntN(999)))
	}
	for i, a := range values {
		if math.IsNaN(a) || math.IsInf(a, 0) {
			continue
		}
		b := values[(i*7919+13)%len(values)]
		if math.IsNaN(b) || math.IsInf(b, 0) {
			continue
		}
		x, _ := new(big.Rat).SetString(formatNumber(a))
		y, _ := new(big.Rat).SetString(formatNumber(b))
		want, _ := x.Add(x, y).Float64()
		if got := decimalSum(a, b); got != want || math.Signbit(got) != math.Signbit(want) {
			t.Fatalf("%v + %v = %v, want %v", a, b, got, want)
		}
	}
}

func TestNumberInputDoesNotDiscardFloatPrecision(t *testing.T) {
	value := 1.234567890123456
	number := NumberInput(value, 0, 2, 1e-15)
	if number.Input.Text() != "1.234567890123456" {
		t.Fatal(number.Input.Text())
	}
	number.increment(1)
	if number.Value() != 1.234567890123457 {
		t.Fatalf("fine step=%v", number.Value())
	}
	number = NumberInput(1e308, -math.MaxFloat64, math.MaxFloat64, 1e308)
	number.increment(1)
	if number.Value() != math.MaxFloat64 {
		t.Fatalf("large step failed to clamp: %v", number.Value())
	}
}

func TestComboBoxFiltersAcceptsKeyboardAndFreeText(t *testing.T) {
	options := []string{"Apple", "Apricot", "Banana", "中文"}
	combo := ComboBox(options, "").Label("Fruit")
	options[0] = "mutated"
	var changes []string
	combo.OnChange(func(text string) { changes = append(changes, text) })
	tester := NewTester(Column(combo.Element).Padding(10), 400, 320)
	defer tester.Close()
	clickElement(tester, combo.Input)
	tester.Type("AP")
	if len(combo.matches) != 2 || combo.option(0) != "Apple" || combo.Choices.hidden {
		t.Fatalf("matches=%v", combo.matches)
	}
	// The suggestions are ordinary retained list controls in the tab sequence.
	tester.Key("Tab", false, false)
	tester.Key("Tab", false, false)
	if tester.Document.live.focused != combo.Choices {
		t.Fatal("suggestions not keyboard reachable")
	}
	tester.Key("ArrowDown", false, false)
	if combo.Text() != "Apple" || combo.Choices.hidden {
		t.Fatal("arrow selection did not preserve open list")
	}
	tester.Key("ArrowDown", false, false)
	if combo.Text() != "Apricot" {
		t.Fatal(combo.Text())
	}
	tester.Key("Enter", false, false)
	if combo.Text() != "Apricot" || !combo.Choices.hidden || tester.Document.live.focused != combo.Input {
		t.Fatal("Enter did not accept the selected suggestion")
	}
	tester.Key("a", false, true)
	tester.Type("custom fruit")
	if combo.Text() != "custom fruit" || !combo.Choices.hidden || combo.noMatches.hidden {
		t.Fatal("free text or empty-match indication failed")
	}
	combo.SetText("中文")
	if !combo.noMatches.hidden || combo.Input.Text() != "中文" {
		t.Fatal("programmatic text did not close suggestions")
	}
	if len(changes) != 4 {
		t.Fatalf("change callbacks=%v", changes)
	}
	combo.filter("ap")
	combo.filter("APPL")
	if combo.Choices.count != 1 || combo.option(0) != "Apple" {
		t.Fatal("appended query did not narrow the prior matches")
	}
	combo.filter("a")
	if combo.Choices.count != 3 {
		t.Fatal("deleting the query did not restore excluded options")
	}
	combo.filter("中文")
	if combo.Choices.count != 1 || combo.option(0) != "中文" {
		t.Fatal("replacing the query reused unrelated matches")
	}
}

func TestExtraControlsRemainDisabledAndUnicodeAvatar(t *testing.T) {
	number := NumberInput(1, 0, 5, 1)
	number.Disabled(true)
	tester := NewTester(Column(number.Element).Padding(10), 360, 140)
	defer tester.Close()
	clickElement(tester, number.plus)
	tester.Key("Tab", false, false)
	tester.Type("4")
	if number.Value() != 1 {
		t.Fatal("disabled composite changed")
	}
	avatar := Avatar("张三", 40)
	if avatar.children[0].text != "张三" || avatar.label != "张三" {
		t.Fatal("Unicode initials or accessible name lost")
	}
	avatar = Avatar("Ada Lovelace", 40)
	if avatar.children[0].text != "AL" {
		t.Fatal(avatar.children[0].text)
	}
}
