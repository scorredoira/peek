package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

func cmdClick(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek click [--css <sel>] [--id <id>] [text]")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	q := parseElementArgs(args)
	el, err := findElement(page, q)
	if err != nil {
		return err
	}

	if covered, err := coveredBy(el); err != nil {
		return err
	} else if covered != "" {
		return fmt.Errorf("%s is behind %s — a user clicking there would hit that instead",
			describeQuery(q), covered)
	}

	// Always click via JS, not Rod's real-mouse click: the latter does a
	// scroll-into-view that hangs on complex layouts (and intermittently in
	// long sessions). DOM-level click is enough — controls respond to the
	// native "click" event. (text queries used to take the mouse path and were
	// the ones that hung; css/id already used JS.)
	if _, err = el.Eval(`() => this.click()`); err != nil {
		return fmt.Errorf("click failed: %w", err)
	}

	fmt.Printf("clicked %s\n", describeQuery(q))
	return nil
}

// coveredBy reports what is painted on top of an element, or "" when nothing is.
//
// A JS click reaches the element directly: through an open dialog's backdrop, through
// pointer-events:none, through anything. That is what makes it immune to the scroll hang, and
// it is also how this tool can report "clicked" for an interaction no user could perform —
// the handler runs, the check passes, and the thing being verified was never reachable.
// Refusing is the whole point of a verification tool.
//
// It asks the browser rather than reasoning about z-index: elementFromPoint is the authority
// on what a click at that point would hit. An element containing the hit (its own label) or
// contained by it counts as itself.
//
// Off screen it declines to judge — elementFromPoint answers null outside the viewport, and
// that is not "covered". Scrolling first is exactly what this command avoids.
func coveredBy(el *rod.Element) (string, error) {
	js := `() => {
		const r = this.getBoundingClientRect()
		if (r.width <= 0 || r.height <= 0) { return "" }

		const x = r.left + r.width / 2
		const y = r.top + r.height / 2
		if (x < 0 || y < 0 || x > innerWidth || y > innerHeight) { return "" }

		const at = document.elementFromPoint(x, y)
		if (!at || at === this || this.contains(at) || at.contains(this)) { return "" }

		const text = (at.textContent || "").replace(/\s+/g, " ").trim().slice(0, 40)
		return text ? at.tagName.toLowerCase() + ' "' + text + '"' : at.tagName.toLowerCase()
	}`

	result, err := el.Eval(js)
	if err != nil {
		return "", fmt.Errorf("check what is on top: %w", err)
	}
	return result.Value.Str(), nil
}

func cmdRightClick(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek rightclick [--css <sel>] [--id <id>] [text]")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	q := parseElementArgs(args)
	el, err := findElement(page, q)
	if err != nil {
		return err
	}

	if covered, err := coveredBy(el); err != nil {
		return err
	} else if covered != "" {
		return fmt.Errorf("%s is behind %s — a user right-clicking there would hit that instead",
			describeQuery(q), covered)
	}

	// Dispatch a real "contextmenu" event via JS rather than Rod's real-mouse
	// right-click: the latter does a scroll-into-view that hangs on complex
	// layouts (same reason cmdClick uses JS). clientX/clientY are set to the
	// element's center so menus that position at the pointer land on it.
	js := `() => {
		const r = this.getBoundingClientRect();
		const ev = new MouseEvent("contextmenu", {
			bubbles: true,
			cancelable: true,
			button: 2,
			clientX: r.left + r.width / 2,
			clientY: r.top + r.height / 2,
		});
		this.dispatchEvent(ev);
	}`
	if _, err = el.Eval(js); err != nil {
		return fmt.Errorf("right-click failed: %w", err)
	}

	fmt.Printf("right-clicked %s\n", describeQuery(q))
	return nil
}

func cmdDblClick(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek dblclick [--css <sel>] [--id <id>] [text]")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	q := parseElementArgs(args)
	el, err := findElement(page, q)
	if err != nil {
		return err
	}

	if err := el.Click(proto.InputMouseButtonLeft, 2); err != nil {
		return fmt.Errorf("double-click failed: %w", err)
	}

	fmt.Printf("double-clicked %s\n", describeQuery(q))
	return nil
}

func cmdFill(ctx *cmdContext, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: peek fill [--name <name>] [--css <sel>] <label> <value>")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	// Parse --name and --css flags.
	var nameAttr, cssAttr string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				nameAttr = args[i+1]
				i++
			}
		case "--css":
			if i+1 < len(args) {
				cssAttr = args[i+1]
				i++
			}
		default:
			rest = append(rest, args[i])
		}
	}

	var el *rod.Element
	var desc string

	if nameAttr != "" {
		if len(rest) == 0 {
			return fmt.Errorf("usage: peek fill --name <name> <value>")
		}
		el, err = findByCSS(page, fmt.Sprintf(`input[name=%q], textarea[name=%q]`, nameAttr, nameAttr), "")
		desc = fmt.Sprintf("name=%q", nameAttr)
		rest = rest // value is all of rest
	} else if cssAttr != "" {
		if len(rest) == 0 {
			return fmt.Errorf("usage: peek fill --css <selector> <value>")
		}
		el, err = findByCSS(page, cssAttr, "")
		desc = fmt.Sprintf("css=%q", cssAttr)
	} else {
		if len(rest) < 2 {
			return fmt.Errorf("usage: peek fill <label> <value>")
		}
		label := rest[0]
		rest = rest[1:]
		el, err = findInput(page, label)
		desc = fmt.Sprintf("%q", label)
	}

	if err != nil {
		return err
	}

	value := strings.Join(rest, " ")

	// Clear existing value.
	_ = el.SelectAllText()
	page.Keyboard.Press(input.Backspace)

	if err := el.Input(value); err != nil {
		return fmt.Errorf("fill failed: %w", err)
	}

	fmt.Printf("filled %s = %q\n", desc, value)
	return nil
}

func cmdSelect(ctx *cmdContext, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: peek select <label> <value>")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	label := args[0]
	value := strings.Join(args[1:], " ")

	// Try fast CSS lookup first (custom widgets like .Select, .Input),
	// then fall back to accessibility tree (native selects/inputs).
	el, err := findByCSS(page, ".Select", label)
	if err != nil {
		el, err = findSelectByLabel(page, label)
		if err != nil {
			el, err = findInput(page, label)
			if err != nil {
				return fmt.Errorf("input with label %q not found", label)
			}
		}
	}

	tag, _ := el.Eval(`() => this.tagName`)
	if tag != nil && strings.EqualFold(tag.Value.Str(), "select") {
		err = el.Select([]string{value}, true, rod.SelectorTypeText)
		if err != nil {
			return fmt.Errorf("select failed: %w", err)
		}
	} else {
		// For custom dropdowns: click to open, then click the option.
		// Use JS click to avoid Rod's ScrollIntoView which triggers
		// scroll events that close framework popups.
		if _, err := el.Eval(`() => this.click()`); err != nil {
			return fmt.Errorf("click to open failed: %w", err)
		}
		time.Sleep(300 * time.Millisecond)

		// Look for the option inside the visible popup using a single
		// JS call that finds and clicks in one shot. This avoids the
		// 3s retry loop of findByCSS which outlasts the popup lifetime.
		jsCode := fmt.Sprintf(`(function(){
			var sels = [".Popup .row", ".Popup .option", ".Popup li"];
			var lower = %q.toLowerCase();
			var all = [];
			for (var s = 0; s < sels.length; s++) {
				var els = document.querySelectorAll(sels[s]);
				for (var i = 0; i < els.length; i++) {
					var t = els[i].textContent.trim();
					if (t) all.push(t);
					if (t.toLowerCase() === lower || t.toLowerCase().indexOf(lower) !== -1) {
						els[i].click();
						return JSON.stringify({found: true});
					}
				}
			}
			return JSON.stringify({found: false, options: all.slice(0, 10)});
		})()`, value)

		// Retry a few times since the popup may need a moment to render.
		var lastResult string
		for attempt := 0; attempt < 6; attempt++ {
			if attempt > 0 {
				time.Sleep(300 * time.Millisecond)
			}
			res, jsErr := proto.RuntimeEvaluate{Expression: jsCode}.Call(page)
			if jsErr != nil {
				continue
			}
			lastResult = res.Result.Value.Str()
			if strings.Contains(lastResult, `"found":true`) {
				goto selected
			}
		}

		// Option not found. Close the popup and report available options.
		page.KeyActions().Press(input.Escape).Do()
		time.Sleep(100 * time.Millisecond)
		return fmt.Errorf("option %q not found\n  available: %s", value, lastResult)

	selected:
	}

	fmt.Printf("selected %q = %q\n", label, value)
	return nil
}

func cmdType(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek type <text>")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	text := strings.Join(args, " ")
	if err := page.InsertText(text); err != nil {
		return fmt.Errorf("type failed: %w", err)
	}

	fmt.Printf("typed %q\n", text)
	return nil
}

func cmdPress(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek press <key>")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	key := resolveKey(args[0])
	if err := page.KeyActions().Press(key).Do(); err != nil {
		return fmt.Errorf("press failed: %w", err)
	}

	fmt.Printf("pressed %s\n", args[0])
	return nil
}

func cmdHover(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek hover [--css <sel>] [--id <id>] [text]")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	q := parseElementArgs(args)
	el, err := findElement(page, q)
	if err != nil {
		return err
	}

	if err := el.Hover(); err != nil {
		return fmt.Errorf("hover failed: %w", err)
	}

	fmt.Printf("hovering %s\n", describeQuery(q))
	return nil
}

func cmdDrag(ctx *cmdContext, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: peek drag <from-text> <to-text>")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	from, err := findByText(page, args[0])
	if err != nil {
		return fmt.Errorf("from element: %w", err)
	}

	to, err := findByText(page, args[1])
	if err != nil {
		return fmt.Errorf("to element: %w", err)
	}

	fromBox, err := from.Shape()
	if err != nil {
		return fmt.Errorf("from shape: %w", err)
	}
	toBox, err := to.Shape()
	if err != nil {
		return fmt.Errorf("to shape: %w", err)
	}

	fromPt := fromBox.OnePointInside()
	toPt := toBox.OnePointInside()

	mouse := page.Mouse
	if err := mouse.MoveTo(proto.NewPoint(fromPt.X, fromPt.Y)); err != nil {
		return err
	}
	if err := mouse.Down(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	if err := mouse.MoveTo(proto.NewPoint(toPt.X, toPt.Y)); err != nil {
		return err
	}
	if err := mouse.Up(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}

	fmt.Printf("dragged %q -> %q\n", args[0], args[1])
	return nil
}

func cmdUpload(ctx *cmdContext, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: peek upload <selector> <filepath>")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	el, err := page.Element(args[0])
	if err != nil {
		return fmt.Errorf("element not found: %w", err)
	}

	if err := el.SetFiles([]string{args[1]}); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	fmt.Printf("uploaded %s\n", args[1])
	return nil
}

func resolveKey(name string) input.Key {
	switch strings.ToLower(name) {
	case "enter":
		return input.Enter
	case "tab":
		return input.Tab
	case "escape", "esc":
		return input.Escape
	case "backspace":
		return input.Backspace
	case "delete":
		return input.Delete
	case "arrowup", "up":
		return input.ArrowUp
	case "arrowdown", "down":
		return input.ArrowDown
	case "arrowleft", "left":
		return input.ArrowLeft
	case "arrowright", "right":
		return input.ArrowRight
	case "space":
		return input.Space
	case "home":
		return input.Home
	case "end":
		return input.End
	case "pageup":
		return input.PageUp
	case "pagedown":
		return input.PageDown
	default:
		return input.Key(rune(name[0]))
	}
}
