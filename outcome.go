package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// What an action did, as the page shows it. "clicked Save" says the press went out, not what
// came back: the record saved, a field the site refused, the screen it went to. Each of those
// used to cost a snapshot after every step, and a form the site turned down read as a success
// until the step after it failed for another reason.
//
// The page is read before the action and again once it is quiet — no request of its own on the
// way, no document loading — and what changed is reported:
//
//   - what it announced: a toast, an alert, a status line (the live regions capture.go hears,
//     so one that came and went before the look still counts)
//   - the fields it marked invalid, with their message
//   - the address it went to
//   - the alert, confirm or prompt it asked, and how it was answered
//   - a tab it opened
//
// Nothing changed prints nothing: the action is reported as before.

// outcomeQuiet is how long a page must stay quiet after the action to have answered it, and
// outcomeWait how long it is given at most: a slow save still reports, a page that never stops
// fetching (a poll) costs that and no more.
const (
	outcomeQuiet = 200 * time.Millisecond
	outcomeWait  = 5 * time.Second
)

// shownState is what the page shows at one look.
type shownState struct {
	Doc       string          `json:"doc"`
	URL       string          `json:"url"`
	Ready     string          `json:"ready"`
	Inflight  int             `json:"inflight"`
	Said      []string        `json:"said"`
	SaidCount int             `json:"saidCount"`
	Dialogs   []dialogRecord  `json:"dialogs"`
	Invalid   []invalidRecord `json:"invalid"`
}

type dialogRecord struct {
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Answered string `json:"answered"`
}

type invalidRecord struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// stateCode reads the page. A field is invalid when the site says so (aria-invalid, Bootstrap's
// is-invalid) or the browser's own validation failed after the person tried to submit
// (:user-invalid); its message is the one the site linked to it, or the one beside it, or the
// browser's.
const stateCode = `() => {
	const shows = el => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden"
	const clean = (t, n) => String(t || "").replace(/\s+/g, " ").trim().slice(0, n)
	const byIds = ids => String(ids || "").split(/\s+/).map(id => id && document.getElementById(id))
		.filter(el => el && shows(el)).map(el => clean(el.innerText, 200)).filter(t => t)
	const label = el => clean(el.getAttribute("aria-label"), 100) ||
		byIds(el.getAttribute("aria-labelledby")).join(" ") ||
		clean(Array.from(el.labels || []).map(l => l.innerText).join(" "), 100) ||
		clean(el.closest("label") && el.closest("label").innerText, 100) ||
		clean(el.getAttribute("name") || el.getAttribute("placeholder") || el.id, 100)
	const message = el => {
		const linked = byIds(el.getAttribute("aria-errormessage")).concat(byIds(el.getAttribute("aria-describedby")))
		if (linked.length > 0) { return clean(linked.join(" "), 200) }
		const near = el.parentElement && el.parentElement.querySelector('.invalid-feedback, [role="alert"], [class*="error" i]')
		if (near && near !== el && !near.contains(el) && shows(near)) { return clean(near.innerText, 200) }
		return clean(el.validationMessage, 200)
	}
	let marks = '[aria-invalid="true"], .is-invalid'
	if (CSS.supports("selector(:user-invalid)")) { marks += ", :user-invalid" }
	const invalid = []
	for (const el of document.querySelectorAll(marks)) {
		if (!shows(el) && !Array.from(el.labels || []).some(shows)) { continue }
		invalid.push({ field: label(el), message: message(el) })
		if (invalid.length === 20) { break }
	}
	return JSON.stringify({
		doc: window.__peekDoc || "",
		url: location.href,
		ready: document.readyState,
		inflight: window.__peekInflight || 0,
		said: window.__peekSaid || [],
		saidCount: window.__peekSaidCount || 0,
		dialogs: window.__peekDialogs || [],
		invalid: invalid,
	})
}`

func readState(page *rod.Page) (shownState, error) {
	var st shownState
	res, err := page.Eval(stateCode)
	if err != nil {
		return st, err
	}
	err = json.Unmarshal([]byte(res.Value.Str()), &st)
	return st, err
}

// outcomeWatch is the page as it was before an action, and whether its main frame is loading a
// document since: the old one keeps answering until the new one replaces it, so only Chrome
// knows a navigation is on its way.
type outcomeWatch struct {
	ctx     *cmdContext
	page    *rod.Page
	before  shownState
	known   bool
	tabs    map[string]bool
	loading atomic.Bool
	stop    context.CancelFunc
}

// watchOutcome reads the page before an action. A page it cannot read is reported as before:
// the outcome is an aid, and the action is what the command was asked for.
func watchOutcome(ctx *cmdContext, page *rod.Page) *outcomeWatch {
	w := &outcomeWatch{ctx: ctx, page: page, tabs: map[string]bool{}}
	if st, err := readState(page); err == nil {
		w.before, w.known = st, true
	}
	if targets, err := listPageTargets(ctx.port); err == nil {
		for _, t := range targets {
			w.tabs[t.ID] = true
		}
	}

	c, cancel := context.WithCancel(context.Background())
	w.stop = cancel
	frame := page.FrameID
	wait := page.Context(c).EachEvent(func(e *proto.PageFrameStartedLoading) {
		if e.FrameID == frame {
			w.loading.Store(true)
		}
	}, func(e *proto.PageFrameStoppedLoading) {
		if e.FrameID == frame {
			w.loading.Store(false)
		}
	})
	go wait()
	return w
}

// report waits for the page to answer the action and prints what changed, each on its own
// line under the action's.
func (w *outcomeWatch) report() {
	defer w.stop()
	if !w.known {
		return
	}

	start := time.Now()
	var st shownState
	var quietSince time.Time
	read := false
	for time.Since(start) < outcomeWait {
		time.Sleep(50 * time.Millisecond)
		now, err := readState(w.page)
		if err != nil {
			// the page is between two documents
			quietSince = time.Time{}
			continue
		}
		st, read = now, true
		if w.loading.Load() || st.Inflight > 0 || st.Ready == "loading" {
			quietSince = time.Time{}
			continue
		}
		if quietSince.IsZero() {
			quietSince = time.Now()
		}
		if time.Since(quietSince) >= outcomeQuiet {
			break
		}
	}
	if !read {
		return
	}

	for _, line := range outcomeLines(w.before, st) {
		fmt.Println("  " + line)
	}

	if targets, err := listPageTargets(w.ctx.port); err == nil {
		for _, t := range targets {
			if !w.tabs[t.ID] {
				fmt.Printf("  opened a tab: %s\n", t.URL)
			}
		}
	}
}

// outcomeLines is what changed from before to after. A new document is all new: what it says
// and marks is the answer, whatever the one before said.
func outcomeLines(before, after shownState) []string {
	var lines []string
	sameDoc := after.Doc != "" && after.Doc == before.Doc

	said := after.Said
	if sameDoc {
		n := after.SaidCount - before.SaidCount
		if n <= 0 {
			said = nil
		} else if n < len(said) {
			said = said[len(said)-n:]
		}
	}
	for _, s := range said {
		lines = append(lines, fmt.Sprintf("the page said %q", s))
	}

	was := map[invalidRecord]bool{}
	if sameDoc {
		for _, f := range before.Invalid {
			was[f] = true
		}
	}
	for _, f := range after.Invalid {
		if was[f] {
			continue
		}
		if f.Message != "" {
			lines = append(lines, fmt.Sprintf("%q is marked invalid: %q", f.Field, f.Message))
		} else {
			lines = append(lines, fmt.Sprintf("%q is marked invalid", f.Field))
		}
	}

	// the dialogs are kept for the tab (sessionStorage), across its documents
	if len(after.Dialogs) > len(before.Dialogs) {
		for _, d := range after.Dialogs[len(before.Dialogs):] {
			lines = append(lines, fmt.Sprintf("%s %q → %s", d.Kind, d.Message, d.Answered))
		}
	}

	if after.URL != before.URL {
		lines = append(lines, "went to "+after.URL)
	}
	return lines
}
