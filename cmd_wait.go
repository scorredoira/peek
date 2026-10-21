package main

import (
	"github.com/scorredoira/peek/browse"
	"regexp"

	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

const defaultTimeout = 10 * time.Second
const pollInterval = 300 * time.Millisecond

func cmdWait(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek wait <text> | --css <sel> | --gone <text> | --url <part> | --url-re <regexp>")
	}

	timeout := defaultTimeout
	gone := false
	css := ""
	var rest []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--timeout":
			if i+1 >= len(args) {
				return fmt.Errorf("--timeout needs a duration")
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return fmt.Errorf("invalid timeout: %s", args[i+1])
			}
			timeout = d
			i++
		case "--gone":
			gone = true
		case "--css":
			if i+1 >= len(args) {
				return fmt.Errorf("--css needs a selector")
			}
			css = args[i+1]
			i++
		case "--url":
			if i+1 >= len(args) {
				return fmt.Errorf("--url needs a pattern")
			}
			return waitURL(ctx, args[i+1], nil, timeout)
		case "--url-re":
			if i+1 >= len(args) {
				return fmt.Errorf("--url-re needs a regular expression")
			}
			re, err := regexp.Compile(args[i+1])
			if err != nil {
				return fmt.Errorf("--url-re: %w", err)
			}
			return waitURL(ctx, args[i+1], re, timeout)
		default:
			rest = append(rest, args[i])
		}
	}

	text := strings.Join(rest, " ")

	if css == "" && text == "" {
		return fmt.Errorf("usage: peek wait <text> | --css <sel> | --gone <text>")
	}

	target := waitTarget{css: css, text: text}

	if gone {
		return waitGone(ctx, target, timeout)
	}

	return waitPresent(ctx, target, timeout)
}

// waitTarget is what a wait is watching: a selector, a piece of text, or a selector
// that must also contain the text.
type waitTarget struct {
	css  string
	text string
}

func (t waitTarget) describe() string {
	switch {
	case t.css != "" && t.text != "":
		return fmt.Sprintf("%q inside %s", t.text, t.css)
	case t.css != "":
		return t.css
	default:
		return fmt.Sprintf("%q", t.text)
	}
}

// isPresent asks the page whether the target is on screen right now.
//
// Text is looked for in the accessibility tree AND in the rendered text of the document.
// The tree alone was the old behaviour and it misses whole categories of what a person
// plainly sees — a table cell, a badge, a span with no role — so waiting for an amount to
// appear timed out while the amount was right there. It also reads an input's VALUE, which
// lives outside the node's name: after filling a field, the text you typed is on screen.
func isPresent(page *rod.Page, t waitTarget) (bool, error) {
	if t.css != "" {
		return presentByCSS(page, t)
	}

	found, err := browse.PresentInAXTree(page, t.text)
	if err != nil {
		return false, err
	}
	if found {
		return true, nil
	}

	return browse.PresentInText(page, t.text)
}

func presentByCSS(page *rod.Page, t waitTarget) (bool, error) {
	js := fmt.Sprintf(`() => {
		const els = document.querySelectorAll(%q)
		const wanted = %q.toLowerCase()
		for (const el of els) {
			const style = getComputedStyle(el)
			if (style.display === "none" || style.visibility === "hidden") { continue }
			if (wanted === "") { return true }
			const text = (el.innerText || el.textContent || "").toLowerCase()
			if (text.indexOf(wanted) >= 0) { return true }
		}
		return false
	}`, t.css, t.text)

	res, err := page.Eval(js)
	if err != nil {
		return false, err
	}

	return res.Value.Bool(), nil
}

func waitPresent(ctx *cmdContext, t waitTarget, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		_, page, err := connect(ctx)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		found, err := isPresent(page, t)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		if found {
			fmt.Printf("found %s\n", t.describe())
			return nil
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for %s", t.describe())
}

// waitGone waits for something to DISAPPEAR, and refuses to call it a disappearance when
// it was never there.
//
// The old version returned success on the first poll if the text was absent, which is the
// idiom every caller reaches for — click Save, wait for "Saving…" to go — passing instantly
// because the spinner had not rendered yet. Waiting for nothing then reads as proof the
// operation finished. So the absence has to be a TRANSITION this command actually watched:
// it must see the thing first, and it says so when it never did.
func waitGone(ctx *cmdContext, t waitTarget, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	seen := false

	for time.Now().Before(deadline) {
		_, page, err := connect(ctx)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		found, err := isPresent(page, t)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		if found {
			seen = true
		} else if seen {
			fmt.Printf("gone %s\n", t.describe())
			return nil
		}

		time.Sleep(pollInterval)
	}

	if !seen {
		return fmt.Errorf("%s was never there — nothing disappeared, and this waited for nothing",
			t.describe())
	}

	return fmt.Errorf("timeout waiting for %s to disappear", t.describe())
}

// waitURL waits for the URL to contain a pattern.
// waitURL waits for the address to hold pattern, or to match re when one is given: an address
// with an id in it (/apps/6824176623/…) is only said as a regular expression.
func waitURL(ctx *cmdContext, pattern string, re *regexp.Regexp, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		_, page, err := connect(ctx)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		info, err := page.Info()
		if err == nil && (re == nil && strings.Contains(info.URL, pattern) || re != nil && re.MatchString(info.URL)) {
			fmt.Println(info.URL)
			return nil
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for URL matching %q", pattern)
}
