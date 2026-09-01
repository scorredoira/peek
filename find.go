package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const retryTimeout = 3 * time.Second

// Interactive roles, in priority order.
var interactiveRoles = map[string]int{
	"button":           1,
	"link":             2,
	"menuitem":         3,
	"tab":              4,
	"checkbox":         5,
	"radio":            6,
	"switch":           7,
	"combobox":         8,
	"textbox":          9,
	"searchbox":        10,
	"option":           11,
	"menuitemcheckbox": 12,
	"menuitemradio":    13,
}

// findByText finds an element by its name in the accessibility tree.
// Retries for up to 3 seconds if not found immediately.
func findByText(page *rod.Page, text string) (*rod.Element, error) {
	deadline := time.Now().Add(retryTimeout)

	for {
		tree, err := proto.AccessibilityGetFullAXTree{}.Call(page)
		if err != nil {
			return nil, fmt.Errorf("accessibility tree failed: %w", err)
		}

		node := findAXNode(tree.Nodes, text, true)
		if node == nil {
			node = findAXNode(tree.Nodes, text, false)
		}
		if node != nil {
			return resolveAXNode(page, node)
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("element with text %q not found", text)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// findInput finds an input element by its label in the accessibility tree.
// Retries for up to 3 seconds if not found immediately.
func findInput(page *rod.Page, label string) (*rod.Element, error) {
	lower := strings.ToLower(label)
	deadline := time.Now().Add(retryTimeout)

	for {
		tree, err := proto.AccessibilityGetFullAXTree{}.Call(page)
		if err != nil {
			return nil, fmt.Errorf("accessibility tree failed: %w", err)
		}

		for _, node := range tree.Nodes {
			if node.Ignored {
				continue
			}
			role := axValueStr(node.Role)
			name := axValueStr(node.Name)

			switch role {
			case "textbox", "searchbox", "combobox", "spinbutton", "slider", "textarea":
				if matchesLabel(name, lower) {
					return resolveAXNode(page, node)
				}
			}
		}

		// Fallback: try by HTML name attribute (single attempt, no retry).
		sel := fmt.Sprintf(`input[name=%q], textarea[name=%q]`, label, label)
		res, cssErr := proto.RuntimeEvaluate{Expression: fmt.Sprintf(`document.querySelector(%q)`, sel)}.Call(page)
		if cssErr == nil && res.Result != nil && res.Result.ObjectID != "" {
			el, cssErr := page.ElementFromObject(res.Result)
			if cssErr == nil {
				return el, nil
			}
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("input with label %q not found", label)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// findAXNode finds a node in the accessibility tree by name.
// Prefers interactive elements over static text.
func findAXNode(nodes []*proto.AccessibilityAXNode, text string, exact bool) *proto.AccessibilityAXNode {
	lower := strings.ToLower(text)

	var best *proto.AccessibilityAXNode
	bestPriority := 999

	for _, node := range nodes {
		if node.Ignored || node.BackendDOMNodeID == 0 {
			continue
		}
		name := stripZeroWidth(axValueStr(node.Name))
		if name == "" {
			continue
		}

		matched := false
		if exact {
			matched = strings.EqualFold(name, text)
		} else {
			matched = strings.Contains(strings.ToLower(name), lower)
		}
		if !matched {
			continue
		}

		role := axValueStr(node.Role)
		priority, isInteractive := interactiveRoles[role]
		if isInteractive {
			if priority < bestPriority {
				best = node
				bestPriority = priority
			}
		} else if best == nil {
			best = node
		}
	}

	return best
}

// resolveAXNode converts an accessibility tree node to a Rod element.
// If the node resolves to a Text node (e.g. StaticText / InlineTextBox),
// it walks up to the nearest parent Element so callers can safely click,
// hover, or call getComputedStyle on it.
func resolveAXNode(page *rod.Page, node *proto.AccessibilityAXNode) (*rod.Element, error) {
	if node.BackendDOMNodeID == 0 {
		return nil, fmt.Errorf("node %q has no DOM backing", axValueStr(node.Name))
	}

	result, err := proto.DOMResolveNode{
		BackendNodeID: node.BackendDOMNodeID,
	}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve DOM node: %w", err)
	}

	el, err := page.ElementFromObject(result.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to create element: %w", err)
	}

	// Text nodes (nodeType 3) cannot be clicked or styled.
	// Walk up to the parent element.
	nodeType, _ := el.Eval(`() => this.nodeType`)
	if nodeType != nil && nodeType.Value.Int() == 3 {
		parent, err := el.Parent()
		if err != nil {
			return nil, fmt.Errorf("failed to get parent of text node: %w", err)
		}
		return parent, nil
	}

	return el, nil
}

func matchesLabel(name, lowerLabel string) bool {
	if name == "" {
		return false
	}
	nameLower := strings.ToLower(stripZeroWidth(name))
	nameLower = strings.TrimRight(nameLower, " *")
	return nameLower == lowerLabel || strings.Contains(nameLower, lowerLabel)
}

// stripZeroWidth removes zero-width characters that some frameworks
// insert between letters in placeholder text (e.g. "N\u200bo\u200bm\u200bb\u200br\u200be").
func stripZeroWidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\ufeff':
			return -1
		}
		return r
	}, s)
}

// findByCSS finds an element by CSS selector, optionally filtered by text content.
// Uses RuntimeEvaluate for fast single-call DOM queries.
// Retries for up to 3 seconds if not found immediately.
func findByCSS(page *rod.Page, selector, textFilter string) (*rod.Element, error) {
	return findByCSSIndex(page, selector, textFilter, -1)
}

// findByCSSIndex finds an element by CSS selector. If index >= 0, returns the
// Nth element matching the selector (ignoring textFilter). If index < 0 and
// textFilter is set, filters by text content. Retries for up to 3 seconds.
func findByCSSIndex(page *rod.Page, selector, textFilter string, index int) (*rod.Element, error) {
	deadline := time.Now().Add(retryTimeout)

	var jsCode string
	if index >= 0 {
		jsCode = fmt.Sprintf(`(function(){
			var els = document.querySelectorAll(%q);
			return els.length > %d ? els[%d] : null;
		})()`, selector, index, index)
	} else if textFilter == "" {
		jsCode = fmt.Sprintf(`document.querySelector(%q)`, selector)
	} else {
		jsCode = fmt.Sprintf(`(function(){
			var lower = %q.toLowerCase();
			var els = document.querySelectorAll(%q);
			for (var i = 0; i < els.length; i++) {
				var t = els[i].textContent.trim();
				if (t.toLowerCase() === lower || t.toLowerCase().indexOf(lower) !== -1) {
					return els[i];
				}
			}
			return null;
		})()`, textFilter, selector)
	}

	for {
		res, err := proto.RuntimeEvaluate{Expression: jsCode}.Call(page)
		if err == nil && res.Result != nil && res.Result.ObjectID != "" {
			el, err := page.ElementFromObject(res.Result)
			if err == nil {
				return el, nil
			}
		}

		if time.Now().After(deadline) {
			if index >= 0 {
				return nil, fmt.Errorf("no element matching %q at index %d", selector, index)
			}
			if textFilter != "" {
				hint := cssSampleTexts(page, selector, textFilter)
				return nil, fmt.Errorf("no element matching %q with text %q%s", selector, textFilter, hint)
			}
			return nil, fmt.Errorf("no element matching %q", selector)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// cssSampleTexts returns a hint string with sample texts from elements matching
// the CSS selector, to help the user find the right text filter.
func cssSampleTexts(page *rod.Page, selector, attempted string) string {
	jsCode := fmt.Sprintf(`(function(){
		var els = document.querySelectorAll(%q);
		var texts = [];
		var seen = {};
		for (var i = 0; i < els.length && texts.length < 5; i++) {
			var t = els[i].textContent.trim();
			if (t && t.length < 40 && !seen[t]) {
				seen[t] = true;
				texts.push(t);
			}
		}
		return JSON.stringify({count: els.length, texts: texts});
	})()`, selector)

	res, err := proto.RuntimeEvaluate{Expression: jsCode}.Call(page)
	if err != nil || res.Result == nil {
		return ""
	}
	raw := res.Result.Value.Str()
	if raw == "" {
		return ""
	}
	return "\n  hint: " + raw
}

// findByID finds an element by its DOM id attribute.
// Retries for up to 3 seconds if not found immediately.
func findByID(page *rod.Page, id string) (*rod.Element, error) {
	return findByCSS(page, "#"+id, "")
}

// elementQuery holds parsed flags for element finding.
type elementQuery struct {
	css        string // --css selector
	id         string // --id selector
	index      int    // --index N (0-based), -1 means not set
	textFilter string // remaining positional args (text filter)
}

// parseElementArgs extracts --css, --id, and --index flags from args.
// Remaining args are joined as the text filter.
//
// A flag with no value, or an --index that is not a number, is an ERROR and never a
// shrug: dropping it silently leaves the command targeting by text instead, which
// finds SOME element and acts on it. Acting on the wrong element while reporting
// success is the one thing this tool exists not to do.
func parseElementArgs(args []string) (elementQuery, error) {
	var q elementQuery
	q.index = -1
	var rest []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--css":
			if i+1 >= len(args) {
				return q, fmt.Errorf("--css needs a selector")
			}
			q.css = args[i+1]
			i++
		case "--id":
			if i+1 >= len(args) {
				return q, fmt.Errorf("--id needs an id")
			}
			q.id = args[i+1]
			i++
		case "--index":
			if i+1 >= len(args) {
				return q, fmt.Errorf("--index needs a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return q, fmt.Errorf("--index: %q is not a number", args[i+1])
			}
			q.index = n
			i++
		default:
			rest = append(rest, args[i])
		}
	}

	q.textFilter = strings.Join(rest, " ")
	return q, nil
}

// findElement finds an element using the parsed query.
// Priority: --css > --id > text (AX tree).
func findElement(page *rod.Page, q elementQuery) (*rod.Element, error) {
	if q.css != "" {
		return findByCSSIndex(page, q.css, q.textFilter, q.index)
	}
	if q.id != "" {
		return findByID(page, q.id)
	}
	if q.textFilter == "" {
		return nil, fmt.Errorf("no selector or text provided")
	}
	return findByText(page, q.textFilter)
}

// findSelectByLabel finds a Select widget or input inside the form field
// (FormCell/FormField) that contains a label matching the given text. This
// handles the common pattern where the label "Type" is a sibling of the
// Select widget inside a shared container.
func findSelectByLabel(page *rod.Page, label string) (*rod.Element, error) {
	deadline := time.Now().Add(retryTimeout)
	jsCode := fmt.Sprintf(`(function(){
		var lower = %q.toLowerCase();
		var cells = document.querySelectorAll('.FormCell, .FormField, .field');
		for (var i = 0; i < cells.length; i++) {
			var labels = cells[i].querySelectorAll('.label, label');
			for (var j = 0; j < labels.length; j++) {
				if (labels[j].textContent.trim().toLowerCase().replace(/[*\s]+$/, '') === lower) {
					var sel = cells[i].querySelector('.Select, select, .Input input, input');
					if (sel) return sel;
				}
			}
		}
		return null;
	})()`, label)

	for {
		res, err := proto.RuntimeEvaluate{Expression: jsCode}.Call(page)
		if err == nil && res.Result != nil && res.Result.ObjectID != "" {
			el, err := page.ElementFromObject(res.Result)
			if err == nil {
				return el, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no select widget for label %q", label)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// describeQuery returns a human-readable description of how the element was found.
func describeQuery(q elementQuery) string {
	if q.css != "" && q.textFilter != "" {
		return fmt.Sprintf("css %q text %q", q.css, q.textFilter)
	}
	if q.css != "" {
		return fmt.Sprintf("css %q", q.css)
	}
	if q.id != "" {
		return fmt.Sprintf("id %q", q.id)
	}
	return fmt.Sprintf("%q", q.textFilter)
}
