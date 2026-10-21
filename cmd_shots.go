package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// A shot set: one PNG per back-office screen and width, taken so that two runs of the same
// code give the same pixels. `peek compare` reads two of them. See docs/BROWSER_TESTING.md.

// shotSize is one viewport a set is taken at.
type shotSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// shotEntry is one PNG of a set: which screen, at which width, and what went wrong on it.
type shotEntry struct {
	Name  string `json:"name"`
	Width int    `json:"width"`
	File  string `json:"file"`
	// Kind is "menu" (an entry of the back office's menu) or "first" (the first row of a
	// menu entry's list, opened by clicking it).
	Kind   string   `json:"kind"`
	URL    string   `json:"url"`
	Failed bool     `json:"failed,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

// shotManifest is the set's shots.json.
type shotManifest struct {
	Origin  string      `json:"origin"`
	Clock   string      `json:"clock"`
	Sizes   []shotSize  `json:"sizes"`
	Started string      `json:"started"`
	Seconds float64     `json:"seconds"`
	Shots   []shotEntry `json:"shots"`
}

const shotsManifestFile = "shots.json"

// Chrome's own rule for networkIdle: no request in flight for this long.
const networkQuiet = 500 * time.Millisecond

// How long one screen may take to load and settle before it is reported as failed.
const screenTimeout = 30 * time.Second

// How long a click on a list's first row is given to open something.
const firstRowTimeout = 5 * time.Second

var defaultShotSizes = []shotSize{{Width: 1440, Height: 900}, {Width: 390, Height: 844}}

// freezeScript runs before every document's own scripts: the clock starts at the same instant
// on every page and runs from there (code that measures elapsed time keeps working), and
// animations, transitions and the text caret are switched off. CLOCK_MS is replaced.
const freezeScript = `(() => {
	if (window.__peekFrozen) { return }
	window.__peekFrozen = true
	const fixed = CLOCK_MS
	const RealDate = Date
	const started = performance.now()
	const now = () => Math.floor(fixed + performance.now() - started)
	// A function rather than a subclass: Date() called without new answers a string.
	function FrozenDate(...args) {
		if (!new.target) { return new RealDate(now()).toString() }
		return args.length === 0 ? new RealDate(now()) : new RealDate(...args)
	}
	FrozenDate.prototype = RealDate.prototype
	FrozenDate.now = now
	FrozenDate.parse = RealDate.parse
	FrozenDate.UTC = RealDate.UTC
	window.Date = FrozenDate

	// The capture (capture.go) carries errors across navigations; a shot answers for its own
	// screen only, not for what the screen before it threw while unloading.
	window.__peekErrors = []
	window.__peekConsole = []
	sessionStorage.removeItem("__peekErrors")

	// An iframe's load does not bubble, but the capture phase sees it; settleScript asks.
	window.__peekLoadedFrames = new WeakSet()
	document.addEventListener("load", e => {
		if (e.target && e.target.tagName === "IFRAME") { window.__peekLoadedFrames.add(e.target) }
	}, true)

	const css = "*, *::before, *::after { animation-duration: 0s !important; " +
		"animation-delay: 0s !important; transition-duration: 0s !important; " +
		"transition-delay: 0s !important; caret-color: transparent !important; " +
		"scroll-behavior: auto !important }"
	const addStyle = () => {
		const style = document.createElement("style")
		style.textContent = css
		document.documentElement.appendChild(style)
	}
	if (document.documentElement) { addStyle() } else {
		document.addEventListener("DOMContentLoaded", addStyle)
	}
})()`

// settleScript resolves once the page has stopped changing: fonts loaded, every image loaded,
// and two animation frames in a row with no DOM mutation. It rejects after timeoutMs.
const settleScript = `async (timeoutMs) => {
	const deadline = performance.now() + timeoutMs
	await document.fonts.ready
	const pending = Array.from(document.images).filter(img => !img.complete)
	await Promise.all(pending.map(img => new Promise(done => {
		img.addEventListener("load", done)
		img.addEventListener("error", done)
	})))
	const inView = el => {
		const r = el.getBoundingClientRect()
		return r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 &&
			r.top < innerHeight && r.left < innerWidth
	}
	const loading = frame => !window.__peekLoadedFrames.has(frame)
	const frames = Array.from(document.querySelectorAll("iframe")).filter(inView).filter(loading)
	await Promise.all(frames.map(frame => new Promise((done, fail) => {
		frame.addEventListener("load", done)
		setTimeout(() => fail(new Error("the iframe " + frame.src + " did not load")),
			deadline - performance.now())
	})))
	let changed = true
	const observer = new MutationObserver(() => { changed = true })
	observer.observe(document, { subtree: true, childList: true, attributes: true, characterData: true })
	const frame = () => new Promise(done => requestAnimationFrame(done))
	let quietFrames = 0
	while (quietFrames < 2) {
		if (performance.now() > deadline) {
			observer.disconnect()
			throw new Error("the page kept changing for " + timeoutMs + "ms")
		}
		changed = false
		await frame()
		quietFrames = changed ? 0 : quietFrames + 1
	}
	observer.disconnect()
	return true
}`

// firstRowScript clicks the first clickable row of the screen's list and waits for what it
// opens: a new address, or anything in place (a dialog). It answers "" when there is no row.
const firstRowScript = `async (timeoutMs) => {
	const cell = document.querySelector(".DataList tbody tr td.Table-cell--clickable")
	if (!cell) { return "" }
	window.__peekErrors = []
	sessionStorage.removeItem("__peekErrors")
	const before = location.href
	const html = document.body.innerHTML
	cell.click()
	const deadline = performance.now() + timeoutMs
	while (location.href === before && document.body.innerHTML === html) {
		if (performance.now() > deadline) {
			throw new Error("clicking the first row opened nothing in " + timeoutMs + "ms")
		}
		await new Promise(done => requestAnimationFrame(done))
	}
	return location.href
}`

// netTracker follows the requests in flight on a page, from Chrome's Network events.
type netTracker struct {
	mu       sync.Mutex
	inflight map[proto.NetworkRequestID]string
	last     time.Time
}

func startNetTracker(page *rod.Page) (*netTracker, error) {
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return nil, fmt.Errorf("enable the network events: %w", err)
	}

	t := &netTracker{inflight: map[proto.NetworkRequestID]string{}, last: time.Now()}

	// A socket or an event stream stays open as long as the page does; it is not loading.
	// Nor is an iframe's document: Chrome asks for a lazy one out of view and holds the
	// request until it is scrolled to. settleScript waits for the iframes in view.
	wait := page.EachEvent(func(e *proto.NetworkRequestWillBeSent) {
		if e.Type == proto.NetworkResourceTypeWebSocket ||
			e.Type == proto.NetworkResourceTypeEventSource {
			return
		}
		if e.Type == proto.NetworkResourceTypeDocument && e.FrameID != page.FrameID {
			return
		}
		t.mu.Lock()
		t.inflight[e.RequestID] = e.Request.URL
		t.last = time.Now()
		t.mu.Unlock()
	}, func(e *proto.NetworkLoadingFinished) {
		t.finish(e.RequestID)
	}, func(e *proto.NetworkLoadingFailed) {
		t.finish(e.RequestID)
	})
	go wait()

	return t, nil
}

func (t *netTracker) finish(id proto.NetworkRequestID) {
	t.mu.Lock()
	delete(t.inflight, id)
	t.last = time.Now()
	t.mu.Unlock()
}

// waitIdle returns once no request has been in flight for networkQuiet, or fails at the
// deadline naming what was still loading.
func (t *netTracker) waitIdle(deadline time.Time) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		t.mu.Lock()
		count := len(t.inflight)
		quiet := time.Since(t.last)
		var loading []string
		if count > 0 && time.Now().After(deadline) {
			for _, u := range t.inflight {
				loading = append(loading, u)
			}
		}
		t.mu.Unlock()

		if count == 0 && quiet >= networkQuiet {
			return nil
		}
		if time.Now().After(deadline) {
			if len(loading) > 0 {
				return fmt.Errorf("still loading after %s: %s", screenTimeout,
					strings.Join(loading, ", "))
			}
			return fmt.Errorf("the network never went quiet in %s", screenTimeout)
		}
		<-ticker.C
	}
}

func cmdShots(ctx *cmdContext, args []string) error {
	dir := ""
	sizes := defaultShotSizes
	clock := ""
	only := ""
	var also []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--sizes":
			if i+1 >= len(args) {
				return fmt.Errorf("--sizes requires a value, like 1440x900,390x844")
			}
			i++
			parsed, err := parseShotSizes(args[i])
			if err != nil {
				return err
			}
			sizes = parsed
		case "--clock":
			if i+1 >= len(args) {
				return fmt.Errorf("--clock requires a value, like 2026-10-10T10:00")
			}
			i++
			clock = args[i]
		case "--only":
			if i+1 >= len(args) {
				return fmt.Errorf("--only requires a value: parts of the screens' addresses, comma separated")
			}
			i++
			only = args[i]
		case "--also":
			if i+1 >= len(args) {
				return fmt.Errorf("--also requires a value: addresses the menu does not list, " +
					"comma separated, like /subscriptions/home,/subscriptions/forecast")
			}
			i++
			for _, a := range strings.Split(args[i], ",") {
				if !strings.HasPrefix(a, "/") {
					return fmt.Errorf("--also takes addresses of the back office, like "+
						"/subscriptions/home: %q", a)
				}
				also = append(also, a)
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %s", args[i])
			}
			if dir != "" {
				return fmt.Errorf("one directory only: %s and %s", dir, args[i])
			}
			dir = args[i]
		}
	}
	if dir == "" {
		return fmt.Errorf("usage: peek shots <dir> [--sizes 1440x900,390x844] " +
			"[--clock 2026-10-10T10:00] [--only <parts of addresses, comma separated>] [--also <addresses>]")
	}

	clockTime, err := parseShotClock(clock)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	// A tab Chrome holds hidden paints no frame, and every screen would time out waiting
	// for one.
	if err := (proto.PageBringToFront{}).Call(page); err != nil {
		return fmt.Errorf("bring the tab to the front: %w", err)
	}
	visibility, err := page.Eval(`() => document.visibilityState`)
	if err != nil {
		return fmt.Errorf("read whether the tab is visible: %w", err)
	}
	if visibility.Value.Str() != "visible" {
		return fmt.Errorf("the tab is %s and paints nothing: take the set in a browser "+
			"opened with peek --headless open", visibility.Value.Str())
	}

	origin, mount, paths, err := readBackOfficeMenu(page)
	if err != nil {
		return err
	}
	if only != "" {
		var kept []string
		parts := strings.Split(only, ",")
		for _, p := range paths {
			for _, part := range parts {
				if strings.Contains(p, part) {
					kept = append(kept, p)
					break
				}
			}
		}
		paths = kept
	}
	paths = append(paths, also...)
	if len(paths) == 0 {
		return fmt.Errorf("the back office's menu has no screen to capture")
	}

	clockMs := clockTime.UnixMilli()
	clockLiteral := strconv.FormatInt(clockMs, 10)
	freeze := strings.Replace(freezeScript, "CLOCK_MS", clockLiteral, 1)
	removeFreeze, err := page.EvalOnNewDocument(freeze)
	if err != nil {
		return fmt.Errorf("install the frozen clock: %w", err)
	}
	defer func() {
		if err := removeFreeze(); err != nil {
			fmt.Fprintf(os.Stderr, "peek: remove the frozen clock: %v\n", err)
		}
	}()

	tracker, err := startNetTracker(page)
	if err != nil {
		return err
	}

	started := time.Now()
	manifest := shotManifest{
		Origin:  origin,
		Clock:   clockTime.Format(time.RFC3339),
		Sizes:   sizes,
		Started: started.Format(time.RFC3339),
	}

	// The browser is left at the viewport it had.
	defer func() {
		if err := (proto.EmulationClearDeviceMetricsOverride{}).Call(page); err != nil {
			fmt.Fprintf(os.Stderr, "peek: clear the viewport: %v\n", err)
		}
		if err := applyViewport(ctx.port, page); err != nil {
			fmt.Fprintf(os.Stderr, "peek: %v\n", err)
		}
	}()

	failed := 0
	for _, size := range sizes {
		widthDir := filepath.Join(dir, strconv.Itoa(size.Width))
		if err := os.MkdirAll(widthDir, 0755); err != nil {
			return fmt.Errorf("create %s: %w", widthDir, err)
		}

		err := proto.EmulationSetDeviceMetricsOverride{
			Width: size.Width, Height: size.Height, DeviceScaleFactor: 1, Mobile: false,
		}.Call(page)
		if err != nil {
			return fmt.Errorf("set the viewport to %dx%d: %w", size.Width, size.Height, err)
		}

		for _, p := range paths {
			address := origin + mount + p
			name := shotName(p)

			entry, hasList := captureScreen(page, tracker, address, name, size.Width, dir)
			manifest.Shots = append(manifest.Shots, entry)
			report(entry)
			if entry.Failed {
				failed++
			}
			if !hasList {
				continue
			}

			first, opened := captureFirstRow(page, tracker, name, size.Width, dir)
			if !opened {
				continue
			}
			manifest.Shots = append(manifest.Shots, first)
			report(first)
			if first.Failed {
				failed++
			}
		}
	}

	manifest.Seconds = time.Since(started).Round(100 * time.Millisecond).Seconds()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the manifest: %w", err)
	}
	manifestPath := filepath.Join(dir, shotsManifestFile)
	if err := writeFile(manifestPath, data); err != nil {
		return err
	}

	fmt.Printf("%d shots of %d screens at %d sizes in %.1fs → %s\n", len(manifest.Shots),
		len(paths), len(sizes), manifest.Seconds, dir)
	if failed > 0 {
		return fmt.Errorf("%d shots failed (listed above and in %s)", failed, manifestPath)
	}
	return nil
}

func report(entry shotEntry) {
	if !entry.Failed {
		fmt.Printf("ok      %4d %s\n", entry.Width, entry.Name)
		return
	}
	fmt.Printf("FAILED  %4d %s  %s\n", entry.Width, entry.Name, entry.URL)
	for _, e := range entry.Errors {
		fmt.Printf("          %s\n", e)
	}
}

// captureScreen loads one address fresh, waits until it is settled and saves its PNG. It
// answers whether the screen shows a list with a row to open.
func captureScreen(page *rod.Page, tracker *netTracker, address, name string, width int,
	dir string) (shotEntry, bool) {
	// Every call on the page shares the screen's deadline: a page that never answers fails
	// this shot instead of hanging the set.
	page = page.Timeout(screenTimeout)
	defer page.CancelTimeout()

	widthName := strconv.Itoa(width)
	entry := shotEntry{
		Name:  name,
		Width: width,
		File:  filepath.Join(widthName, name+".png"),
		Kind:  "menu",
		URL:   address,
	}

	fail := func(err error) (shotEntry, bool) {
		entry.Failed = true
		entry.Errors = append(entry.Errors, err.Error())
		return entry, false
	}

	deadline := time.Now().Add(screenTimeout)
	if err := page.Navigate(address); err != nil {
		return fail(fmt.Errorf("navigate: %w", err))
	}
	if err := page.WaitLoad(); err != nil {
		return fail(fmt.Errorf("wait for the document: %w", err))
	}

	status, err := page.Eval(`() => {
		const nav = performance.getEntriesByType("navigation")[0]
		return nav && nav.responseStatus !== undefined ? nav.responseStatus : 0
	}`)
	if err != nil {
		return fail(fmt.Errorf("read the answer's status: %w", err))
	}
	code := status.Value.Int()
	if code >= 400 {
		return fail(fmt.Errorf("the server answered %d", code))
	}

	if err := settle(page, tracker, deadline); err != nil {
		return fail(err)
	}

	pageErrors, err := readPageErrors(page)
	if err != nil {
		return fail(err)
	}
	if len(pageErrors) > 0 {
		entry.Failed = true
		entry.Errors = pageErrors
	}

	shotPath := filepath.Join(dir, entry.File)
	if err := saveShot(page, shotPath); err != nil {
		return fail(err)
	}

	hasRow, err := page.Eval(`() => document.querySelector(
		".DataList tbody tr td.Table-cell--clickable") !== null`)
	if err != nil {
		return fail(fmt.Errorf("look for a list: %w", err))
	}
	return entry, hasRow.Value.Bool()
}

// captureFirstRow clicks the first row of the list on screen and saves what it opens, as
// "<name>.first". It answers false when the screen has no row after all.
func captureFirstRow(page *rod.Page, tracker *netTracker, listName string, width int,
	dir string) (shotEntry, bool) {
	page = page.Timeout(screenTimeout)
	defer page.CancelTimeout()

	name := listName + ".first"
	widthName := strconv.Itoa(width)
	entry := shotEntry{
		Name:  name,
		Width: width,
		File:  filepath.Join(widthName, name+".png"),
		Kind:  "first",
	}

	fail := func(err error) (shotEntry, bool) {
		entry.Failed = true
		entry.Errors = append(entry.Errors, err.Error())
		return entry, true
	}

	deadline := time.Now().Add(screenTimeout)
	timeoutMs := firstRowTimeout.Milliseconds()
	opened, err := page.Eval(firstRowScript, timeoutMs)
	if err != nil {
		return fail(fmt.Errorf("open the first row: %w", err))
	}
	entry.URL = opened.Value.Str()
	if entry.URL == "" {
		return entry, false
	}

	if err := settle(page, tracker, deadline); err != nil {
		return fail(err)
	}

	pageErrors, err := readPageErrors(page)
	if err != nil {
		return fail(err)
	}
	if len(pageErrors) > 0 {
		entry.Failed = true
		entry.Errors = pageErrors
	}

	shotPath := filepath.Join(dir, entry.File)
	if err := saveShot(page, shotPath); err != nil {
		return fail(err)
	}
	return entry, true
}

// settle waits for the network to go quiet and then for the page to stop changing.
func settle(page *rod.Page, tracker *netTracker, deadline time.Time) error {
	if err := tracker.waitIdle(deadline); err != nil {
		return err
	}
	left := time.Until(deadline)
	if left <= 0 {
		return fmt.Errorf("did not settle in %s", screenTimeout)
	}
	leftMs := left.Milliseconds()
	if _, err := page.Eval(settleScript, leftMs); err != nil {
		return fmt.Errorf("wait for the page to settle: %w", err)
	}
	return nil
}

// readPageErrors is what the screen threw or logged as an error, and the notice it opened when it
// said a failure in an alert instead (role="alertdialog"): a screen that caught its exception and
// showed it is as broken as one that threw it.
func readPageErrors(page *rod.Page) ([]string, error) {
	res, err := page.Eval(`() => {
		const errors = (window.__peekErrors || []).map(e => e.text)
		for (const alert of document.querySelectorAll('[role="alertdialog"]')) {
			if (alert.checkVisibility()) {
				errors.push("an alert is open: " + alert.innerText.replace(/\s+/g, " ").trim())
			}
		}
		return errors
	}`)
	if err != nil {
		return nil, fmt.Errorf("read the page's errors: %w", err)
	}
	var texts []string
	if err := res.Value.Unmarshal(&texts); err != nil {
		return nil, fmt.Errorf("read the page's errors: %w", err)
	}
	return texts, nil
}

// saveShot takes screenshots until two in a row are the same, and saves that one: what the
// DOM cannot tell (a picture resampled, a font swapped inside a sandboxed iframe) the pixels do.
func saveShot(page *rod.Page, path string) error {
	shoot := func() ([]byte, error) {
		if _, err := page.Eval(`() => new Promise(done =>
			requestAnimationFrame(() => requestAnimationFrame(done)))`); err != nil {
			return nil, fmt.Errorf("wait for a frame: %w", err)
		}
		data, err := page.Screenshot(false, &proto.PageCaptureScreenshot{
			Format: proto.PageCaptureScreenshotFormatPng,
		})
		if err != nil {
			return nil, fmt.Errorf("take the screenshot: %w", err)
		}
		return data, nil
	}

	previous, err := shoot()
	if err != nil {
		return err
	}
	for {
		current, err := shoot()
		if err != nil {
			return fmt.Errorf("the screen kept changing: %w", err)
		}
		if bytes.Equal(previous, current) {
			return writeFile(path, current)
		}
		previous = current
	}
}

// readBackOfficeMenu asks the signed-in page for the menu of the realm it is on (/api/init for
// the tab's own address: the back office at /admin, the platform's console at /platform; the
// sidebar, the foot hubs and the account entries) and answers the origin, the mount and every
// address in it, in menu order and without repeats.
func readBackOfficeMenu(page *rod.Page) (string, string, []string, error) {
	res, err := page.Eval(`async () => {
		if (!location.protocol.startsWith("http")) {
			throw new Error("the tab is on " + location.href + ", not on a back office")
		}
		const answer = await fetch("/api/init?path=" + encodeURIComponent(location.pathname),
			{ credentials: "same-origin" })
		if (!answer.ok) { throw new Error("/api/init answered " + answer.status) }
		const init = await answer.json()
		return { origin: location.origin, init: init }
	}`)
	if err != nil {
		return "", "", nil, fmt.Errorf("read the back office's menu: %w", err)
	}

	var boot struct {
		Origin string `json:"origin"`
		Init   struct {
			Mount string          `json:"mount"`
			User  json.RawMessage `json:"user"`
			Nav   struct {
				Sidebar []struct {
					Items []navEntry `json:"items"`
				} `json:"sidebar"`
				Foot    []navEntry `json:"foot"`
				Account []navEntry `json:"account"`
			} `json:"nav"`
		} `json:"init"`
	}
	if err := res.Value.Unmarshal(&boot); err != nil {
		return "", "", nil, fmt.Errorf("read the back office's menu: %w", err)
	}
	if len(boot.Init.User) == 0 || string(boot.Init.User) == "null" {
		return "", "", nil, fmt.Errorf("the browser is not signed in to a back office at %s: "+
			"open a browser-session link first (docs/BROWSER.md)", boot.Origin)
	}

	var entries []navEntry
	for _, section := range boot.Init.Nav.Sidebar {
		entries = append(entries, section.Items...)
	}
	entries = append(entries, boot.Init.Nav.Foot...)
	entries = append(entries, boot.Init.Nav.Account...)

	seen := map[string]bool{}
	var paths []string
	for _, e := range entries {
		paths = e.collectPaths(paths, seen)
	}
	return boot.Origin, boot.Init.Mount, paths, nil
}

// navEntry is a menu entry as /api/init serves it: its own address and its children, flat,
// grouped or in columns.
type navEntry struct {
	Path    string     `json:"path"`
	Website bool       `json:"website"`
	Items   []navEntry `json:"items"`
	Groups  []navGroup `json:"groups"`
	Columns []struct {
		Items  []navEntry `json:"items"`
		Groups []navGroup `json:"groups"`
	} `json:"columns"`
}

type navGroup struct {
	Items []navEntry `json:"items"`
}

func (e navEntry) collectPaths(paths []string, seen map[string]bool) []string {
	// The website entry opens the tenant's site in a new tab: it is not a back-office screen.
	if e.Path != "" && !e.Website && !seen[e.Path] {
		seen[e.Path] = true
		paths = append(paths, e.Path)
	}
	var children []navEntry
	children = append(children, e.Items...)
	for _, g := range e.Groups {
		children = append(children, g.Items...)
	}
	for _, c := range e.Columns {
		children = append(children, c.Items...)
		for _, g := range c.Groups {
			children = append(children, g.Items...)
		}
	}
	for _, child := range children {
		paths = child.collectPaths(paths, seen)
	}
	return paths
}

var unsafeNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// shotName turns a screen's address into a file name: /billing/document → billing.document,
// /user/1 → user.1, a query's characters into "_".
func shotName(path string) string {
	trimmed := strings.Trim(path, "/")
	parts := strings.Split(trimmed, "/")
	var kept []string
	for _, part := range parts {
		safe := unsafeNameChars.ReplaceAllString(part, "_")
		if safe != "" {
			kept = append(kept, safe)
		}
	}
	if len(kept) == 0 {
		return "home"
	}
	return strings.Join(kept, ".")
}

func parseShotSizes(spec string) ([]shotSize, error) {
	var sizes []shotSize
	for _, part := range strings.Split(spec, ",") {
		var w, h int
		if _, err := fmt.Sscanf(part, "%dx%d", &w, &h); err != nil || w <= 0 || h <= 0 {
			return nil, fmt.Errorf("invalid size %q: it is <width>x<height>, like 1440x900", part)
		}
		sizes = append(sizes, shotSize{Width: w, Height: h})
	}
	return sizes, nil
}

// parseShotClock reads --clock; without one the pages see today at 10:00 local time, so two
// sets taken the same day agree and the data around today still shows.
func parseShotClock(clock string) (time.Time, error) {
	if clock == "" {
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, time.Local), nil
	}
	parsed, err := time.ParseInLocation("2006-01-02T15:04", clock, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --clock %q: it is like 2026-10-10T10:00", clock)
	}
	return parsed, nil
}
