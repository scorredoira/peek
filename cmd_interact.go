package main

import (
	"github.com/scorredoira/peek/browse"

	"fmt"
	"io"
	"os"
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

	q, err := browse.ParseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := browse.FindElement(page, q)
	if err != nil {
		return err
	}

	if err := reveal(ctx, page, el); err != nil {
		return err
	}

	if err := browse.RefuseUnclickable(el, browse.DescribeQuery(q)); err != nil {
		return err
	}

	if err := press(ctx, page, el, "click"); err != nil {
		return err
	}

	fmt.Printf("clicked %s\n", browse.DescribeQuery(q))
	return nil
}

func cmdRightClick(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek rightclick [--css <sel>] [--id <id>] [text]")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	q, err := browse.ParseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := browse.FindElement(page, q)
	if err != nil {
		return err
	}

	if err := reveal(ctx, page, el); err != nil {
		return err
	}

	if err := browse.RefuseUnclickable(el, browse.DescribeQuery(q)); err != nil {
		return err
	}

	if err := press(ctx, page, el, "rightclick"); err != nil {
		return err
	}

	fmt.Printf("right-clicked %s\n", browse.DescribeQuery(q))
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

	q, err := browse.ParseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := browse.FindElement(page, q)
	if err != nil {
		return err
	}

	if err := reveal(ctx, page, el); err != nil {
		return err
	}

	if err := browse.RefuseUnclickable(el, browse.DescribeQuery(q)); err != nil {
		return err
	}

	if err := press(ctx, page, el, "dblclick"); err != nil {
		return err
	}

	fmt.Printf("double-clicked %s\n", browse.DescribeQuery(q))
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

	var nameAttr, cssAttr string
	var rest []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 >= len(args) {
				return fmt.Errorf("--name needs a field name")
			}
			nameAttr = args[i+1]
			i++
		case "--css":
			if i+1 >= len(args) {
				return fmt.Errorf("--css needs a selector")
			}
			cssAttr = args[i+1]
			i++
		default:
			rest = append(rest, args[i])
		}
	}

	if nameAttr != "" && cssAttr != "" {
		return fmt.Errorf("--name and --css are exclusive")
	}

	var el *rod.Element
	var desc string

	switch {
	case nameAttr != "":
		if len(rest) == 0 {
			return fmt.Errorf("usage: peek fill --name <name> <value>")
		}
		selector := fmt.Sprintf(`input[name=%q], textarea[name=%q]`, nameAttr, nameAttr)
		el, err = browse.FindByCSS(page, selector, "")
		desc = fmt.Sprintf("name=%q", nameAttr)

	case cssAttr != "":
		if len(rest) == 0 {
			return fmt.Errorf("usage: peek fill --css <selector> <value>")
		}
		el, err = browse.FindByCSS(page, cssAttr, "")
		desc = fmt.Sprintf("css=%q", cssAttr)

	default:
		if len(rest) < 2 {
			return fmt.Errorf("usage: peek fill <label> <value>")
		}
		label := rest[0]
		rest = rest[1:]
		el, err = browse.FindInput(page, label)
		desc = fmt.Sprintf("%q", label)
	}

	if err != nil {
		return err
	}

	value := strings.Join(rest, " ")

	var landed string
	if pacedFor(ctx) {
		landed, err = humanFill(ctx, page, el, value)
	} else {
		landed, err = browse.FillElement(page, el, value)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", desc, err)
	}

	if landed != value {
		fmt.Printf("filled %s = %q — the field shows %q\n", desc, value, landed)
		return nil
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
	el, err := browse.FindByCSS(page, ".Select", label)
	if err != nil {
		el, err = browse.FindSelectByLabel(page, label)
		if err != nil {
			el, err = browse.FindInput(page, label)
			if err != nil {
				el, err = browse.FindDropdownTrigger(page, label)
				if err != nil {
					return fmt.Errorf("input with label %q not found", label)
				}
			}
		}
	}

	desc := fmt.Sprintf("%q", label)
	if err := browse.RefuseUnclickable(el, desc); err != nil {
		return err
	}

	tag, err := el.Eval(`() => this.tagName`)
	if err != nil {
		return fmt.Errorf("cannot read the field: %w", err)
	}

	if strings.EqualFold(tag.Value.Str(), "select") {
		if err := selectNativeOption(el, value); err != nil {
			return err
		}
	} else {
		if err := selectFromPopup(page, el, value); err != nil {
			return err
		}
	}

	fmt.Printf("selected %q = %q\n", label, value)
	return nil
}

// selectNativeOption picks an option of a real <select> by its visible text.
//
// rod's own Select goes through Focus, which scrolls the element into view — the path
// that hangs on complex layouts, the same one fill had to stop taking. Matching and
// setting in JS costs nothing and cannot hang, and it can say which options there ARE
// when none matches, instead of a bare "not found".
func selectNativeOption(el *rod.Element, value string) error {
	js := fmt.Sprintf(`() => {
		// a page's no-break spaces ("EE.\u00a0UU.") and its accents' form read as typed
		const plain = t => t.normalize("NFC").replace(/\s+/g, " ").trim().toLowerCase()
		const wanted = plain(%q)
		const options = Array.from(this.options)
		const label = o => (o.label || o.textContent || "").trim()

		let hit = options.find(o => plain(label(o)) === wanted)
		// the option's value (en-US) names it too
		if (!hit) { hit = options.find(o => plain(o.value) === wanted) }
		if (!hit) { hit = options.find(o => plain(label(o)).indexOf(wanted) >= 0) }
		if (!hit) { return { found: false, options: options.map(label).slice(0, 10) } }

		this.value = hit.value
		this.dispatchEvent(new Event("input", { bubbles: true }))
		this.dispatchEvent(new Event("change", { bubbles: true }))
		return { found: true, chosen: label(hit) }
	}`, value)

	res, err := el.Eval(js)
	if err != nil {
		return fmt.Errorf("select failed: %w", err)
	}

	outcome, ok := res.Value.Val().(map[string]interface{})
	if !ok {
		return fmt.Errorf("select failed: cannot read the result")
	}

	if found, _ := outcome["found"].(bool); !found {
		return fmt.Errorf("option %q not found\n  available: %v", value, outcome["options"])
	}

	return nil
}

// selectFromPopup drives a custom dropdown: press the control, then press the option.
//
// Both presses dispatch the full pointer sequence, because a dropdown that opens on
// mousedown — most of them — saw nothing at all from the bare element.click() this used
// to do, and the command reported success anyway.
func selectFromPopup(page *rod.Page, el *rod.Element, value string) error {
	if err := browse.PressElement(el, "click"); err != nil {
		return fmt.Errorf("cannot open the dropdown: %w", err)
	}

	time.Sleep(300 * time.Millisecond)

	// One JS call finds and presses in one shot: browse.FindByCSS's 3s retry outlasts the
	// popup, which closes the moment anything else touches the page.
	js := fmt.Sprintf(`(function(){
		// scl's own popup, then any list of options a design library draws (role=option)
		var sels = [".Popup .row", ".Popup .option", ".Popup li", "[role=option]"];
		var lower = %q.toLowerCase();
		var all = [];
		for (var s = 0; s < sels.length; s++) {
			var els = document.querySelectorAll(sels[s]);
			for (var i = 0; i < els.length; i++) {
				if (!els[i].getClientRects().length) continue;
				var t = els[i].textContent.replace(/\s+/g, " ").trim();
				if (t) all.push(t);
				if (t.toLowerCase() === lower || t.toLowerCase().indexOf(lower) !== -1) {
					var el = els[i];
					var r = el.getBoundingClientRect();
					var opts = {
						bubbles: true, cancelable: true, view: window,
						clientX: r.left + r.width / 2, clientY: r.top + r.height / 2,
						button: 0, buttons: 1,
					};
					el.dispatchEvent(new PointerEvent("pointerdown", opts));
					el.dispatchEvent(new MouseEvent("mousedown", opts));
					el.dispatchEvent(new PointerEvent("pointerup", opts));
					el.dispatchEvent(new MouseEvent("mouseup", opts));
					// Guarded like browse.PressElement's: an option row that is not an
					// HTMLElement has no click(), and the throw would cost the
					// press after its mouse events had already gone out.
					if (el.click) { el.click(); }
					else { el.dispatchEvent(new MouseEvent("click", opts)); }
					return JSON.stringify({found: true});
				}
			}
		}
		return JSON.stringify({found: false, options: all.slice(0, 60)});
	})()`, value)

	var lastResult string

	// The popup may need a moment to render.
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}

		res, err := proto.RuntimeEvaluate{Expression: js}.Call(page)
		if err != nil {
			continue
		}

		lastResult = res.Result.Value.Str()
		if strings.Contains(lastResult, `"found":true`) {
			return nil
		}
	}

	// Close the popup so the page is left as it was found.
	page.KeyActions().Press(input.Escape).Do()
	time.Sleep(100 * time.Millisecond)

	return fmt.Errorf("option %q not found\n  available: %s", value, lastResult)
}

// cmdType is peek type <text>, or peek type --stdin: the text read from standard input and
// never printed, for a password. An argument shows in the process list and the output echoes
// what was typed; stdin does neither. One final newline is dropped (echo, a file).
func cmdType(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek type <text> | peek type --stdin")
	}
	secret := len(args) == 1 && args[0] == "--stdin"

	text := strings.Join(args, " ")
	if secret {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read standard input: %w", err)
		}
		text = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		if text == "" {
			return fmt.Errorf("nothing to type: standard input is empty")
		}
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	typeText := page.InsertText
	if pacedFor(ctx) {
		typeText = func(text string) error { return humanType(page, text) }
	}
	if err := typeText(text); err != nil {
		return fmt.Errorf("type failed: %w", err)
	}

	if secret {
		fmt.Printf("typed %d characters from standard input\n", len([]rune(text)))
		return nil
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

	keys, err := resolveCombo(args[0])
	if err != nil {
		return err
	}

	if err := page.KeyActions().Press(keys...).Do(); err != nil {
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

	q, err := browse.ParseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := browse.FindElement(page, q)
	if err != nil {
		return err
	}

	if err := reveal(ctx, page, el); err != nil {
		return err
	}

	// A disabled control is legitimately hovered — that is how its tooltip is read — so
	// only being unseeable or covered is a refusal here.
	r, err := browse.CheckReach(el)
	if err != nil {
		return err
	}
	if r.Unseen != "" {
		return fmt.Errorf("%s is %s — nobody could hover it", browse.DescribeQuery(q), r.Unseen)
	}
	if r.Covered != "" {
		return fmt.Errorf("%s is behind %s — the pointer would be over that instead",
			browse.DescribeQuery(q), r.Covered)
	}

	hover := hoverElement
	if pacedFor(ctx) {
		hover = func(el *rod.Element) error { return humanHover(ctx, page, el) }
	}
	if err := hover(el); err != nil {
		return err
	}

	fmt.Printf("hovering %s\n", browse.DescribeQuery(q))
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

	from, err := browse.FindByText(page, args[0])
	if err != nil {
		return fmt.Errorf("from element: %w", err)
	}

	to, err := browse.FindByText(page, args[1])
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

// resolveCombo reads a key or a chord — "Enter", "Control+k", "Shift+Meta+Enter" — as the keys
// to hold down, modifiers first. Without it a chord was read as its first letter, so
// "Control+k" pressed a plain C and fired whatever that key means on the page.
func resolveCombo(combo string) ([]input.Key, error) {
	// The plus key is spelled "+" alone, or "++" after its modifiers ("Shift++"): splitting on
	// "+" would read it as an empty key.
	body := ""
	last := "+"
	switch {
	case combo == "+":
		body = ""
	case strings.HasSuffix(combo, "++"):
		body = strings.TrimSuffix(combo, "++")
	default:
		cut := strings.LastIndex(combo, "+")
		body = ""
		if cut >= 0 {
			body = combo[:cut]
		}
		last = combo[cut+1:]
	}

	keys := []input.Key{}
	if body != "" {
		for _, part := range strings.Split(body, "+") {
			switch strings.ToLower(part) {
			case "control", "ctrl":
				keys = append(keys, input.ControlLeft)
			case "shift":
				keys = append(keys, input.ShiftLeft)
			case "alt", "option":
				keys = append(keys, input.AltLeft)
			case "meta", "cmd", "command":
				keys = append(keys, input.MetaLeft)
			default:
				return nil, fmt.Errorf("unknown modifier %q in %q (Control, Shift, Alt, Meta)",
					part, combo)
			}
		}
	}

	if last == "" {
		return nil, fmt.Errorf("no key in %q", combo)
	}

	key := resolveKey(last)
	keys = append(keys, key)

	return keys, nil
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

// hoverElement moves the pointer onto the element in events rather than through rod's
// real-mouse path, which scroll-hangs on complex layouts exactly like the click path did.
func hoverElement(el *rod.Element) error {
	js := `() => {
		const r = this.getBoundingClientRect()
		const opts = {
			bubbles: true,
			cancelable: true,
			view: window,
			clientX: r.left + r.width / 2,
			clientY: r.top + r.height / 2,
		}

		this.dispatchEvent(new PointerEvent("pointerover", opts))
		this.dispatchEvent(new MouseEvent("mouseover", opts))
		this.dispatchEvent(new PointerEvent("pointermove", opts))
		this.dispatchEvent(new MouseEvent("mousemove", opts))
	}`

	if _, err := el.Eval(js); err != nil {
		return fmt.Errorf("hover failed: %w", err)
	}

	return nil
}
