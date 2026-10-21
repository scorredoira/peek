package browse

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A page's errors are what its documents said while they were on screen. What one says while
// it is being left — the browser cuts its requests as Open goes elsewhere, and the page's
// catch reports it — is not kept; what it said before, and what the next one says, are.
func TestPageKeepsNoErrorFromADocumentItLeft(t *testing.T) {
	if noChrome != "" {
		t.Skipf("no Chrome on this machine: %s", noChrome)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/slow":
			// answers only once the browser has given up on it
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
		case "/first":
			fmt.Fprint(w, `<script>console.error("before leaving")
fetch("/slow").catch(e => console.error("cut while leaving: " + e))</script><p>first</p>`)
		default:
			fmt.Fprint(w, `<script>console.error("on the next page")</script><p>next</p>`)
		}
	}))
	defer server.Close()

	page, err := testChrome.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer page.page.Close()

	if err := page.Open(server.URL + "/first"); err != nil {
		t.Fatal(err)
	}
	if err := page.Wait("first"); err != nil {
		t.Fatal(err)
	}
	if err := page.Open(server.URL + "/next"); err != nil {
		t.Fatal(err)
	}
	if err := page.Wait("next"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for len(page.Errors()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	got := strings.Join(page.Errors(), " | ")
	if got != "before leaving | on the next page" {
		t.Fatalf("the page kept %q", got)
	}
}

// A menu fades in: for its first frames its items are transparent. A press waits for it, as a
// person does; what stays transparent is refused.
func TestClickWaitsForWhatIsFadingIn(t *testing.T) {
	if noChrome != "" {
		t.Skipf("no Chrome on this machine: %s", noChrome)
	}

	page, err := testChrome.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer page.page.Close()

	err = page.page.SetDocumentContent(`<button id="fading" style="opacity:0">Delete page</button>` +
		`<button style="opacity:0">Never shown</button>`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.page.Eval(`() => {
		const button = document.getElementById("fading")
		button.addEventListener("click", () => { window.pressed = true })
		setTimeout(() => { button.style.opacity = "1" }, 300)
	}`); err != nil {
		t.Fatal(err)
	}

	if err := page.Click("Delete page"); err != nil {
		t.Fatalf("the button that faded in was not pressed: %v", err)
	}
	pressed, err := page.page.Eval(`() => window.pressed === true`)
	if err != nil || !pressed.Value.Bool() {
		t.Fatalf("the press did not reach the button: %v", err)
	}

	err = page.Click("Never shown")
	if err == nil || !strings.Contains(err.Error(), "fully transparent") {
		t.Fatalf("a button that stays transparent must be refused, got %v", err)
	}
}

// A field named in another language for the screen reader is found by the words written beside
// it, as a person finds it.
func TestFillFindsAFieldByTheWordsBesideIt(t *testing.T) {
	if noChrome != "" {
		t.Skipf("no Chrome on this machine: %s", noChrome)
	}

	page, err := testChrome.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer page.page.Close()

	err = page.page.SetDocumentContent(`<div><span>Nombre&nbsp;del paquete</span></div>` +
		`<div><input id="package" aria-label="App package name"></div><input id="other">`)
	if err != nil {
		t.Fatal(err)
	}
	if err := page.Fill("Nombre del paquete", "com.example.app"); err != nil {
		t.Fatal(err)
	}
	filled, err := page.Eval(`() => document.getElementById("package").value + "|" + document.getElementById("other").value`)
	if err != nil {
		t.Fatal(err)
	}
	if filled != "com.example.app|" {
		t.Fatalf("the fields hold %q", filled)
	}

	err = page.page.SetDocumentContent(`<label><span>Nombre</span><span> *</span></label><input id="name">`)
	if err != nil {
		t.Fatal(err)
	}
	if err := page.Fill("Nombre *", "Club staff"); err != nil {
		t.Fatal(err)
	}
	named, err := page.Eval(`() => document.getElementById("name").value`)
	if err != nil {
		t.Fatal(err)
	}
	if named != "Club staff" {
		t.Fatalf("the required field holds %q", named)
	}

	if err := page.Fill("Nowhere written", "x"); err == nil {
		t.Fatal("a field nothing names must not be found")
	}
}

// A radio a design library draws itself is a transparent input under its drawing: pressing its
// words picks it. A button left transparent is still refused (TestClickWaitsForWhatIsFadingIn).
func TestClickPicksARadioDrawnOverItsInput(t *testing.T) {
	if noChrome != "" {
		t.Skipf("no Chrome on this machine: %s", noChrome)
	}

	page, err := testChrome.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer page.page.Close()

	err = page.page.SetDocumentContent(`<div style="position:relative;width:20px;height:20px">` +
		`<input type="radio" name="kind" id="free" aria-labelledby="free-words" ` +
		`style="opacity:0;position:absolute;inset:0;margin:0">` +
		`<div style="position:absolute;inset:0;border:2px solid;border-radius:50%"></div></div>` +
		`<span id="free-words">Sin coste</span>`)
	if err != nil {
		t.Fatal(err)
	}
	if err := page.Click("Sin coste"); err != nil {
		t.Fatal(err)
	}
	picked, err := page.Eval(`() => document.getElementById("free").checked`)
	if err != nil {
		t.Fatal(err)
	}
	if picked != true {
		t.Fatalf("the radio is not picked: %v", picked)
	}
}

// A design library's dropdown is a button named by its label and then what is chosen: it is
// found by the label, its required mark aside, and a button that only holds the words further
// on is not taken for it.
func TestFindDropdownTriggerByItsLabel(t *testing.T) {
	if noChrome != "" {
		t.Skipf("no Chrome on this machine: %s", noChrome)
	}

	page, err := testChrome.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer page.page.Close()

	err = page.page.SetDocumentContent(`<button id="other">Ver Categoría</button>` +
		`<button id="trigger" aria-label="Categoría Campo obligatorio No seleccionado">▾</button>`)
	if err != nil {
		t.Fatal(err)
	}
	el, err := FindDropdownTrigger(page.page, "Categoría *")
	if err != nil {
		t.Fatal(err)
	}
	id, err := el.Attribute("id")
	if err != nil || id == nil || *id != "trigger" {
		t.Fatalf("found %v (%v)", id, err)
	}

	if _, err := FindDropdownTrigger(page.page, "Idioma"); err == nil {
		t.Fatal("a label nothing starts with must not be found")
	}
}
