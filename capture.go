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
// turns out to be broken. `peek network` reports the status of a request that got a response;
// a throw and a rejected promise reach nothing but this.
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

	// The resource timing buffer holds 250 entries by default and then silently drops
	// every later one — on a screen that keeps fetching, what "peek network" shows is the
	// page loading and never the call that just failed.
	if (performance.setResourceTimingBufferSize) { performance.setResourceTimingBufferSize(1000) }

	const store = {
		read: key => {
			try { return JSON.parse(sessionStorage.getItem(key)) } catch (e) { return null }
		},
		write: (key, value) => {
			try { sessionStorage.setItem(key, JSON.stringify(value)) } catch (e) { }
		},
	}

	// Errors survive the navigation that caused them. The log lives in the page, so a full
	// page load used to wipe it — sign in, land on the next screen, and the exception that
	// broke the sign-in page was gone before anyone could ask. Only errors are carried
	// across: they are what you still want one page later, and writing every log line back
	// on a page in a render loop would cost more than it is worth.
	//
	// They are kept in their own list as well as in the log, so a page throwing in a loop
	// does not re-scan and re-serialize the whole log on every single throw.
	window.__peekErrors = store.read("__peekErrors") || []
	window.__peekConsole = window.__peekErrors.slice()

	const keep = (level, text) => {
		const entry = { level: level, text: String(text).slice(0, 2000) }

		window.__peekConsole.push(entry)
		// Bounded: a page in a render loop must not grow this without limit.
		if (window.__peekConsole.length > 500) { window.__peekConsole.shift() }

		if (level === "error") {
			window.__peekErrors.push(entry)
			if (window.__peekErrors.length > 100) { window.__peekErrors.shift() }
			store.write("__peekErrors", window.__peekErrors)
		}
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

	// capture:true is what makes this hear a <script>, <img> or <link> that failed to load:
	// those fire on the element and never bubble, so a listener without it hears only
	// uncaught exceptions. A missing bundle or a 404'd stylesheet was invisible to both
	// this and "peek network", and the page just rendered unstyled.
	addEventListener('error', e => {
		if (e.target && e.target !== window && e.target.tagName) {
			const src = e.target.src || e.target.href || ""
			keep('error', 'failed to load ' + e.target.tagName.toLowerCase() + ' ' + src)
			return
		}
		if (e.error && e.error.stack) { keep('error', 'uncaught: ' + e.error.stack); return }
		keep('error', 'uncaught: ' + (e.message || "an error with no message"))
	}, true)

	addEventListener('unhandledrejection', e => {
		const r = e.reason
		keep('error', 'unhandled rejection: ' + (r && r.stack ? r.stack : r))
	})

	addEventListener('securitypolicyviolation', e => {
		keep('error', 'CSP blocked ' + (e.blockedURI || "something") +
			' (' + e.violatedDirective + ')')
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

	// A native alert/confirm/prompt BLOCKS the renderer, and a CLI has nobody there to
	// answer it: the page freezes and the next command times out with an error about
	// something else entirely. Answering them here keeps the page moving and, more to the
	// point, RECORDS what was asked — so "did it warn before deleting?" has an answer.
	//
	// The default answer is no. Auto-accepting would press OK on "Delete everything?"
	// while nobody was looking; "peek dialog accept" is how you say otherwise, and it is
	// remembered for the tab.
	window.__peekDialogs = store.read("__peekDialogs") || []

	const askedDialog = (kind, message, answer) => {
		window.__peekDialogs.push({
			kind: kind,
			message: String(message === undefined ? "" : message).slice(0, 500),
			answered: String(answer),
		})
		if (window.__peekDialogs.length > 100) { window.__peekDialogs.shift() }
		store.write("__peekDialogs", window.__peekDialogs)
	}

	const policy = () => store.read("__peekDialogPolicy") || { accept: false, text: "" }

	window.alert = message => { askedDialog("alert", message, "dismissed") }

	window.confirm = message => {
		const answer = policy().accept
		askedDialog("confirm", message, answer ? "accepted" : "dismissed")
		return answer
	}

	window.prompt = (message, fallback) => {
		const p = policy()
		if (!p.accept) {
			askedDialog("prompt", message, "dismissed")
			return null
		}
		const answer = p.text !== "" ? p.text : (fallback === undefined ? "" : fallback)
		askedDialog("prompt", message, answer)
		return answer
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
