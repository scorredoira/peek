package main

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/scorredoira/peek/browse"

	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

func TestHumanPathEndsOnTheTargetNearlyStraight(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 200; i++ {
		from := proto.Point{X: rng.Float64() * 1200, Y: rng.Float64() * 800}
		to := proto.Point{X: rng.Float64() * 1200, Y: rng.Float64() * 800}
		path := humanPath(from, to, 800*time.Millisecond, rng)

		if len(path) != int(800*time.Millisecond/moveStep) {
			t.Fatalf("%d points for 800ms, want one per step", len(path))
		}
		if last := path[len(path)-1]; last != to {
			t.Fatalf("ends at %v, want exactly %v", last, to)
		}
		// the bow is at most 8% of the way, plus the tremor
		dist := math.Hypot(to.X-from.X, to.Y-from.Y)
		for _, p := range path {
			if off := lineDistance(p, from, to); off > 0.08*dist+2 {
				t.Fatalf("%v is %.1fpx off the line %v -> %v", p, off, from, to)
			}
		}
	}
}

// The recording: 90% of the way is done by about mid-time, then a slow homing in.
func TestHumanPathIsFastFirstAndHomesInSlowly(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	from, to := proto.Point{X: 0, Y: 0}, proto.Point{X: 500, Y: 0}
	path := humanPath(from, to, time.Second, rng)
	at := func(frac float64) float64 { return path[int(frac*float64(len(path)-1))].X / 500 }
	if got := at(0.5); got < 0.8 || got > 0.97 {
		t.Fatalf("%.2f of the way at mid-time, want 0.8 to 0.97", got)
	}
	if got := at(0.1); got > 0.25 {
		t.Fatalf("%.2f of the way at a tenth of the time: it should start gently", got)
	}
}

func TestMoveDurationGrowsWithDistanceAndShrinkingTargets(t *testing.T) {
	median := func(dist, size float64) time.Duration {
		rng := rand.New(rand.NewPCG(9, 10))
		var sum time.Duration
		for range 500 {
			sum += moveDuration(dist, size, rng)
		}
		return sum / 500
	}
	near, far, tiny := median(100, 40), median(800, 40), median(800, 10)
	if !(near < far && far < tiny) {
		t.Fatalf("near %v, far %v, far and tiny %v: want each longer", near, far, tiny)
	}
	if near < 400*time.Millisecond || far > 1500*time.Millisecond {
		t.Fatalf("near %v, far %v: outside what the recording shows", near, far)
	}
}

func TestHumanPathVaries(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	from, to := proto.Point{X: 10, Y: 10}, proto.Point{X: 600, Y: 400}
	a, b := humanPath(from, to, time.Second, rng), humanPath(from, to, time.Second, rng)
	if a[len(a)/2] == b[len(b)/2] {
		t.Fatal("two moves between the same points took the same way")
	}
}

func TestPointInBoxStaysAwayFromTheEdges(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	box := proto.DOMRect{X: 100, Y: 50, Width: 80, Height: 30}
	seen := map[proto.Point]bool{}
	for i := 0; i < 1000; i++ {
		p := pointInBox(box, rng)
		if p.X < box.X+box.Width*0.2-1e-9 || p.X > box.X+box.Width*0.8+1e-9 ||
			p.Y < box.Y+box.Height*0.2-1e-9 || p.Y > box.Y+box.Height*0.8+1e-9 {
			t.Fatalf("%v is near the edge of %+v", p, box)
		}
		seen[p] = true
	}
	if len(seen) < 900 {
		t.Fatalf("only %d different points in 1000 presses", len(seen))
	}
}

func TestFlickAddsUpAndTrailsOff(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	for _, total := range []float64{300, -450, 90} {
		d := flick(total, rng)
		sum := 0.0
		for _, x := range d {
			sum += x
		}
		if sum != total {
			t.Fatalf("flick of %v adds up to %v", total, sum)
		}
		if math.Abs(d[0]) != 1 {
			t.Fatalf("a flick starts with one pixel, got %v", d[0])
		}
		peak := 0.0
		for _, x := range d {
			peak = math.Max(peak, math.Abs(x))
		}
		if tail := math.Abs(d[len(d)-2]); tail > peak/3 {
			t.Fatalf("flick of %v ends at %v of a %v peak: no inertial tail", total, tail, peak)
		}
	}
}

func TestKeyForTypesWhatTheKeyboardHasAndLeavesTheRestAsText(t *testing.T) {
	for _, r := range "aZ5 .@" {
		if _, ok := keyFor(r); !ok {
			t.Errorf("%q has a key", r)
		}
	}
	if k, ok := keyFor('\n'); !ok || k != input.Enter {
		t.Errorf("a newline is Enter, got %v %v", k, ok)
	}
	for _, r := range "ñá€😀" {
		if _, ok := keyFor(r); ok {
			t.Errorf("%q has no key on rod's keyboard", r)
		}
	}
	for _, r := range "A?@!(" {
		if !shifted[r] {
			t.Errorf("%q is typed with Shift", r)
		}
	}
	for _, r := range "a5 .," {
		if shifted[r] {
			t.Errorf("%q is typed without Shift", r)
		}
	}
}

func lineDistance(p, a, b proto.Point) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return math.Hypot(p.X-a.X, p.Y-a.Y)
	}
	return math.Abs((p.X-a.X)*dy-(p.Y-a.Y)*dx) / l
}

// Select-all is the keys a person presses, trusted, and it empties a contenteditable as well as
// an input, which select() never reached.
func TestSelectAllEmptiesTheFieldWithRealKeys(t *testing.T) {
	if _, err := browse.FindChromePath(); err != nil {
		t.Skipf("no Chrome on this machine: %v", err)
	}
	chrome, err := browse.Launch(browse.LaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer chrome.Close()
	page, err := chrome.Browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{`<input id=f value="old text">`, `<div id=f contenteditable>old text</div>`} {
		page.MustEval(`html => {
			document.body.innerHTML = html
			window.keys = []
			if (!window.listening) {
				window.listening = true
				addEventListener('keydown', e => keys.push(e.isTrusted + ' ' + e.key + ' ' + (e.metaKey || e.ctrlKey)))
			}
		}`, field)
		el := page.MustElement("#f")
		el.MustEval(`() => this.focus()`)

		tp := &typist{page: page, rng: newRand(), nextAt: time.Now()}
		if err := tp.selectAll(); err != nil {
			t.Fatal(err)
		}
		if err := tp.backspace(); err != nil {
			t.Fatal(err)
		}
		if err := tp.releaseAll(); err != nil {
			t.Fatal(err)
		}

		if got, _ := browse.FieldValue(el); got != "" {
			t.Errorf("%s: still holds %q", field, got)
		}
		keys := page.MustEval(`() => keys.join(', ')`).Str()
		if !strings.HasPrefix(keys, "true Meta true, true a true, true Backspace false") &&
			!strings.HasPrefix(keys, "true Control true, true a true, true Backspace false") {
			t.Errorf("%s: keys were %s", field, keys)
		}
	}
}
