package browse

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// The suite's browser: ONE headless Chrome for every test here. Each test sets the page's
// own content before pressing anything, so they share it without seeing one another.
var (
	testChrome *Chrome
	testPage   *rod.Page
	noChrome   string // why there is no browser to test on, or ""
)

func TestMain(m *testing.M) {
	os.Exit(runSuite(m))
}

// runSuite launches the Chrome the presses are tested on, runs the tests and closes it.
func runSuite(m *testing.M) int {
	if _, err := FindChromePath(); err != nil {
		noChrome = err.Error()
		return m.Run()
	}

	chrome, err := Launch(LaunchOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer chrome.Close()
	testChrome = chrome

	page, err := chrome.Browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open a page: %v\n", err)
		return 1
	}
	testPage = page

	return m.Run()
}

// chromePage is the page a test presses on, or a skip when this machine has no Chrome.
func chromePage(t *testing.T) *rod.Page {
	t.Helper()

	if noChrome != "" {
		t.Skipf("no Chrome on this machine: %s", noChrome)
	}
	return testPage
}

// watchPress puts html in the page and records, in window.hits, every event a press
// produces on the element with id "target".
func watchPress(t *testing.T, page *rod.Page, html string) {
	t.Helper()

	if err := page.SetDocumentContent(html); err != nil {
		t.Fatalf("cannot set the page content: %v", err)
	}

	// The listeners go in from here rather than in a <script>: setDocumentContent does not
	// run the scripts it carries.
	if _, err := page.Eval(`() => {
		window.hits = []
		const el = document.getElementById("target")
		for (const type of ["pointerdown", "mousedown", "pointerup", "mouseup", "click", "dblclick"]) {
			el.addEventListener(type, e => window.hits.push(e.type))
		}
	}`); err != nil {
		t.Fatalf("cannot watch the element: %v", err)
	}
}

func pressHits(t *testing.T, page *rod.Page) string {
	t.Helper()

	res, err := page.Eval(`() => window.hits.join(" ")`)
	if err != nil {
		t.Fatalf("cannot read what the element got: %v", err)
	}
	return res.Value.Str()
}

// An icon button is an <svg>, and SVGSVGElement has no click() — the whole top bar of the
// back office (bell, help, settings, history) is one. The press used to dispatch its pointer
// and mouse events and then die on the native call: half a press, reported as a failure.
func TestPressElementClicksAnSVGWithNoNativeClick(t *testing.T) {
	page := chromePage(t)
	watchPress(t, page, `<html><body><svg id="target" width="24" height="24">`+
		`<circle cx="12" cy="12" r="10"></circle></svg></body></html>`)

	el, err := FindElement(page, ElementQuery{CSS: "#target", Index: -1})
	if err != nil {
		t.Fatal(err)
	}

	if err := PressElement(el, "click"); err != nil {
		t.Fatalf("clicking an svg failed: %v", err)
	}

	if got := pressHits(t, page); got != "pointerdown mousedown pointerup mouseup click" {
		t.Fatalf("the svg got %q", got)
	}
}

func TestPressElementDoubleClicksAnSVGWithNoNativeClick(t *testing.T) {
	page := chromePage(t)
	watchPress(t, page, `<html><body><svg id="target" width="24" height="24">`+
		`<circle cx="12" cy="12" r="10"></circle></svg></body></html>`)

	el, err := FindElement(page, ElementQuery{CSS: "#target", Index: -1})
	if err != nil {
		t.Fatal(err)
	}

	if err := PressElement(el, "dblclick"); err != nil {
		t.Fatalf("double-clicking an svg failed: %v", err)
	}

	got := pressHits(t, page)
	if !strings.HasSuffix(got, "click click dblclick") {
		t.Fatalf("the svg got %q — a double click is two clicks and a dblclick", got)
	}
}

// The ordinary button gets one click event, not two.
func TestPressElementStillClicksAButtonOnce(t *testing.T) {
	page := chromePage(t)
	watchPress(t, page, `<html><body><button id="target">Save</button></body></html>`)

	el, err := FindElement(page, ElementQuery{CSS: "#target", Index: -1})
	if err != nil {
		t.Fatal(err)
	}

	if err := PressElement(el, "click"); err != nil {
		t.Fatalf("clicking a button failed: %v", err)
	}

	if got := pressHits(t, page); got != "pointerdown mousedown pointerup mouseup click" {
		t.Fatalf("the button got %q", got)
	}
}

// A Canvas tells a mouse from a finger by the pointer's kind and places a tap where the click
// landed: the press is a mouse's, and every event of it, the click too, lands on the middle
// of the element — not at the page's corner, where the native click() put it.
func TestPressElementIsAMouseAtTheElementsMiddle(t *testing.T) {
	page := chromePage(t)
	if err := page.SetDocumentContent(`<html><body style="margin:0">` +
		`<div id="target" style="position:absolute;left:100px;top:40px;width:200px;height:60px">` +
		`</div></body></html>`); err != nil {
		t.Fatalf("cannot set the page content: %v", err)
	}
	if _, err := page.Eval(`() => {
		window.hits = []
		const el = document.getElementById("target")
		for (const type of ["pointerdown", "pointerup", "click"]) {
			el.addEventListener(type, e => window.hits.push(
				e.type + ":" + (e.pointerType ?? "") + ":" + e.clientX + "," + e.clientY))
		}
	}`); err != nil {
		t.Fatalf("cannot watch the element: %v", err)
	}

	el, err := FindElement(page, ElementQuery{CSS: "#target", Index: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := PressElement(el, "click"); err != nil {
		t.Fatalf("clicking failed: %v", err)
	}

	want := "pointerdown:mouse:200,70 pointerup:mouse:200,70 click::200,70"
	if got := pressHits(t, page); got != want {
		t.Fatalf("the element got %q, want %q", got, want)
	}
}

// The dispatched click still carries the activation behaviour the native one did.
func TestPressElementTogglesACheckboxAndSubmitsAForm(t *testing.T) {
	page := chromePage(t)
	if err := page.SetDocumentContent(`<html><body><form id="f">` +
		`<input type="checkbox" id="box"><button id="go">Go</button></form></body></html>`); err != nil {
		t.Fatalf("cannot set the page content: %v", err)
	}
	if _, err := page.Eval(`() => {
		window.submitted = false
		document.getElementById("f").addEventListener("submit", e => {
			e.preventDefault()
			window.submitted = true
		})
	}`); err != nil {
		t.Fatalf("cannot watch the form: %v", err)
	}

	for _, css := range []string{"#box", "#go"} {
		el, err := FindElement(page, ElementQuery{CSS: css, Index: -1})
		if err != nil {
			t.Fatal(err)
		}
		if err := PressElement(el, "click"); err != nil {
			t.Fatalf("clicking %s failed: %v", css, err)
		}
	}

	res, err := page.Eval(`() => document.getElementById("box").checked + " " + window.submitted`)
	if err != nil {
		t.Fatalf("cannot read the form: %v", err)
	}
	if got := res.Value.Str(); got != "true true" {
		t.Fatalf("checked and submitted = %q, want \"true true\"", got)
	}
}

// A widget that saves its value on blur only saves what was typed if the press takes the focus
// away, as a mouse does: a press on a plain row or label left the field focused, the field
// never got its blur, and Save went out with the old value.
func TestPressElementTakesTheFocusAwayFromTheFieldBeingEdited(t *testing.T) {
	page := chromePage(t)
	if err := page.SetDocumentContent(`<html><body><input id="field">` +
		`<div id="target">Save</div><div id="kept">Keep</div><button id="button">Go</button>` +
		`</body></html>`); err != nil {
		t.Fatal(err)
	}
	if _, err := page.Eval(`() => {
		window.blurred = 0
		document.getElementById("field").addEventListener("blur", () => window.blurred++)
		document.getElementById("kept").addEventListener("mousedown", e => e.preventDefault())
	}`); err != nil {
		t.Fatal(err)
	}

	focusField := func() {
		if _, err := page.Eval(`() => document.getElementById("field").focus()`); err != nil {
			t.Fatal(err)
		}
	}
	active := func() string {
		res, err := page.Eval(`() => document.activeElement.id || document.activeElement.tagName`)
		if err != nil {
			t.Fatal(err)
		}
		return res.Value.Str()
	}
	press := func(id string) {
		el, err := FindElement(page, ElementQuery{CSS: "#" + id, Index: -1})
		if err != nil {
			t.Fatal(err)
		}
		if err := PressElement(el, "click"); err != nil {
			t.Fatal(err)
		}
	}

	focusField()
	press("target")
	if got := active(); got != "BODY" {
		t.Fatalf("after pressing a plain div the focus is on %q, want the body", got)
	}

	focusField()
	press("button")
	if got := active(); got != "button" {
		t.Fatalf("after pressing a button the focus is on %q, want the button", got)
	}

	// a mousedown that prevents its default keeps the focus where it was, as in the browser
	focusField()
	press("kept")
	if got := active(); got != "field" {
		t.Fatalf("after pressing what prevents the mousedown the focus is on %q, want the field", got)
	}
}
