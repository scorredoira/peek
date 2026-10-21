package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/scorredoira/peek/browse"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// A paced session (--paced, or a vault unlocked with it) is for the sites that would rather not
// be driven by a machine, and the cheapest tell is not the speed: it is a press made of events
// the page dispatched to itself (isTrusted false), at the exact centre of the element, with no
// pointer ever moving to it. Paced, everything goes through Chrome's own input instead, shaped on
// a recording of the owner's hand (2026-10-21, trackpad, 80 s):
//
//   - a move takes 0.5-1.6 s, grows with distance over target size (Fitts), and runs nearly
//     straight: fast at first, 90% of the way done by mid-time, then a slow homing in;
//   - the press comes 0-40 ms after the pointer stops, held 65-135 ms, landing off-centre by
//     about a sixth of the width and a ninth of the height;
//   - a key is held 75-210 ms, the next one comes 60-255 ms later (more after a space or a
//     punctuation mark), the hold often overlapping it; one key in eight was a correction;
//   - a scroll is a trackpad flick: one-pixel start, a quick ramp, a long inertial tail, at
//     60 events a second.

// pacedFor says whether this command goes at a person's pace.
func pacedFor(ctx *cmdContext) bool {
	return ctx.paced || (runtime.GOOS == "darwin" && vaultPaced(ctx.port))
}

func newRand() *rand.Rand {
	return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
}

// logNormal draws a duration around median, spread by sigma, cut to [min, max].
func logNormal(rng *rand.Rand, median, sigma float64, min, max time.Duration) time.Duration {
	d := time.Duration(median*math.Exp(rng.NormFloat64()*sigma)) * time.Millisecond
	return clampDuration(d, min, max)
}

func clampDuration(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}

func sleepBetween(rng *rand.Rand, min, max time.Duration) {
	time.Sleep(min + time.Duration(rng.Int64N(int64(max-min))))
}

const pointerFile = "pointer"

// lastPointer is where the previous command left the pointer: each peek command is its own
// process, and a pointer that jumps back to the corner between two clicks is a tell too.
func lastPointer(port int, page *rod.Page) proto.Point {
	b, err := os.ReadFile(filepath.Join(profileDir(port), pointerFile))
	if err == nil {
		if x, y, ok := strings.Cut(strings.TrimSpace(string(b)), " "); ok {
			px, errX := strconv.ParseFloat(x, 64)
			py, errY := strconv.ParseFloat(y, 64)
			if errX == nil && errY == nil {
				return proto.Point{X: px, Y: py}
			}
		}
	}
	// No record: somewhere in the page, as a hand already resting on the mouse.
	w, h := viewport(page)
	return proto.Point{X: w * (0.3 + 0.4*rand.Float64()), Y: h * (0.3 + 0.4*rand.Float64())}
}

func savePointer(port int, p proto.Point) {
	dir := profileDir(port)
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, pointerFile), fmt.Appendf(nil, "%.1f %.1f", p.X, p.Y), 0644)
}

func viewport(page *rod.Page) (float64, float64) {
	w, h := 1000.0, 700.0
	if res, err := page.Eval(`() => [innerWidth, innerHeight]`); err == nil {
		if arr := res.Value.Arr(); len(arr) == 2 {
			w, h = arr[0].Num(), arr[1].Num()
		}
	}
	return w, h
}

// pointInBox picks where a press lands: off-centre by about a sixth of the width and a ninth of
// the height, never in the outer fifth.
func pointInBox(box proto.DOMRect, rng *rand.Rand) proto.Point {
	pick := func(start, size, spread float64) float64 {
		off := rng.NormFloat64() * size * spread
		limit := size * 0.3
		return start + size/2 + math.Max(-limit, math.Min(limit, off))
	}
	return proto.Point{X: pick(box.X, box.Width, 0.155), Y: pick(box.Y, box.Height, 0.11)}
}

// moveDuration is how long a move takes: longer the farther and the smaller the target (Fitts'
// law, fitted to the recording), never the same.
func moveDuration(dist, size float64, rng *rand.Rand) time.Duration {
	if size < 8 {
		size = 8
	}
	id := math.Log2(dist/size + 1)
	return logNormal(rng, 230+170*id, 0.25, 150*time.Millisecond, 2500*time.Millisecond)
}

// moveStep is the gap between two pointer events: about 50 a second.
const moveStep = 19 * time.Millisecond

// humanPath is the way the pointer travels from one point to another in the given time, one
// point per event: nearly straight, its speed peaking early (90% of the way done by mid-time)
// and a slow homing in, ending exactly on the target.
func humanPath(from, to proto.Point, duration time.Duration, rng *rand.Rand) []proto.Point {
	steps := max(int(duration/moveStep), 4)
	dx, dy := to.X-from.X, to.Y-from.Y
	dist := math.Hypot(dx, dy)

	// a slight bow to one side, a few percent of the way
	nx, ny := 0.0, 0.0
	if dist > 0 {
		nx, ny = -dy/dist, dx/dist
	}
	bow := math.Max(-0.08, math.Min(0.08, rng.NormFloat64()*0.03)) * dist

	// the speed at each step goes as t·(1-t)^k, k around 3.5 (90% of the way by mid-time),
	// its running total is how far along it is
	k := 2.6 + rng.Float64()*1.8
	weights := make([]float64, steps)
	total := 0.0
	for i := range weights {
		t := (float64(i) + 0.5) / float64(steps)
		weights[i] = t * math.Pow(1-t, k)
		total += weights[i]
	}

	path := make([]proto.Point, 0, steps)
	done := 0.0
	for i := 0; i < steps; i++ {
		done += weights[i] / total
		s := done
		if i == steps-1 {
			s = 1
		}
		side := 4 * s * (1 - s) * bow
		p := proto.Point{X: from.X + dx*s + nx*side, Y: from.Y + dy*s + ny*side}
		if i < steps-1 {
			p.X += rng.NormFloat64() * 0.4
			p.Y += rng.NormFloat64() * 0.4
		} else {
			p = to
		}
		path = append(path, p)
	}
	return path
}

// rectOf is where the element is in the viewport now.
func rectOf(el *rod.Element) (proto.DOMRect, error) {
	res, err := el.Eval(`() => { const r = this.getBoundingClientRect(); return [r.x, r.y, r.width, r.height] }`)
	if err != nil {
		return proto.DOMRect{}, fmt.Errorf("cannot measure the element: %w", err)
	}
	b := res.Value.Arr()
	if len(b) != 4 {
		return proto.DOMRect{}, fmt.Errorf("cannot measure the element")
	}
	return proto.DOMRect{X: b[0].Num(), Y: b[1].Num(), Width: b[2].Num(), Height: b[3].Num()}, nil
}

// humanReveal brings an element that is out of view into it by trackpad flicks, with the
// pointer where it rests. A page that loads more as it goes may need more than one flick; an
// element that will not come is left for the reach check to refuse.
func humanReveal(ctx *cmdContext, page *rod.Page, el *rod.Element, rng *rand.Rand) error {
	box, err := rectOf(el)
	if err != nil {
		return err
	}
	_, vh := viewport(page)
	if box.Y >= 0 && box.Y+box.Height <= vh {
		return nil
	}
	at := lastPointer(ctx.port, page)
	if err := page.Mouse.MoveTo(at); err != nil {
		return err
	}
	for range 8 {
		want := box.Y + box.Height/2 - vh*(0.35+0.25*rng.Float64())
		if err := humanScroll(page, at, want, rng); err != nil {
			return err
		}
		if box, err = rectOf(el); err != nil {
			return err
		}
		if box.Y >= 0 && box.Y+box.Height <= vh {
			return nil
		}
	}
	return nil
}

// reveal is humanReveal when paced; otherwise the reach check's own scroll does it.
func reveal(ctx *cmdContext, page *rod.Page, el *rod.Element) error {
	if !pacedFor(ctx) {
		return nil
	}
	return humanReveal(ctx, page, el, newRand())
}

// humanMove brings the pointer over the element, scrolling to it first when it is out of view,
// and answers where it ended. The events go out on a clock, about 50 a second, whatever each
// one takes to send.
func humanMove(ctx *cmdContext, page *rod.Page, el *rod.Element, rng *rand.Rand) (proto.Point, error) {
	if err := humanReveal(ctx, page, el, rng); err != nil {
		return proto.Point{}, err
	}
	box, err := rectOf(el)
	if err != nil {
		return proto.Point{}, err
	}
	from := lastPointer(ctx.port, page)
	if err := page.Mouse.MoveTo(from); err != nil {
		return proto.Point{}, err
	}

	target := pointInBox(box, rng)
	dist := math.Hypot(target.X-from.X, target.Y-from.Y)
	next := time.Now()
	for _, p := range humanPath(from, target, moveDuration(dist, math.Min(box.Width, box.Height), rng), rng) {
		next = next.Add(moveStep + time.Duration(rng.Int64N(int64(4*time.Millisecond))) - 2*time.Millisecond)
		time.Sleep(time.Until(next))
		if err := page.Mouse.MoveTo(p); err != nil {
			return proto.Point{}, err
		}
	}
	savePointer(ctx.port, target)
	return target, nil
}

// flick is one trackpad scroll gesture worth total pixels: a one-pixel start, a ramp up over a
// few events, then an inertial tail that decays to a pixel, at 60 events a second.
func flick(total float64, rng *rand.Rand) []float64 {
	n := 18 + rng.IntN(48)
	ramp := 3 + rng.IntN(3)
	decay := math.Pow(0.06, 1/float64(n-ramp)) // the tail ends at about 6% of the peak
	shape := make([]float64, n)
	sum := 0.0
	for i := range shape {
		if i < ramp {
			shape[i] = float64(i+1) / float64(ramp)
		} else {
			shape[i] = math.Pow(decay, float64(i-ramp))
		}
		sum += shape[i]
	}
	deltas := make([]float64, n)
	done := 0.0
	for i := range shape {
		d := math.Round(total * shape[i] / sum)
		if i == 0 {
			d = math.Copysign(1, total)
		}
		if i == n-1 {
			d = total - done
		}
		deltas[i] = d
		done += d
	}
	return deltas
}

// humanScroll scrolls by about dy pixels (negative is up) the way a trackpad does: flicks of a
// few hundred pixels with a pause between them, with the pointer resting where it is.
func humanScroll(page *rod.Page, at proto.Point, dy float64, rng *rand.Rand) error {
	for left := dy; math.Abs(left) >= 1; {
		size := math.Copysign(math.Min(math.Abs(left), 250+rng.Float64()*450), left)
		for _, d := range flick(size, rng) {
			err := proto.InputDispatchMouseEvent{
				Type: proto.InputDispatchMouseEventTypeMouseWheel, X: at.X, Y: at.Y, DeltaY: d,
			}.Call(page)
			if err != nil {
				return fmt.Errorf("scroll failed: %w", err)
			}
			time.Sleep(16*time.Millisecond + time.Duration(rng.Int64N(int64(time.Millisecond))))
		}
		left -= size
		if math.Abs(left) >= 1 {
			sleepBetween(rng, 250*time.Millisecond, 900*time.Millisecond)
		}
	}
	// let the page settle and draw what came into view
	sleepBetween(rng, 200*time.Millisecond, 400*time.Millisecond)
	return nil
}

// humanPress is a click, right-click or double-click made with the pointer.
func humanPress(ctx *cmdContext, page *rod.Page, el *rod.Element, kind string) error {
	rng := newRand()
	if _, err := humanMove(ctx, page, el, rng); err != nil {
		return err
	}
	sleepBetween(rng, time.Millisecond, 25*time.Millisecond)

	button := proto.InputMouseButtonLeft
	if kind == "rightclick" {
		button = proto.InputMouseButtonRight
	}
	clicks := 1
	if kind == "dblclick" {
		clicks = 2
	}
	for n := 1; n <= clicks; n++ {
		if n > 1 {
			sleepBetween(rng, 70*time.Millisecond, 140*time.Millisecond)
		}
		if err := page.Mouse.Down(button, n); err != nil {
			return fmt.Errorf("%s failed: %w", kind, err)
		}
		time.Sleep(logNormal(rng, 100, 0.25, 55*time.Millisecond, 180*time.Millisecond))
		if err := page.Mouse.Up(button, n); err != nil {
			return fmt.Errorf("%s failed: %w", kind, err)
		}
	}
	return nil
}

// humanHover is a hover made with the pointer.
func humanHover(ctx *cmdContext, page *rod.Page, el *rod.Element) error {
	_, err := humanMove(ctx, page, el, newRand())
	return err
}

// shifted holds the characters typed with Shift held: the capitals and the symbols above the
// digits and punctuation.
var shifted = func() map[rune]bool {
	m := map[rune]bool{}
	for r := rune(32); r < 127; r++ {
		if s, ok := input.Key(r).Shift(); ok && s != input.Key(r) {
			m[rune(s)] = true
		}
	}
	return m
}()

// keyFor is the key that types this character on the keyboard rod knows, when it has one.
func keyFor(r rune) (key input.Key, ok bool) {
	if r == '\n' {
		return input.Enter, true
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	input.Key(r).Info()
	return input.Key(r), true
}

// A Spanish keyboard has keys of its own for these; an accented vowel is the dead accent key
// and then the vowel, as the recording shows.
var spanishKeys = map[rune]struct {
	code  string
	shift bool
}{
	'ñ': {"Semicolon", false}, 'Ñ': {"Semicolon", true},
	'¡': {"Equal", false}, '¿': {"Equal", true},
	'ç': {"Backslash", false}, 'Ç': {"Backslash", true},
	'º': {"Backquote", false}, 'ª': {"Backquote", true},
}

var accented = map[rune]rune{
	'á': 'a', 'é': 'e', 'í': 'i', 'ó': 'o', 'ú': 'u',
	'Á': 'A', 'É': 'E', 'Í': 'I', 'Ó': 'O', 'Ú': 'U',
}

// neighbours on a QWERTY row, for the slip of a finger
var neighbours = map[rune]string{
	'q': "wa", 'w': "qes", 'e': "wrd", 'r': "etf", 't': "ryg", 'y': "tuh", 'u': "yij", 'i': "uok",
	'o': "ipl", 'p': "o", 'a': "qsz", 's': "adwx", 'd': "sfec", 'f': "dgrv", 'g': "fhtb",
	'h': "gjyn", 'j': "hkum", 'k': "jli", 'l': "ko", 'z': "xa", 'x': "zcs", 'c': "xvd",
	'v': "cbf", 'b': "vng", 'n': "bmh", 'm': "nj",
}

// typist sends keys with a person's timing: each key held a while, the next one often pressed
// before the last is let go.
type typist struct {
	page    *rod.Page
	rng     *rand.Rand
	held    []heldKey
	nextAt  time.Time
	lastKey rune
}

type heldKey struct {
	up  time.Time
	key *proto.InputDispatchKeyEvent
}

func (t *typist) releaseUntil(at time.Time) error {
	for len(t.held) > 0 && !t.held[0].up.After(at) {
		h := t.held[0]
		t.held = t.held[1:]
		time.Sleep(time.Until(h.up))
		if err := h.key.Call(t.page); err != nil {
			return err
		}
	}
	return nil
}

func (t *typist) releaseAll() error {
	if len(t.held) == 0 {
		return nil
	}
	return t.releaseUntil(t.held[len(t.held)-1].up)
}

// gap is the time from one key to the next, by what the last one was.
func (t *typist) gap(prev rune) time.Duration {
	switch {
	case prev == 0:
		return 0
	case prev == ' ':
		return logNormal(t.rng, 180, 0.25, 90*time.Millisecond, 500*time.Millisecond)
	case strings.ContainsRune(".,;:!?¿¡\n", prev):
		return logNormal(t.rng, 260, 0.6, 100*time.Millisecond, 1500*time.Millisecond)
	default:
		return logNormal(t.rng, 135, 0.5, 50*time.Millisecond, 600*time.Millisecond)
	}
}

func (t *typist) hold() time.Duration {
	return logNormal(t.rng, 115, 0.35, 60*time.Millisecond, 250*time.Millisecond)
}

// press sends one key down at the next moment and schedules its release.
func (t *typist) press(down, up *proto.InputDispatchKeyEvent) error {
	at := t.nextAt
	if err := t.releaseUntil(at); err != nil {
		return err
	}
	time.Sleep(time.Until(at))
	if err := down.Call(t.page); err != nil {
		return err
	}
	upAt := time.Now().Add(t.hold())
	i := len(t.held)
	for i > 0 && t.held[i-1].up.After(upAt) {
		i--
	}
	t.held = append(t.held[:i], append([]heldKey{{upAt, up}}, t.held[i:]...)...)
	return nil
}

func keyEvents(code, key, text string, modifiers int) (*proto.InputDispatchKeyEvent, *proto.InputDispatchKeyEvent) {
	downType := proto.InputDispatchKeyEventTypeKeyDown
	if text == "" {
		downType = proto.InputDispatchKeyEventTypeRawKeyDown
	}
	down := &proto.InputDispatchKeyEvent{Type: downType, Code: code, Key: key, Text: text, UnmodifiedText: text, Modifiers: modifiers}
	up := &proto.InputDispatchKeyEvent{Type: proto.InputDispatchKeyEventTypeKeyUp, Code: code, Key: key, Modifiers: modifiers}
	return down, up
}

// withShift holds Shift across one key, as a person does: Shift first, let go last.
func (t *typist) withShift(code, key, text string) error {
	if err := t.releaseAll(); err != nil {
		return err
	}
	shiftDown, shiftUp := keyEvents("ShiftLeft", "Shift", "", 0)
	time.Sleep(time.Until(t.nextAt))
	if err := shiftDown.Call(t.page); err != nil {
		return err
	}
	t.nextAt = time.Now().Add(logNormal(t.rng, 90, 0.3, 40*time.Millisecond, 200*time.Millisecond))
	down, up := keyEvents(code, key, text, 8)
	if err := t.press(down, up); err != nil {
		return err
	}
	if err := t.releaseAll(); err != nil {
		return err
	}
	sleepBetween(t.rng, 15*time.Millisecond, 60*time.Millisecond)
	return shiftUp.Call(t.page)
}

// typeRune types one character with the keys a person would press for it.
func (t *typist) typeRune(r rune) error {
	t.nextAt = time.Now().Add(t.gap(t.lastKey))
	t.lastKey = r

	if base, ok := accented[r]; ok {
		deadDown, deadUp := keyEvents("Quote", "Dead", "", 0)
		if err := t.press(deadDown, deadUp); err != nil {
			return err
		}
		t.nextAt = time.Now().Add(logNormal(t.rng, 110, 0.3, 50*time.Millisecond, 300*time.Millisecond))
		code := "Key" + string(unicode.ToUpper(base))
		if unicode.IsUpper(r) {
			return t.withShift(code, string(r), string(r))
		}
		down, up := keyEvents(code, string(r), string(r), 0)
		return t.press(down, up)
	}
	if k, ok := spanishKeys[r]; ok {
		if k.shift {
			return t.withShift(k.code, string(r), string(r))
		}
		down, up := keyEvents(k.code, string(r), string(r), 0)
		return t.press(down, up)
	}
	if key, ok := keyFor(r); ok {
		info := key.Info()
		text := ""
		if key.Printable() {
			text = info.Key
		}
		if r == '\n' {
			text = "\r"
		}
		if shifted[r] {
			return t.withShift(info.Code, info.Key, text)
		}
		down, up := keyEvents(info.Code, info.Key, text, 0)
		down.WindowsVirtualKeyCode, up.WindowsVirtualKeyCode = info.KeyCode, info.KeyCode
		return t.press(down, up)
	}
	// an emoji or anything else no key makes: in as text, as the system's picker puts it
	if err := t.releaseAll(); err != nil {
		return err
	}
	time.Sleep(time.Until(t.nextAt))
	return t.page.InsertText(string(r))
}

// backspace takes back one character.
func (t *typist) backspace() error {
	t.nextAt = time.Now().Add(logNormal(t.rng, 95, 0.3, 60*time.Millisecond, 250*time.Millisecond))
	down, up := keyEvents("Backspace", "Backspace", "", 0)
	down.WindowsVirtualKeyCode, up.WindowsVirtualKeyCode = 8, 8
	return t.press(down, up)
}

// slipRate is how often a letter comes out as its neighbour first and is corrected: the
// recording had one correction in eight keys; a fewer of them still reads as a person.
const slipRate = 0.03

// humanType types text key by key with a person's rhythm, now and then hitting a neighbouring
// letter and taking it back.
func humanType(page *rod.Page, text string) error {
	t := &typist{page: page, rng: newRand()}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if near, ok := neighbours[r]; ok && t.rng.Float64() < slipRate {
			slip := []rune(near)[t.rng.IntN(len(near))]
			if err := t.typeRune(slip); err != nil {
				return err
			}
			// sometimes one more goes in before the slip is seen
			extra := 0
			if i+1 < len(runes) && unicode.IsLower(runes[i+1]) && t.rng.Float64() < 0.4 {
				if err := t.typeRune(runes[i+1]); err != nil {
					return err
				}
				extra = 1
			}
			if err := t.releaseAll(); err != nil {
				return err
			}
			sleepBetween(t.rng, 200*time.Millisecond, 500*time.Millisecond)
			for range 1 + extra {
				if err := t.backspace(); err != nil {
					return err
				}
			}
			t.lastKey = 'a'
		}
		if err := t.typeRune(r); err != nil {
			return err
		}
	}
	return t.releaseAll()
}

// press is a click, right-click or double-click: with the pointer when paced, otherwise the
// element's own events, as fast as they go.
func press(ctx *cmdContext, page *rod.Page, el *rod.Element, kind string) error {
	if pacedFor(ctx) {
		return humanPress(ctx, page, el, kind)
	}
	return browse.PressElement(el, kind)
}

// humanFill clicks into the field, empties it and types the value, and answers what the field
// shows afterwards, as browse.FillElement does.
func humanFill(ctx *cmdContext, page *rod.Page, el *rod.Element, value string) (string, error) {
	refuse, err := browse.RefusesInput(el)
	if err != nil {
		return "", err
	}
	if refuse != "" {
		return "", fmt.Errorf("the field is %s — typing into it does nothing", refuse)
	}
	if err := humanPress(ctx, page, el, "click"); err != nil {
		return "", err
	}
	// what it held goes the way a person's select-all and delete would take it
	if _, err := el.Eval(`() => { if (this.select && this.value) { this.select() } }`); err != nil {
		return "", fmt.Errorf("cannot select the field: %w", err)
	}
	if current, _ := browse.FieldValue(el); current != "" {
		t := &typist{page: page, rng: newRand(), nextAt: time.Now()}
		if err := t.backspace(); err != nil {
			return "", err
		}
		if err := t.releaseAll(); err != nil {
			return "", err
		}
	}
	if err := humanType(page, value); err != nil {
		return "", fmt.Errorf("fill failed: %w", err)
	}
	landed, err := browse.FieldValue(el)
	if err != nil {
		return "", err
	}
	if landed == "" && value != "" {
		return "", fmt.Errorf("still empty after typing %q — nothing landed", value)
	}
	return landed, nil
}

// cmdScroll is peek scroll [pixels]: down by that much (negative is up), a screen by default.
// Paced it is trackpad flicks where the pointer rests; otherwise the page jumps.
func cmdScroll(ctx *cmdContext, args []string) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}
	_, vh := viewport(page)
	dy := vh * 0.8
	if len(args) > 0 {
		if dy, err = strconv.ParseFloat(args[0], 64); err != nil {
			return fmt.Errorf("usage: peek scroll [pixels]  (negative is up)")
		}
	}
	if pacedFor(ctx) {
		rng := newRand()
		at := lastPointer(ctx.port, page)
		if err := page.Mouse.MoveTo(at); err != nil {
			return err
		}
		if err := humanScroll(page, at, dy, rng); err != nil {
			return err
		}
	} else if _, err := page.Eval(`dy => window.scrollBy(0, dy)`, dy); err != nil {
		return fmt.Errorf("scroll failed: %w", err)
	}
	fmt.Printf("scrolled %.0f\n", dy)
	return nil
}
