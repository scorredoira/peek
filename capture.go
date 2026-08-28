package main

import (
	"github.com/go-rod/rod"
)

// The page's own account of itself: what it logged, what it threw, and what it asked for and
// did not get. It lives in the PAGE rather than in a CDP listener because peek is a CLI — every
// command is a process that connects, acts and exits, so nothing here outlives a command. A
// listener would only ever hear what happened while that one command was running.
//
// Three things are captured, and the two that were missing are the ones that matter:
//
//   - console.log/warn/error/info — what was already there.
//   - UNCAUGHT exceptions, via onerror and unhandledrejection. A screen that throws while
//     loading paints half of itself and looks like a design problem.
//   - Requests that FAILED, by wrapping fetch and XHR. A 404 on a data call leaves a screen
//     that renders perfectly and is empty, which a screenshot cannot tell from "no rows yet".
//
// Both of those are invisible to a console patch, and both are how a screen that looks right
// turns out to be broken. `peek network` cannot answer it either: it reads
// performance.getEntriesByType("resource"), which lists what was requested and not how it went.
//
// It is idempotent and cheap, so every command that touches a page installs it without asking.
//
// Kept as a BODY because the two doors take different shapes: Eval is handed a function and
// calls it, while a script registered for new documents is evaluated as source — an arrow
// function there is created and thrown away, which installs nothing at all and looks exactly
// like success. Building both forms from one body is what stops them drifting apart.
const captureBody = `
	if (window.__peekCapture) { return }
	window.__peekCapture = true
	window.__peekConsole = window.__peekConsole || []

	const keep = (level, text) => {
		window.__peekConsole.push({ level: level, text: String(text).slice(0, 2000) })
		// Bounded: a page in a render loop must not grow this without limit.
		if (window.__peekConsole.length > 500) { window.__peekConsole.shift() }
	}

	const orig = {}
	;['log', 'warn', 'error', 'info'].forEach(level => {
		orig[level] = console[level]
		console[level] = function (...args) {
			keep(level, args.map(a => {
				if (a instanceof Error) { return a.stack || a.message }
				if (typeof a === 'object') { try { return JSON.stringify(a) } catch (e) { return String(a) } }
				return String(a)
			}).join(' '))
			orig[level].apply(console, args)
		}
	})

	addEventListener('error', e => {
		keep('error', 'uncaught: ' + (e.error && e.error.stack ? e.error.stack : e.message))
	})
	addEventListener('unhandledrejection', e => {
		const r = e.reason
		keep('error', 'unhandled rejection: ' + (r && r.stack ? r.stack : r))
	})

	const failed = (what, url, detail) => keep('error', what + ' ' + url + ' — ' + detail)

	const fetchOrig = window.fetch
	window.fetch = function (...args) {
		const url = (args[0] && args[0].url) || String(args[0])
		return fetchOrig.apply(this, args).then(response => {
			if (!response.ok) { failed('fetch', url, response.status + ' ' + response.statusText) }
			return response
		}).catch(e => {
			failed('fetch', url, e.message)
			throw e
		})
	}

	const openOrig = XMLHttpRequest.prototype.open
	XMLHttpRequest.prototype.open = function (method, url, ...rest) {
		this.addEventListener('load', () => {
			if (this.status >= 400) { failed('xhr', url, this.status + ' ' + this.statusText) }
		})
		this.addEventListener('error', () => failed('xhr', url, 'network error'))
		return openOrig.call(this, method, url, ...rest)
	}
`

// installCapture puts the capture in place for this page, both ways it can be needed.
//
// EvalOnNewDocument is what makes it worth anything: it runs BEFORE the page's own scripts, so
// a throw at load time or a data call that 404s on the first render is caught. It applies to the
// NEXT document, which is why every command that navigates installs before navigating.
//
// The plain Eval covers the page that is already open — `peek console` on a screen loaded ten
// minutes ago still gets whatever happens from now on, rather than nothing.
//
// What it does not cover is the very first page `peek open` loads: Chrome is launched pointing at
// the url, so it is already loading before any session exists to register anything. A `peek
// reload` afterwards gets it. Fixing that means launching blank and navigating, which is a
// change to how Chrome starts for a payoff that a reload already buys.
//
// Failures are deliberately not fatal: capture is an aid, and a command that navigated fine
// should not report failure because instrumenting it did not. The reason still surfaces — it is
// the caller's own action that fails visibly if the page is unusable.
func installCapture(page *rod.Page) {
	_, _ = page.EvalOnNewDocument("(() => {" + captureBody + "})()")
	_, _ = page.Eval("() => {" + captureBody + "}")
}
