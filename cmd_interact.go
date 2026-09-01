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

	q, err := parseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := findElement(page, q)
	if err != nil {
		return err
	}

	if err := refuseUnclickable(el, describeQuery(q)); err != nil {
		return err
	}

	if err := pressElement(el, "click"); err != nil {
		return err
	}

	fmt.Printf("clicked %s\n", describeQuery(q))
	return nil
}

// reach is what the page says about whether an element could be interacted with at all.
//
// Facts, not a verdict: hovering something disabled is legitimate, clicking it is not, so
// each command reads the ones it cares about.
type reach struct {
	disabled bool
	unseen   string // why it cannot be seen, or ""
	covered  string // what is painted on top, or ""
}

// checkReach scrolls the element into view and then asks the page about it.
//
// A JS click reaches the element directly: through an open dialog's backdrop, through
// pointer-events:none, through anything. That is what makes it immune to the scroll hang of
// rod's real-mouse path, and it is also how this tool can report "clicked" for an interaction
// no user could perform. Refusing is the whole point of a verification tool.
//
// It asks the browser rather than reasoning about z-index: elementFromPoint is the authority
// on what a click at that point would hit. An element containing the hit (its own label) or
// contained by it counts as itself.
//
// It SCROLLS first, deliberately. Declining to judge whatever sat outside the viewport was a
// hole exactly where the answer matters — an element parked off screen is the one a person
// cannot click. The scroll is a plain scrollIntoView, not rod's mouse path, so nothing hangs.
func checkReach(el *rod.Element) (reach, error) {
	res, err := el.Eval(`() => {
		this.scrollIntoView({ block: "center", inline: "center" })

		const style = getComputedStyle(this)
		const r = this.getBoundingClientRect()

		let unseen = ""
		if (style.display === "none") { unseen = "not rendered (display:none)" }
		else if (style.visibility === "hidden") { unseen = "invisible (visibility:hidden)" }
		else if (Number(style.opacity) === 0) { unseen = "fully transparent (opacity:0)" }
		else if (r.width <= 0 || r.height <= 0) { unseen = "zero-sized" }
		else if (style.pointerEvents === "none") { unseen = "click-through (pointer-events:none)" }
		else if (r.bottom < 0 || r.right < 0 || r.top > innerHeight || r.left > innerWidth) {
			unseen = "outside the viewport even after scrolling to it"
		}

		const disabled = !!this.disabled ||
			this.getAttribute("aria-disabled") === "true" ||
			!!this.closest("fieldset[disabled]")

		let covered = ""
		if (!unseen) {
			const x = r.left + r.width / 2
			const y = r.top + r.height / 2
			const at = document.elementFromPoint(x, y)
			if (at && at !== this && !this.contains(at) && !at.contains(this)) {
				const text = (at.textContent || "").replace(/\s+/g, " ").trim().slice(0, 40)
				covered = text ? at.tagName.toLowerCase() + ' "' + text + '"' : at.tagName.toLowerCase()
			}
		}

		return { disabled: disabled, unseen: unseen, covered: covered }
	}`)
	if err != nil {
		return reach{}, fmt.Errorf("cannot check whether the element is reachable: %w", err)
	}

	facts, ok := res.Value.Val().(map[string]interface{})
	if !ok {
		return reach{}, fmt.Errorf("cannot read the element's state")
	}

	var r reach
	if v, ok := facts["disabled"].(bool); ok {
		r.disabled = v
	}
	r.unseen = fmt.Sprint(facts["unseen"])
	r.covered = fmt.Sprint(facts["covered"])

	return r, nil
}

// refuseUnclickable is the gate every pressing command goes through.
func refuseUnclickable(el *rod.Element, desc string) error {
	r, err := checkReach(el)
	if err != nil {
		return err
	}

	if r.disabled {
		return fmt.Errorf("%s is disabled — a user cannot press it, and pressing it does nothing",
			desc)
	}
	if r.unseen != "" {
		return fmt.Errorf("%s is %s — nobody could press it", desc, r.unseen)
	}
	if r.covered != "" {
		return fmt.Errorf("%s is behind %s — a user pressing there would hit that instead",
			desc, r.covered)
	}

	return nil
}

// pressElement dispatches the events a real press produces, in order.
//
// `element.click()` alone fires ONE click event and no pointer or mouse events at all, so
// every control that opens on mousedown — most custom dropdowns, menus, drag handles —
// did nothing while peek printed success. The native call still goes last because it is
// what carries the activation behaviour: a form submits, a label focuses its input, a
// checkbox toggles.
func pressElement(el *rod.Element, kind string) error {
	button := "0"
	activation := `this.click()`

	switch kind {
	case "rightclick":
		button = "2"
		activation = `this.dispatchEvent(new MouseEvent("contextmenu", opts))`
	case "dblclick":
		activation = `this.click()
		this.dispatchEvent(new MouseEvent("click", opts))
		this.dispatchEvent(new MouseEvent("dblclick", opts))`
	}

	js := fmt.Sprintf(`() => {
		const r = this.getBoundingClientRect()
		const opts = {
			bubbles: true,
			cancelable: true,
			view: window,
			clientX: r.left + r.width / 2,
			clientY: r.top + r.height / 2,
			button: %s,
			buttons: %s,
		}

		this.dispatchEvent(new PointerEvent("pointerdown", opts))
		this.dispatchEvent(new MouseEvent("mousedown", opts))
		if (this.focus) { this.focus() }
		this.dispatchEvent(new PointerEvent("pointerup", opts))
		this.dispatchEvent(new MouseEvent("mouseup", opts))
		%s
	}`, button, buttonsFor(button), activation)

	if _, err := el.Eval(js); err != nil {
		return fmt.Errorf("%s failed: %w", kind, err)
	}

	return nil
}

// buttonsFor maps the pressed button to the bitmask MouseEvent.buttons wants for it.
func buttonsFor(button string) string {
	if button == "2" {
		return "2"
	}
	return "1"
}

func cmdRightClick(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek rightclick [--css <sel>] [--id <id>] [text]")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	q, err := parseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := findElement(page, q)
	if err != nil {
		return err
	}

	if err := refuseUnclickable(el, describeQuery(q)); err != nil {
		return err
	}

	if err := pressElement(el, "rightclick"); err != nil {
		return err
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

	q, err := parseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := findElement(page, q)
	if err != nil {
		return err
	}

	if err := refuseUnclickable(el, describeQuery(q)); err != nil {
		return err
	}

	if err := pressElement(el, "dblclick"); err != nil {
		return err
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
		el, err = findByCSS(page, selector, "")
		desc = fmt.Sprintf("name=%q", nameAttr)

	case cssAttr != "":
		if len(rest) == 0 {
			return fmt.Errorf("usage: peek fill --css <selector> <value>")
		}
		el, err = findByCSS(page, cssAttr, "")
		desc = fmt.Sprintf("css=%q", cssAttr)

	default:
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

	refuse, err := refusesInput(el)
	if err != nil {
		return err
	}
	if refuse != "" {
		return fmt.Errorf("%s is %s — typing into it does nothing", desc, refuse)
	}

	value := strings.Join(rest, " ")

	if err := el.SelectAllText(); err != nil {
		return fmt.Errorf("cannot select the text of %s: %w", desc, err)
	}
	if err := page.Keyboard.Press(input.Backspace); err != nil {
		return fmt.Errorf("cannot clear %s: %w", desc, err)
	}
	if err := el.Input(value); err != nil {
		return fmt.Errorf("fill failed: %w", err)
	}

	landed, err := fieldValue(el)
	if err != nil {
		return err
	}

	if landed == "" && value != "" {
		return fmt.Errorf("%s is still empty after typing %q — nothing landed", desc, value)
	}

	if landed != value {
		fmt.Printf("filled %s = %q — the field shows %q\n", desc, value, landed)
		return nil
	}

	fmt.Printf("filled %s = %q\n", desc, value)
	return nil
}

// refusesInput reports why a field cannot take typing, or "" when it can.
//
// A disabled or readonly input swallows every keystroke and leaves no trace. Typing into
// one and reporting "filled" is the same lie as clicking something covered: the command
// ran, the value is not there, and the next step builds on a field that was never set.
func refusesInput(el *rod.Element) (string, error) {
	res, err := el.Eval(`() => {
		if (this.disabled) { return "disabled" }
		if (this.readOnly) { return "readonly" }
		const style = getComputedStyle(this)
		if (style.display === "none" || style.visibility === "hidden") { return "not visible" }
		return ""
	}`)
	if err != nil {
		return "", fmt.Errorf("cannot inspect the field: %w", err)
	}

	return res.Value.Str(), nil
}

// fieldValue reads back what a field actually holds after typing.
//
// The point is not to second-guess the widget: a money or date input that reformats
// what was typed is working correctly, and the caller is told what it shows rather
// than refused. What it catches is the field that stayed empty, or a controlled input
// that reverted the value the moment it was set.
func fieldValue(el *rod.Element) (string, error) {
	res, err := el.Eval(`() => {
		if (this.value !== undefined && this.value !== null) { return String(this.value) }
		return this.textContent || ""
	}`)
	if err != nil {
		return "", fmt.Errorf("cannot read the field back: %w", err)
	}

	return res.Value.Str(), nil
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

	q, err := parseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := findElement(page, q)
	if err != nil {
		return err
	}

	// A disabled control is legitimately hovered — that is how its tooltip is read — so
	// only being unseeable or covered is a refusal here.
	r, err := checkReach(el)
	if err != nil {
		return err
	}
	if r.unseen != "" {
		return fmt.Errorf("%s is %s — nobody could hover it", describeQuery(q), r.unseen)
	}
	if r.covered != "" {
		return fmt.Errorf("%s is behind %s — the pointer would be over that instead",
			describeQuery(q), r.covered)
	}

	if err := hoverElement(el); err != nil {
		return err
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
