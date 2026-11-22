package ui

import (
	"math"

	"github.com/zatxm/fego/v26"
)

// RangeSliderView selects an inclusive interval with two handles. Shift and an
// arrow key move the upper handle, while arrows alone move the lower handle.
type RangeSliderView struct {
	*Element
	high       float64
	activeHigh bool
	onRange    func(low, high float64)
}

func RangeSlider(low, high, minimum, maximum float64) *RangeSliderView {
	e := Slider(low, minimum, maximum)
	r := &RangeSliderView{Element: e}
	e.own().interval = r
	return r.SetRange(low, high)
}

func (r *RangeSliderView) Range() (low, high float64) { return r.value, r.high }

func (r *RangeSliderView) SetRange(low, high float64) *RangeSliderView {
	if !finite(low) || !finite(high) || low > high {
		panic("ui: invalid slider interval")
	}
	high = max(r.minimum, min(r.maximum, high))
	if r.high != high {
		previous := r.high
		r.high = high
		r.damageValue(previous, high)
	}
	r.SetValue(low)
	return r
}

func (r *RangeSliderView) OnChange(fn func(low, high float64)) *RangeSliderView {
	r.onRange = fn
	return r
}

func (r *RangeSliderView) move(value float64, upper bool) {
	low, high := r.Range()
	if upper {
		r.SetRange(low, max(low, value))
	} else {
		r.SetRange(min(value, high), high)
	}
	if r.onRange != nil && (low != r.value || high != r.high) {
		r.onRange(r.value, r.high)
	}
}

func sliderFraction(e *Element, value float64) float32 {
	if math.IsInf(e.maximum-e.minimum, 0) {
		return float32((value/2 - e.minimum/2) / (e.maximum/2 - e.minimum/2))
	}
	return float32((value - e.minimum) / (e.maximum - e.minimum))
}

func (r *RangeSliderView) key(ev fego.Event) bool {
	high := ev.Shift
	value := r.value
	if high {
		value = r.high
	}
	fraction := sliderFraction(r.Element, value)
	switch ev.Key {
	case "ArrowUp", "ArrowRight":
		fraction += .01
	case "ArrowDown", "ArrowLeft":
		fraction -= .01
	case "Home":
		fraction = 0
	case "End":
		fraction = 1
	default:
		return false
	}
	f := float64(max(0, min(1, fraction)))
	r.move(r.minimum*(1-f)+r.maximum*f, high)
	return true
}
