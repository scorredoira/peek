package main

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

func cmdConsole(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	result, err := page.Eval(`() => {
		if (!window.__peekConsole) { return [] }
		return window.__peekConsole.map(m => m.level + ': ' + m.text)
	}`)
	if err != nil {
		return fmt.Errorf("failed to read console messages: %w", err)
	}

	arr := result.Value.Val()
	if arr == nil {
		fmt.Println("(no console messages)")
		return nil
	}

	messages, ok := arr.([]interface{})
	if !ok {
		fmt.Println(arr)
		return nil
	}

	// Printing nothing is ambiguous: a clean page and a capture that never installed
	// look identical, and the second one is why you ran this.
	if len(messages) == 0 {
		fmt.Println("(no console messages)")
		return nil
	}

	for _, m := range messages {
		fmt.Println(m)
	}

	return nil
}

// cmdNetwork lists what the page asked for and HOW IT WENT.
//
// The status is the whole point. performance.getEntriesByType("resource") was already
// listing every request, but without responseStatus a 500 and a 200 print identically —
// a screen that renders empty because its data call failed looks exactly like a screen
// with no rows. Chrome exposes the status on the timing entry, so there is nothing to
// intercept and no listener to keep alive between commands.
//
// A status of 0 is not a success: it is a request that never got a response at all —
// blocked by CSP, refused by CORS, a dead host. Those are called out by name rather
// than printed as a number nobody reads as a failure.
func cmdNetwork(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	result, err := page.Eval(`() => {
		const entries = performance.getEntriesByType('resource')
		const total = entries.length
		return {
			total: total,
			entries: entries.slice(-50).map(e => {
				return {
					name: e.name,
					type: e.initiatorType,
					status: e.responseStatus === undefined ? -1 : e.responseStatus,
					duration: Math.round(e.duration),
					size: e.transferSize || 0,
				}
			}),
		}
	}`)
	if err != nil {
		return fmt.Errorf("failed to get network entries: %w", err)
	}

	payload, ok := result.Value.Val().(map[string]interface{})
	if !ok {
		fmt.Println("(no network entries)")
		return nil
	}

	entries, ok := payload["entries"].([]interface{})
	if !ok || len(entries) == 0 {
		fmt.Println("(no network entries)")
		return nil
	}

	total := 0
	if n, ok := payload["total"].(float64); ok {
		total = int(n)
	}
	if total > len(entries) {
		fmt.Printf("(showing the last %d of %d requests)\n", len(entries), total)
	}

	failed := 0

	for _, entry := range entries {
		m, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}

		name := fmt.Sprint(m["name"])
		if len(name) > 80 {
			name = name[:77] + "..."
		}

		status := describeStatus(m["status"])
		if status.bad {
			failed++
		}

		fmt.Printf("%-9s %-6s %-8s %s  %s\n",
			status.text, fmt.Sprintf("%.0fms", asFloat(m["duration"])),
			m["type"], formatBytes(m["size"]), name)
	}

	if failed == 0 {
		fmt.Println("\nall requests succeeded")
		return nil
	}

	fmt.Printf("\n%d of these requests FAILED — a screen can render perfectly on failed data\n",
		failed)
	return nil
}

// networkStatus is one request's outcome, ready to print and already judged.
type networkStatus struct {
	text string
	bad  bool
}

func describeStatus(v interface{}) networkStatus {
	code := int(asFloat(v))

	switch {
	case code < 0:
		return networkStatus{text: "?", bad: false}
	case code == 0:
		return networkStatus{text: "BLOCKED", bad: true}
	case code >= 400:
		return networkStatus{text: fmt.Sprintf("%d !", code), bad: true}
	default:
		return networkStatus{text: fmt.Sprint(code), bad: false}
	}
}

func asFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

func formatBytes(v interface{}) string {
	var n float64
	switch val := v.(type) {
	case float64:
		n = val
	case int:
		n = float64(val)
	default:
		return "?"
	}

	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1fMB", n/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", n/1024)
	default:
		return fmt.Sprintf("%.0fB", n)
	}
}

// cmdDialog sets how the page's alert/confirm/prompt are answered, and shows what it asked.
//
// The old version registered a CDP event handler in a goroutine, printed success and let the
// process exit — the handler never fired, so the flow it was meant to verify was never
// exercised, and the page then sat frozen on a native confirm() while the next command timed
// out with an unrelated error. The answer lives in the page instead (see capture.go), so it
// survives the command that set it, which is the only way a CLI can do this at all.
func cmdDialog(ctx *cmdContext, args []string) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return showDialogs(page)
	}

	action := strings.ToLower(args[0])

	switch action {
	case "accept":
		text := ""
		if len(args) > 1 {
			text = strings.Join(args[1:], " ")
		}
		if err := setDialogPolicy(page, true, text); err != nil {
			return err
		}
		fmt.Println("dialogs will be accepted from now on, in this tab")

	case "dismiss":
		if err := setDialogPolicy(page, false, ""); err != nil {
			return err
		}
		fmt.Println("dialogs will be dismissed from now on, in this tab")

	default:
		return fmt.Errorf("unknown dialog action: %s (use accept, dismiss, or nothing to list)",
			action)
	}

	return nil
}

func setDialogPolicy(page *rod.Page, accept bool, text string) error {
	js := fmt.Sprintf(`() => {
		sessionStorage.setItem("__peekDialogPolicy", JSON.stringify({ accept: %t, text: %q }))
	}`, accept, text)

	if _, err := page.Eval(js); err != nil {
		return fmt.Errorf("cannot set the dialog answer: %w", err)
	}

	return nil
}

// showDialogs prints what the page actually asked. Setting an answer proves nothing; this is
// how you check the confirm before a delete was really there.
func showDialogs(page *rod.Page) error {
	res, err := page.Eval(`() => {
		if (!window.__peekDialogs) { return [] }
		return window.__peekDialogs.map(d => d.kind + ': ' + d.message + ' → ' + d.answered)
	}`)
	if err != nil {
		return fmt.Errorf("cannot read the dialogs: %w", err)
	}

	asked, ok := res.Value.Val().([]interface{})
	if !ok || len(asked) == 0 {
		fmt.Println("(the page has asked nothing)")
		return nil
	}

	for _, d := range asked {
		fmt.Println(d)
	}

	return nil
}
