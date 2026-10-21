package browse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// waitTimeout is how long Wait looks for a text before it gives up.
const waitTimeout = 10 * time.Second

// Page is one tab a program drives the way a person would: it opens an address, presses what
// carries a text, types into the field a label names and waits for a text to be on screen. It
// finds and presses through the same code peek does (find.go), so a press a person could not
// make — on something covered, disabled or hidden — is refused here too.
//
// What the page itself reports — an uncaught exception, a console error — is kept, so a
// program can ask for it: a screen that throws while loading paints half of itself and
// passes a test that only looks for a text. What a document says once it has been left (Open
// went elsewhere) is not kept: the browser cuts its requests as it goes, and that is the
// leaving, not a fault of the screen.
type Page struct {
	page *rod.Page

	mu     sync.Mutex
	errors []string
	// contexts are the documents' script contexts the tab has had; left, the ones Open went
	// away from
	contexts []proto.RuntimeExecutionContextID
	left     map[proto.RuntimeExecutionContextID]bool
}

// NewPage opens a blank tab and starts keeping what it reports.
func (c *Chrome) NewPage() (*Page, error) {
	page, err := c.Browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("cannot open a tab: %w", err)
	}

	return c.watch(page), nil
}

// SetCookie gives every page of host a cookie, as the server would have set it.
func (c *Chrome) SetCookie(host, name, value string) error {
	cookie := &proto.NetworkCookieParam{Name: name, Value: value, URL: "http://" + host + "/"}
	return proto.StorageSetCookies{Cookies: []*proto.NetworkCookieParam{cookie}}.Call(c.Browser)
}

// watch wraps a tab and keeps what it reports.
func (c *Chrome) watch(page *rod.Page) *Page {
	p := &Page{page: page, left: map[proto.RuntimeExecutionContextID]bool{}}
	go page.EachEvent(func(e *proto.RuntimeExecutionContextCreated) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.contexts = append(p.contexts, e.Context.ID)
	}, func(e *proto.RuntimeExceptionThrown) {
		text := e.ExceptionDetails.Text
		if e.ExceptionDetails.Exception != nil && e.ExceptionDetails.Exception.Description != "" {
			text = e.ExceptionDetails.Exception.Description
		}
		p.keep(e.ExceptionDetails.ExecutionContextID, text)
	}, func(e *proto.RuntimeConsoleAPICalled) {
		if e.Type != proto.RuntimeConsoleAPICalledTypeError {
			return
		}
		var parts []string
		for _, arg := range e.Args {
			if arg.Description != "" {
				parts = append(parts, arg.Description)
				continue
			}
			parts = append(parts, arg.Value.String())
		}
		p.keep(e.ExecutionContextID, strings.Join(parts, " "))
	})()

	return p
}

func (p *Page) keep(context proto.RuntimeExecutionContextID, text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.left[context] {
		return
	}
	p.errors = append(p.errors, text)
}

// Errors is everything the page threw or logged as an error, oldest first.
func (p *Page) Errors() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.errors...)
}

// Open goes to an address and waits for the page to load. The document it leaves says nothing
// more that is kept.
func (p *Page) Open(url string) error {
	p.mu.Lock()
	for _, context := range p.contexts {
		p.left[context] = true
	}
	p.mu.Unlock()

	if err := p.page.Navigate(url); err != nil {
		return fmt.Errorf("cannot open %s: %w", url, err)
	}
	if err := p.page.WaitLoad(); err != nil {
		return fmt.Errorf("%s did not load: %w", url, err)
	}
	return nil
}

// Click presses what a person would mean by text: a button, a link, a tab.
func (p *Page) Click(text string) error {
	el, err := FindByText(p.page, text)
	if err != nil {
		return err
	}
	desc := fmt.Sprintf("%q", text)
	if err := RefuseUnclickable(el, desc); err != nil {
		return err
	}
	return PressElement(el, "click")
}

// Fill types value into the field its label names.
func (p *Page) Fill(label, value string) error {
	el, err := FindInput(p.page, label)
	if err != nil {
		return err
	}
	if _, err := FillElement(p.page, el, value); err != nil {
		return fmt.Errorf("%q: %w", label, err)
	}
	return nil
}

// Has says whether text is on screen now: in the accessibility tree, the rendered text or a
// field's value.
func (p *Page) Has(text string) (bool, error) {
	found, err := PresentInAXTree(p.page, text)
	if err != nil {
		return false, err
	}
	if found {
		return true, nil
	}
	return PresentInText(p.page, text)
}

// Wait waits until text is on screen. Timing out, it says what the page shows instead.
func (p *Page) Wait(text string) error {
	deadline := time.Now().Add(waitTimeout)
	for {
		found, err := p.Has(text)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
		if time.Now().After(deadline) {
			shown, _ := p.Text()
			if len(shown) > 400 {
				shown = shown[:400] + "…"
			}
			said := fmt.Sprintf("%q is not on the page after %s; it shows: %s", text,
				waitTimeout, strings.Join(strings.Fields(shown), " "))
			if errors := p.Errors(); len(errors) > 0 {
				said += "\nthe page reported:\n" + strings.Join(errors, "\n")
			}
			return fmt.Errorf("%s", said)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Text is what the page shows, as a person reads it.
func (p *Page) Text() (string, error) {
	res, err := p.page.Eval(`() => document.body ? document.body.innerText : ""`)
	if err != nil {
		return "", err
	}
	return res.Value.Str(), nil
}

// URL is the address the tab is on.
func (p *Page) URL() (string, error) {
	info, err := p.page.Info()
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

// Eval runs a function's source in the page and answers its result.
func (p *Page) Eval(js string) (any, error) {
	res, err := p.page.Eval(js)
	if err != nil {
		return nil, err
	}
	return res.Value.Val(), nil
}

// Resize gives the window another size: a phone's is 390×844.
func (p *Page) Resize(width, height int) error {
	return p.page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:  width,
		Height: height,
	})
}

// Screenshot writes what is on screen as a PNG at path, making its folder.
func (p *Page) Screenshot(path string) error {
	data, err := p.page.Screenshot(false, nil)
	if err != nil {
		return fmt.Errorf("screenshot failed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
