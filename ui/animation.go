package ui

import (
	"math"
	"sort"
	"time"
)

type Easing func(progress float64) float64

func EaseInOut(t float64) float64 { return t * t * (3 - 2*t) }

// Spring returns an underdamped transition. Damping must be between 0 and 1,
// and frequency is the number of undamped oscillations across the duration.
func Spring(damping, frequency float64) Easing {
	if !finiteValue(damping) || !finiteValue(frequency) {
		return EaseInOut
	}
	damping = min(.999, max(0, damping))
	frequency = min(30, max(.01, frequency))
	angular := 2 * math.Pi * frequency
	rate := angular * math.Sqrt(1-damping*damping)
	return func(t float64) float64 {
		if t <= 0 {
			return 0
		}
		if t >= 1 {
			return 1
		}
		return 1 - math.Exp(-damping*angular*t)*(math.Cos(rate*t)+damping*angular/rate*math.Sin(rate*t))
	}
}

type Keyframe struct {
	At    float64 // normalized position, between 0 and 1
	Value float64
	Ease  Easing // transition from the previous keyframe to this one
}

// Keyframes interpolates ordered keyframes. Outside the range it holds the
// first or last value. At a repeated position the last frame takes precedence.
func Keyframes(frames ...Keyframe) Easing {
	frames = append([]Keyframe(nil), frames...)
	sort.SliceStable(frames, func(i, j int) bool { return frames[i].At < frames[j].At })
	return func(t float64) float64 {
		if len(frames) == 0 {
			return 0
		}
		i := sort.Search(len(frames), func(i int) bool { return frames[i].At > t })
		if i == 0 {
			return frames[0].Value
		}
		a := frames[i-1]
		if i == len(frames) {
			return a.Value
		}
		b := frames[i]
		progress := (t - a.At) / (b.At - a.At)
		if b.Ease != nil {
			progress = b.Ease(progress)
		}
		return a.Value*(1-progress) + b.Value*progress
	}
}

type TimelineTrack struct {
	Start, Duration time.Duration
	Curve           Easing
	Apply           func(float64)
}

type animationClock interface {
	After(time.Duration, func()) func()
}

// Animate schedules a transition on the window's UI thread. The returned
// function cancels it and must also be called on the UI thread.
func Animate(window animationClock, duration time.Duration, curve Easing, apply func(float64)) func() {
	return Timeline(window, TimelineTrack{Duration: duration, Curve: curve, Apply: apply})
}

// Timeline runs tracks with one timer, skipping timestamps already elapsed.
func Timeline(window animationClock, tracks ...TimelineTrack) func() {
	type activeTrack struct {
		TimelineTrack
		finished bool
	}
	if window == nil {
		return func() {}
	}
	active := make([]activeTrack, 0, len(tracks))
	var duration time.Duration
	for _, track := range tracks {
		if track.Apply == nil {
			continue
		}
		track.Start, track.Duration = max(0, track.Start), max(0, track.Duration)
		if track.Start > time.Duration(math.MaxInt64)-track.Duration {
			continue
		}
		active = append(active, activeTrack{TimelineTrack: track})
		duration = max(duration, track.Start+track.Duration)
	}
	start := time.Now()
	var stop func()
	cancelled := false
	var tick func()
	tick = func() {
		if cancelled {
			return
		}
		elapsed := time.Since(start)
		next := duration
		for i := range active {
			t := &active[i]
			if t.finished {
				continue
			}
			if elapsed < t.Start {
				next = min(next, t.Start)
				continue
			}
			value := float64(1)
			t.finished = elapsed >= t.Start+t.Duration
			if !t.finished {
				const step = time.Second / 60
				next = min(next, (elapsed/step+1)*step, t.Start+t.Duration)
				value = float64(elapsed-t.Start) / float64(t.Duration)
			}
			if t.Curve != nil {
				value = t.Curve(value)
			}
			t.Apply(value)
			if cancelled {
				return
			}
		}
		if elapsed < duration {
			stop = window.After(max(0, next-time.Since(start)), tick)
		}
	}
	tick()
	return func() {
		cancelled = true
		if stop != nil {
			stop()
		}
	}
}
