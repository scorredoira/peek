package main

import (
	"fmt"
	"os"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// dialogPolicy is the backstop answer for the dialogs the page's own overrides never see, and
// which pages already have somebody listening.
//
// capture.go replaces window.alert/confirm/prompt, so those normally never become native
// dialogs at all: they are answered and recorded inside the page, which is where `peek dialog`
// reads them from. Two things escape that, and both used to freeze the browser:
//
//   - beforeunload — "Leave site? Changes you made may not be saved". It is not a function the
//     page calls, so there is nothing to replace: only Chrome can answer it.
//   - a document the overrides never reached — an iframe, or a script that asked before the
//     capture was installed.
//
// The lock is not decoration: the listener is a goroutine that outlives the command that
// started it, so it reads this while a later command is writing it.
type dialogPolicy struct {
	mu       sync.Mutex
	explicit bool
	accept   bool
	text     string
	armed    map[proto.TargetTargetID]bool
}

func (p *dialogPolicy) set(accept bool, text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.explicit = true
	p.accept = accept
	p.text = text
}

// answer says how a dialog of this kind gets closed.
//
// A beforeunload is not a question about what the app should do: it is the page asking whether
// to let go of edits this run itself typed, and the command it interrupted is the one that
// asked to leave. Answering anything but yes cancels a navigation that was ordered.
//
// An alert has one button. Accept and dismiss both close it.
//
// A confirm or a prompt is a real question, and the default answer is no — the same answer the
// page's own override gives, so which of the two entry points it came through does not change what the
// app was told.
func (p *dialogPolicy) answer(kind proto.PageDialogType) (bool, string) {
	if kind == proto.PageDialogTypeBeforeunload || kind == proto.PageDialogTypeAlert {
		return true, ""
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.explicit {
		return false, ""
	}
	return p.accept, p.text
}

// arm claims a page for the listener, and says whether it was already claimed.
func (p *dialogPolicy) arm(target proto.TargetTargetID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.armed[target] {
		return false
	}
	if p.armed == nil {
		p.armed = map[proto.TargetTargetID]bool{}
	}
	p.armed[target] = true
	return true
}

// armDialogs answers the native dialogs, because there is nobody at the keyboard to do it.
//
// A native dialog blocks the page until somebody presses a button, and peek is a CLI: every
// command is a process that connects, acts and exits, so a dialog raised by a click is still
// standing when that process is gone. The command that raised it hangs, every command after it
// times out against a page that cannot answer, and the only way out is a human walking to the
// machine and clicking. Filling a form and then navigating away was enough to do it.
//
// Armed in connect(), so it covers the command that opens the dialog rather than a later one:
// by the time a second command runs, the first has already hung.
//
// Once per page, not once per command: every command connects again over the same tab, and a
// listener per connection means every listener but the first finds the dialog already answered
// and reports a failure that is only its own duplication.
//
// It cannot record into __peekDialogs the way the page's own overrides do: while a native
// dialog is up the renderer is blocked, so there is no JavaScript to run, and a beforeunload is
// answered on the way out of the document that would have held it. It says so on stderr
// instead — an answer nobody typed is never silent.
func armDialogs(page *rod.Page, policy *dialogPolicy) {
	if !policy.arm(page.TargetID) {
		return
	}

	wait := page.EachEvent(func(e *proto.PageJavascriptDialogOpening) bool {
		accept, text := policy.answer(e.Type)
		err := proto.PageHandleJavaScriptDialog{
			Accept:     accept,
			PromptText: text,
		}.Call(page)

		answer := "accepted"
		if !accept {
			answer = "dismissed"
		}
		if err != nil {
			answer = fmt.Sprintf("could not be answered: %v", err)
		}
		fmt.Fprintf(os.Stderr, "dialog: %s %q → %s\n", e.Type, e.Message, answer)

		// Keep listening: a page that asks once usually asks again.
		return false
	})

	go wait()
}

// dialogPolicy returns this run's policy, creating it if no command has connected yet — a
// `dialog` line can be the first line of a test file.
func (ctx *cmdContext) dialogPolicy() *dialogPolicy {
	if ctx.dialogs == nil {
		ctx.dialogs = &dialogPolicy{}
	}
	return ctx.dialogs
}
