package browse

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
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

// FindByText finds an element by its name in the accessibility tree.
// Retries for up to 3 seconds if not found immediately.
func FindByText(page *rod.Page, text string) (*rod.Element, error) {
	return FindByRole(page, "", text)
}

// FindByRole finds an element by its name among those of one accessibility role (tab, link,
// checkbox…); "" is any role. The same name is often on a link and a tab ("Páginas" in a menu
// and in a page's tabs), and by text alone the link wins.
func FindByRole(page *rod.Page, role, text string) (*rod.Element, error) {
	deadline := time.Now().Add(retryTimeout)

	for {
		nodes, err := FullAXTree(page)
		if err != nil && !cutByLeaving(err) {
			return nil, err
		}

		var el *rod.Element
		if err == nil {
			el, err = pickByText(page, nodes, role, text)
		}
		if err != nil && !cutByLeaving(err) {
			return nil, err
		}
		if el != nil {
			return el, nil
		}

		if time.Now().After(deadline) {
			if role != "" {
				return nil, fmt.Errorf("%s with text %q not found", role, text)
			}
			return nil, fmt.Errorf("element with text %q not found", text)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// FullAXTree is the accessibility tree of the page and of every frame drawn inside it: a
// payment form in an iframe (a bank's page in a shop's checkout) is on the screen a person
// looks at, so finding by text reaches it too. Chrome answers a tree per document, so each
// frame of the page's process is asked on its own. A frame in another process (a site of
// another origin, isolated) is a target of its own that this session does not drive: Chrome
// refuses its tree, and it is left out.
func FullAXTree(page *rod.Page) ([]*proto.AccessibilityAXNode, error) {
	tree, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("accessibility tree failed: %w", err)
	}
	nodes := tree.Nodes

	frames, err := proto.PageGetFrameTree{}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("frame tree failed: %w", err)
	}
	var children []*proto.PageFrameTree
	children = append(children, frames.FrameTree.ChildFrames...)
	for len(children) > 0 {
		frame := children[0]
		children = append(children[1:], frame.ChildFrames...)

		inner, err := proto.AccessibilityGetFullAXTree{FrameID: frame.Frame.ID}.Call(page)
		if err != nil {
			continue // another process's frame: not this session's to read
		}
		nodes = append(nodes, inner.Nodes...)
	}
	return nodes, nil
}

// FindInput finds an input element by its label in the accessibility tree.
// Retries for up to 3 seconds if not found immediately.
func FindInput(page *rod.Page, label string) (*rod.Element, error) {
	lower := strings.ToLower(label)
	deadline := time.Now().Add(retryTimeout)

	for {
		nodes, err := FullAXTree(page)
		if err != nil {
			return nil, err
		}

		for _, node := range nodes {
			if node.Ignored {
				continue
			}
			role := AXValueStr(node.Role)
			name := AXValueStr(node.Name)

			switch role {
			case "textbox", "searchbox", "combobox", "spinbutton", "slider", "textarea":
				if matchesLabel(name, lower) {
					return resolveAXNode(page, node)
				}
			}
		}

		// Fallback: try by HTML name attribute (single attempt, no retry).
		// Compared in JS rather than spliced into a selector, so no label can make it throw.
		byName := fmt.Sprintf(`Array.prototype.find.call(
			document.querySelectorAll("input, textarea"), function(e) { return e.name === %q }
		) || null`, label)
		el, err := queryElement(page, byName)
		if err != nil {
			return nil, err
		}
		if el != nil {
			return el, nil
		}

		// Last: the words written beside the field. A page in Spanish can name its field in
		// English for the screen reader (Play Console's "Nombre del paquete" is "App package
		// name"): the field a person means is the first drawn one after those words.
		el, err = queryElement(page, inputAfterText(label))
		if err != nil {
			return nil, err
		}
		if el != nil {
			return el, nil
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("input with label %q not found", label)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// pickByText is the element a person would mean by that text: of the nodes it names (the
// exact name first, then a name that contains it; interactive before static text), the first
// one that is actually drawn. The same text is often on the page twice — a menu's link folded
// away on a phone, a calendar popup's day kept in the DOM — and the hidden twin is never the
// one meant. With none drawn it answers the best match anyway, so the command reports why it
// cannot act on it. Nil when nothing matches.
func pickByText(
	page *rod.Page, nodes []*proto.AccessibilityAXNode, role, text string,
) (*rod.Element, error) {
	exact := findAXNodes(nodes, role, text, true)
	partial := findAXNodes(nodes, role, text, false)
	candidates := append(exact, partial...)
	if len(candidates) == 0 {
		return nil, nil
	}

	// Being on screen only chooses among candidates of the same kind: an exact match before a
	// partial one, a control before its text. A heading that says "Sign in" over the form is on
	// screen while the button under it waits below the fold, and the button is the one meant.
	tiers := [][]*proto.AccessibilityAXNode{
		interactiveOnly(exact, true), interactiveOnly(exact, false),
		interactiveOnly(partial, true), interactiveOnly(partial, false),
	}
	for _, tier := range tiers {
		el, err := pickDrawn(page, tier, text)
		if err != nil || el != nil {
			return el, err
		}
	}

	return resolveAXNode(page, candidates[0])
}

// interactiveOnly keeps the nodes that are controls (want true) or that are not.
func interactiveOnly(nodes []*proto.AccessibilityAXNode, want bool) []*proto.AccessibilityAXNode {
	var out []*proto.AccessibilityAXNode
	for _, node := range nodes {
		_, interactive := interactiveRoles[AXValueStr(node.Role)]
		if interactive == want {
			out = append(out, node)
		}
	}
	return out
}

// pickDrawn is, of candidates, the first one a person is looking at right now (its middle is
// on screen and it, not something over it or a scroller clipping it, is what is there), then
// the first one that is drawn at all — a click scrolls to it. Nil when none is drawn.
func pickDrawn(
	page *rod.Page, candidates []*proto.AccessibilityAXNode, text string,
) (*rod.Element, error) {
	var drawn *rod.Element
	for _, node := range candidates {
		el, err := resolveAXNode(page, node)
		if err != nil {
			return nil, err
		}
		seen, err := el.Eval(`() => {
			const rects = this.getClientRects()
			if (rects.length === 0) { return 0 }
			const style = getComputedStyle(this)
			const box = rects[0]
			if (style.visibility === "hidden" || box.width === 0 || box.height === 0) { return 0 }
			const x = box.left + box.width / 2
			const y = box.top + box.height / 2
			if (x < 0 || y < 0 || x >= innerWidth || y >= innerHeight) { return 1 }
			// followed into shadow roots, as CheckReach does
			let hit = document.elementFromPoint(x, y)
			while (hit && hit.shadowRoot) {
				const inner = hit.shadowRoot.elementFromPoint(x, y)
				if (!inner || inner === hit) { break }
				hit = inner
			}
			// a radio or checkbox drawn by a design library over its transparent input: the
			// drawing is the control (CheckReach)
			let self = this
			if ((this.type === "radio" || this.type === "checkbox") && Number(style.opacity) === 0) {
				let up = this.parentElement
				while (up && (Number(getComputedStyle(up).opacity) === 0 ||
					up.getBoundingClientRect().width <= 0)) { up = up.parentElement }
				if (up) { self = up }
			}
			return hit != null && (hit === self || self.contains(hit)) ? 2 : 1
		}`)
		if err != nil {
			return nil, fmt.Errorf("cannot read whether %q is drawn: %w", text, err)
		}
		switch seen.Value.Int() {
		case 2:
			return el, nil
		case 1:
			if drawn == nil {
				drawn = el
			}
		}
	}
	return drawn, nil
}

// findAXNodes lists the nodes of the accessibility tree with that name, best first:
// interactive roles by their priority, then static text, each in document order. A role keeps
// only the nodes of that role.
func findAXNodes(
	nodes []*proto.AccessibilityAXNode, role, text string, exact bool,
) []*proto.AccessibilityAXNode {
	text = readable(text)
	lower := strings.ToLower(text)

	type ranked struct {
		node     *proto.AccessibilityAXNode
		priority int
	}
	var found []ranked

	for _, node := range nodes {
		// the page itself is named by its title, which a text never means
		if node.Ignored || node.BackendDOMNodeID == 0 || AXValueStr(node.Role) == "RootWebArea" {
			continue
		}
		if role != "" && AXValueStr(node.Role) != role {
			continue
		}
		name := readable(AXValueStr(node.Name))
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

		priority, isInteractive := interactiveRoles[AXValueStr(node.Role)]
		if !isInteractive {
			priority = 999
		}
		found = append(found, ranked{node: node, priority: priority})
	}

	sort.SliceStable(found, func(i, j int) bool { return found[i].priority < found[j].priority })

	out := make([]*proto.AccessibilityAXNode, len(found))
	for i, r := range found {
		out[i] = r.node
	}
	return out
}

// resolveAXNode converts an accessibility tree node to a Rod element.
// If the node resolves to a Text node (e.g. StaticText / InlineTextBox),
// it walks up to the nearest parent Element so callers can safely click,
// hover, or call getComputedStyle on it.
func resolveAXNode(page *rod.Page, node *proto.AccessibilityAXNode) (*rod.Element, error) {
	if node.BackendDOMNodeID == 0 {
		return nil, fmt.Errorf("node %q has no DOM backing", AXValueStr(node.Name))
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
	nameLower := strings.ToLower(readable(name))
	nameLower = strings.TrimRight(nameLower, " *")
	return nameLower == lowerLabel || strings.Contains(nameLower, lowerLabel)
}

// readable is a text as a person reads it: without the characters nobody sees — the zero-width
// ones some frameworks put between letters ("N\u200bo\u200bm\u200bb\u200br\u200be") and the
// direction marks and isolates an amount is wrapped in — and with a space where a hard space
// sits ("29,00\u00a0€"). Typing what is on screen finds it.
func readable(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\ufeff', '\u200e', '\u200f',
			'\u2066', '\u2067', '\u2068', '\u2069':
			return -1
		case '\u00a0', '\u202f':
			return ' '
		}
		return r
	}, s)
}

// FindByCSS finds an element by CSS selector, optionally filtered by text content.
// Uses RuntimeEvaluate for fast single-call DOM queries.
// Retries for up to 3 seconds if not found immediately.
func FindByCSS(page *rod.Page, selector, textFilter string) (*rod.Element, error) {
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
		el, err := queryElement(page, jsCode)
		if err != nil {
			return nil, fmt.Errorf("css %q: %w", selector, err)
		}
		if el != nil {
			return el, nil
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
//
// It asks getElementById, never the selector "#"+id: an id is any string, and one that
// starts with a digit ("1st") is no selector at all while one with a dot ("a.b") is a
// DIFFERENT selector — id "a" with class "b" — that finds nothing or the wrong element.
func findByID(page *rod.Page, id string) (*rod.Element, error) {
	deadline := time.Now().Add(retryTimeout)
	jsCode := fmt.Sprintf(`document.getElementById(%q)`, id)

	for {
		el, err := queryElement(page, jsCode)
		if err != nil {
			return nil, fmt.Errorf("id %q: %w", id, err)
		}
		if el != nil {
			return el, nil
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no element with id %q", id)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// findByRef finds the node a snapshot printed as [ref]: its accessibility node id, which
// stays the same while the node is on the page.
func findByRef(page *rod.Page, ref string) (*rod.Element, error) {
	nodes, err := FullAXTree(page)
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		if string(node.NodeID) == ref {
			return resolveAXNode(page, node)
		}
	}
	return nil, fmt.Errorf("no node [%s] on the page: take a new snapshot", ref)
}

// queryElement evaluates an expression that yields one DOM element or null, and returns
// the element, nil when there is none, or an error.
func queryElement(page *rod.Page, expr string) (*rod.Element, error) {
	res, err := proto.RuntimeEvaluate{Expression: expr}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("evaluate failed: %w", err)
	}

	obj, err := nodeResult(res)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}

	el, err := page.ElementFromObject(obj)
	if err != nil {
		return nil, fmt.Errorf("cannot take the element: %w", err)
	}
	return el, nil
}

// nodeResult reads an evaluation that should have produced a DOM element.
//
// An expression that THROWS still returns a result — the exception object, with an
// ObjectID of its own — so trusting ObjectID alone wrapped a DOMException as the element,
// and the first thing done with it died with "this.scrollIntoView is not a function".
// Anything that is neither null nor a node is an error for the same reason.
func nodeResult(res *proto.RuntimeEvaluateResult) (*proto.RuntimeRemoteObject, error) {
	if err := Thrown(res); err != nil {
		return nil, err
	}

	obj := res.Result
	if obj == nil || obj.Subtype == proto.RuntimeRemoteObjectSubtypeNull ||
		obj.Type == proto.RuntimeRemoteObjectTypeUndefined {
		return nil, nil
	}
	if obj.Subtype != proto.RuntimeRemoteObjectSubtypeNode {
		return nil, fmt.Errorf("the lookup gave %s, not an element", describeRemote(obj))
	}

	return obj, nil
}

// Thrown is the exception an evaluation raised, as an error, or nil when it raised none.
func Thrown(res *proto.RuntimeEvaluateResult) error {
	if res.ExceptionDetails == nil {
		return nil
	}

	msg := res.ExceptionDetails.Text
	if ex := res.ExceptionDetails.Exception; ex != nil && ex.Description != "" {
		msg = ex.Description
	}
	return fmt.Errorf("the page threw: %s", msg)
}

// describeRemote names a remote object for an error message.
func describeRemote(obj *proto.RuntimeRemoteObject) string {
	if obj.Description != "" {
		return obj.Description
	}
	if obj.Subtype != "" {
		return string(obj.Subtype)
	}
	return string(obj.Type)
}

// ElementQuery holds parsed flags for element finding.
type ElementQuery struct {
	CSS   string // --css selector
	ID    string // --id selector
	Ref   string // --ref: the [N] a snapshot prints before a node
	Index int    // --index N (0-based), -1 means not set
	Role  string // --role: only elements of that accessibility role, found by their text
	Text  string // remaining positional args (text filter)
}

// ParseElementArgs extracts --css, --id, --ref and --index flags from args.
// Remaining args are joined as the text filter.
//
// A flag with no value, or an --index that is not a number, is an ERROR and never a
// shrug: dropping it silently leaves the command targeting by text instead, which
// finds SOME element and acts on it. Acting on the wrong element while reporting
// success is the one thing this tool exists not to do.
func ParseElementArgs(args []string) (ElementQuery, error) {
	var q ElementQuery
	q.Index = -1
	var rest []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--css":
			if i+1 >= len(args) {
				return q, fmt.Errorf("--css needs a selector")
			}
			q.CSS = args[i+1]
			i++
		case "--id":
			if i+1 >= len(args) {
				return q, fmt.Errorf("--id needs an id")
			}
			q.ID = args[i+1]
			i++
		case "--ref":
			if i+1 >= len(args) {
				return q, fmt.Errorf("--ref needs the number a snapshot prints")
			}
			q.Ref = args[i+1]
			i++
		case "--role":
			if i+1 >= len(args) {
				return q, fmt.Errorf("--role needs a role (tab, link, button, checkbox…)")
			}
			q.Role = args[i+1]
			i++
		case "--index":
			if i+1 >= len(args) {
				return q, fmt.Errorf("--index needs a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return q, fmt.Errorf("--index: %q is not a number", args[i+1])
			}
			q.Index = n
			i++
		default:
			rest = append(rest, args[i])
		}
	}

	q.Text = strings.Join(rest, " ")
	if q.Role != "" && (q.CSS != "" || q.ID != "" || q.Ref != "") {
		return q, fmt.Errorf("--role goes with a text, not with --css, --id or --ref")
	}
	if q.Role != "" && q.Text == "" {
		return q, fmt.Errorf("--role needs the element's text")
	}
	return q, nil
}

// FindElement finds an element using the parsed query.
// Priority: --css > --id > --ref > text (AX tree).
func FindElement(page *rod.Page, q ElementQuery) (*rod.Element, error) {
	if q.CSS != "" {
		return findByCSSIndex(page, q.CSS, q.Text, q.Index)
	}
	if q.ID != "" {
		return findByID(page, q.ID)
	}
	if q.Ref != "" {
		return findByRef(page, q.Ref)
	}
	if q.Text == "" {
		return nil, fmt.Errorf("no selector or text provided")
	}
	return FindByRole(page, q.Role, q.Text)
}

// FindDropdownTrigger is the control that opens a design library's dropdown: a button or a
// combobox whose name starts with the label, then says what is chosen ("Categoría Campo
// obligatorio No seleccionado" in Play Console). Its required mark aside.
func FindDropdownTrigger(page *rod.Page, label string) (*rod.Element, error) {
	wanted := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.ToLower(label)), "*"))
	nodes, err := FullAXTree(page)
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		if node.Ignored {
			continue
		}
		role := AXValueStr(node.Role)
		if role != "button" && role != "combobox" {
			continue
		}
		name := strings.ToLower(strings.Join(strings.Fields(AXValueStr(node.Name)), " "))
		if name == wanted || strings.HasPrefix(name, wanted+" ") {
			return resolveAXNode(page, node)
		}
	}
	return nil, fmt.Errorf("no dropdown for label %q", label)
}

// FindSelectByLabel finds a Select widget or input inside the form field
// (FormCell/FormField) that contains a label matching the given text. This
// handles the common pattern where the label "Type" is a sibling of the
// Select widget inside a shared container.
func FindSelectByLabel(page *rod.Page, label string) (*rod.Element, error) {
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
		el, err := queryElement(page, jsCode)
		if err != nil {
			return nil, err
		}
		if el != nil {
			return el, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no select widget for label %q", label)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// DescribeQuery returns a human-readable description of how the element was found.
func DescribeQuery(q ElementQuery) string {
	if q.CSS != "" && q.Text != "" {
		return fmt.Sprintf("css %q text %q", q.CSS, q.Text)
	}
	if q.CSS != "" {
		return fmt.Sprintf("css %q", q.CSS)
	}
	if q.ID != "" {
		return fmt.Sprintf("id %q", q.ID)
	}
	if q.Ref != "" {
		return fmt.Sprintf("[%s]", q.Ref)
	}
	return fmt.Sprintf("%q", q.Text)
}

// Reach is what the page says about whether an element could be interacted with at all.
//
// Facts, not a verdict: hovering something disabled is legitimate, clicking it is not, so
// each command reads the ones it cares about.
type Reach struct {
	// Gone is an element no longer on the page: the page drew it again, and the one on
	// screen now is another element
	Gone     bool
	Disabled bool
	Unseen   string // why it cannot be seen, or ""
	Covered  string // what is painted on top, or ""
	// Link is where the element leads when it is a link (or inside one), or "": a link that
	// is covered is still reachable by going to its address
	Link string
}

// CheckReach scrolls the element into view and then asks the page about it.
//
// A JS click reaches the element directly: through an open dialog's backdrop, through
// pointer-events:none, through anything. That is what makes it immune to the scroll hang of
// rod's real-mouse path, and it is also how this tool can report "clicked" for an interaction
// no user could perform. Refusing is the whole point of a verification tool.
//
// It asks the browser rather than reasoning about z-index: elementFromPoint is the authority
// on what a click at that point would hit. An element containing the hit (its own label) or
// contained by it counts as itself. The hit is followed into shadow roots: a page's panel
// drawn in one (the website's app) is the host at the document's level, and its buttons are
// what a press there reaches.
//
// It SCROLLS first when the element is not wholly in view, deliberately. Declining to judge
// whatever sat outside the viewport was a hole exactly where the answer matters — an element
// parked off screen is the one a person cannot click. The scroll is a plain scrollIntoView, not
// rod's mouse path, so nothing hangs. One already in view stays where it is: a page that jumps
// under a press nobody scrolled to is what a site watching for machines looks for.
func CheckReach(el *rod.Element) (Reach, error) {
	res, err := el.Eval(`() => {
		// taken off the page (its screen drew it again): its style reads as empty, which
		// would be judged transparent
		if (!this.isConnected) { return { gone: true } }
		const box = this.getBoundingClientRect()
		if (box.top < 0 || box.left < 0 || box.bottom > innerHeight || box.right > innerWidth) {
			this.scrollIntoView({ block: "center", inline: "center" })
		}

		// A radio or a checkbox a design library draws itself (Material's) is a transparent
		// native input under its drawing: the person presses the drawing, which holds the
		// input, so the drawing is what is judged.
		let self = this
		if ((this.type === "radio" || this.type === "checkbox") &&
			Number(getComputedStyle(this).opacity) === 0) {
			let up = this.parentElement
			while (up && (Number(getComputedStyle(up).opacity) === 0 ||
				up.getBoundingClientRect().width <= 0)) { up = up.parentElement }
			if (up) { self = up }
		}

		const style = getComputedStyle(self)
		const r = self.getBoundingClientRect()

		let unseen = ""
		if (style.display === "none") { unseen = "not rendered (display:none)" }
		else if (style.visibility === "hidden") { unseen = "invisible (visibility:hidden)" }
		else if (Number(style.opacity) === 0) {
			// what is transparent and why, so a refusal that only shows under load says
			// which element it measured and what was moving it
			const what = self.tagName.toLowerCase() +
				Array.from(self.classList).map(c => "." + c).join("")
			const moving = self.getAnimations().map(a =>
				(a.animationName || a.transitionProperty || "animation") + " " + a.playState +
				" at " + Math.round(Number(a.currentTime)) + "ms")
			unseen = "fully transparent (opacity:0) — " + what +
				(moving.length > 0 ? ", " + moving.join(", ") : ", no animation on it")
		}
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
			let at = document.elementFromPoint(x, y)
			while (at && at.shadowRoot) {
				const inner = at.shadowRoot.elementFromPoint(x, y)
				if (!inner || inner === at) { break }
				at = inner
			}
			if (at && at !== self && !self.contains(at) && !at.contains(self)) {
				const text = (at.textContent || "").replace(/\s+/g, " ").trim().slice(0, 40)
				covered = text ? at.tagName.toLowerCase() + ' "' + text + '"' : at.tagName.toLowerCase()
			}
		}

		const link = this.closest("a[href]")
		const href = link ? link.href : ""

		return { disabled: disabled, unseen: unseen, covered: covered, href: href }
	}`)
	if err != nil {
		return Reach{}, fmt.Errorf("cannot check whether the element is reachable: %w", err)
	}

	facts, ok := res.Value.Val().(map[string]interface{})
	if !ok {
		return Reach{}, fmt.Errorf("cannot read the element's state")
	}

	var r Reach
	if v, ok := facts["gone"].(bool); ok && v {
		r.Gone = true
		return r, nil
	}
	if v, ok := facts["disabled"].(bool); ok {
		r.Disabled = v
	}
	r.Unseen = fmt.Sprint(facts["unseen"])
	r.Covered = fmt.Sprint(facts["covered"])
	r.Link = fmt.Sprint(facts["href"])

	return r, nil
}

// cutByLeaving is a question to the page that its going to another one cut: the document it
// was asked of is gone, and the next one is what to ask. rod retries some of these itself
// (page.Eval, a query's wait), not the one Chrome gives when the tab swaps its process for
// another site's ("Inspected target navigated or closed").
func cutByLeaving(err error) bool {
	if errors.Is(err, cdp.ErrCtxNotFound) || errors.Is(err, cdp.ErrCtxDestroyed) {
		return true
	}
	var e *cdp.Error
	return errors.As(err, &e) && e.Code == -32000 &&
		e.Message == "Inspected target navigated or closed"
}

// ErrGone is a press refused because its element was taken off the page: whoever found it
// finds it again.
var ErrGone = errors.New("no longer on the page; it was drawn again")

// RefuseUnclickable is the check every pressing command goes through. Like finding, it waits
// up to 3 seconds: a menu fading in is transparent for its first frames, and a person waits
// for it too. What is still not pressable then is refused. An element taken off the page is
// refused at once with ErrGone: waiting cannot bring it back.
func RefuseUnclickable(el *rod.Element, desc string) error {
	deadline := time.Now().Add(retryTimeout)
	for {
		err := refuseUnclickableNow(el, desc)
		if err == nil || errors.Is(err, ErrGone) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// refuseUnclickableNow is RefuseUnclickable asked once.
func refuseUnclickableNow(el *rod.Element, desc string) error {
	r, err := CheckReach(el)
	if err != nil {
		return err
	}

	if r.Gone {
		return fmt.Errorf("%s: %w", desc, ErrGone)
	}
	if r.Disabled {
		return fmt.Errorf("%s is disabled — a user cannot press it, and pressing it does nothing",
			desc)
	}
	if r.Unseen != "" {
		return fmt.Errorf("%s is %s — nobody could press it", desc, r.Unseen)
	}
	if r.Covered != "" && r.Link != "" {
		return fmt.Errorf("%s is behind %s — a user pressing there would hit that instead; "+
			"it is a link: peek navigate %q", desc, r.Covered, r.Link)
	}
	if r.Covered != "" {
		return fmt.Errorf("%s is behind %s — a user pressing there would hit that instead",
			desc, r.Covered)
	}

	return nil
}

// clickEvent is the click a press ends with, at the same point as the events before it.
//
// It used to be the native `element.click()`, for its activation behaviour, but that call
// fires its click at (0,0): a canvas that reads where it was clicked (the timetable's free
// slot) was pressed at its corner. A dispatched click carries the activation behaviour all
// the same — a checkbox toggles, a label reaches its input, a button submits its form, a link
// navigates — and works on an `<svg>` icon, which has no `click()` of its own.
const clickEvent = `this.dispatchEvent(new MouseEvent("click", opts))`

// PressElement dispatches the events a real press produces, in order.
//
// `element.click()` alone fires ONE click event and no pointer or mouse events at all, so
// every control that opens on mousedown — most custom dropdowns, menus, drag handles —
// did nothing while peek printed success. The pointer events say they come from a mouse, as a
// real press does: a control that tells a finger from a pointer (a Canvas) ignored a press of
// no kind at all. The click goes last, where it carries the activation behaviour.
func PressElement(el *rod.Element, kind string) error {
	button := "0"
	activation := clickEvent

	switch kind {
	case "rightclick":
		button = "2"
		activation = `this.dispatchEvent(new MouseEvent("contextmenu", opts))`
	case "dblclick":
		activation = clickEvent + `
		this.dispatchEvent(new MouseEvent("click", { ...opts, detail: 2 }))
		this.dispatchEvent(new MouseEvent("dblclick", { ...opts, detail: 2 }))`
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
			detail: 1,
			pointerId: 1,
			pointerType: "mouse",
			isPrimary: true,
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

// RefusesInput reports why a field cannot take typing, or "" when it can.
//
// A disabled or readonly input swallows every keystroke and leaves no trace. Typing into
// one and reporting "filled" is the same lie as clicking something covered: the command
// ran, the value is not there, and the next step builds on a field that was never set.
func RefusesInput(el *rod.Element) (string, error) {
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

// FillElement types value into a field the way a person does, and answers what the field
// shows afterwards: a field that refuses typing, or that is still empty after it, is an error.
//
// Focus and clear in JS. rod's own SelectAllText and Input go through Focus, which scrolls the
// element into view — a path that hangs on complex layouts. Focusing directly cannot hang. The
// text goes in as real insertion rather than an assignment, so a field that reacts to typing —
// a search-as-you-type, an autocomplete — reacts.
func FillElement(page *rod.Page, el *rod.Element, value string) (string, error) {
	refuse, err := RefusesInput(el)
	if err != nil {
		return "", err
	}
	if refuse != "" {
		return "", fmt.Errorf("the field is %s — typing into it does nothing", refuse)
	}

	if _, err := el.Eval(`() => {
		this.focus()
		if (this.value !== undefined && this.value !== null) {
			this.value = ""
			this.dispatchEvent(new Event("input", { bubbles: true }))
		}
	}`); err != nil {
		return "", fmt.Errorf("cannot focus the field: %w", err)
	}

	if err := page.InsertText(value); err != nil {
		return "", fmt.Errorf("fill failed: %w", err)
	}

	landed, err := FieldValue(el)
	if err != nil {
		return "", err
	}
	if landed == "" && value != "" {
		return "", fmt.Errorf("still empty after typing %q — nothing landed", value)
	}

	return landed, nil
}

// FieldValue reads back what a field actually holds after typing.
//
// The point is not to second-guess the widget: a money or date input that reformats
// what was typed is working correctly, and the caller is told what it shows rather
// than refused. What it catches is the field that stayed empty, or a controlled input
// that reverted the value the moment it was set.
func FieldValue(el *rod.Element) (string, error) {
	res, err := el.Eval(`() => {
		if (this.value !== undefined && this.value !== null) { return String(this.value) }
		return this.textContent || ""
	}`)
	if err != nil {
		return "", fmt.Errorf("cannot read the field back: %w", err)
	}

	return res.Value.Str(), nil
}

func PresentInAXTree(page *rod.Page, text string) (bool, error) {
	nodes, err := FullAXTree(page)
	if err != nil {
		return false, err
	}

	lower := strings.ToLower(readable(text))

	for _, node := range nodes {
		if node.Ignored {
			continue
		}

		name := readable(AXValueStr(node.Name))
		if name != "" && strings.Contains(strings.ToLower(name), lower) {
			return true, nil
		}

		value := readable(AXValueStr(node.Value))
		if value != "" && strings.Contains(strings.ToLower(value), lower) {
			return true, nil
		}
	}

	return false, nil
}

func PresentInText(page *rod.Page, text string) (bool, error) {
	js := fmt.Sprintf(`() => {
		const readable = s => String(s || "").replace(/[\u00a0\u202f]/g, " ")
			.replace(/[\u200b-\u200f\ufeff\u2066-\u2069]/g, "").toLowerCase()
		const wanted = %q.toLowerCase()
		const body = document.body
		if (!body) { return false }
		if (readable(body.innerText).indexOf(wanted) >= 0) { return true }
		for (const el of document.querySelectorAll("input, textarea")) {
			if (readable(el.value).indexOf(wanted) >= 0) { return true }
		}
		return false
	}`, readable(text))

	res, err := page.Eval(js)
	if err != nil {
		return false, err
	}

	return res.Value.Bool(), nil
}

// AXValueStr is an accessibility value as text, "" for none.
func AXValueStr(v *proto.AccessibilityAXValue) string {
	if v == nil {
		return ""
	}
	return v.Value.Str()
}

// inputAfterText is JS answering the first drawn text field that follows, in the page's order,
// the drawn element whose own words are text (case and spaces aside); null when there is none.
func inputAfterText(text string) string {
	return fmt.Sprintf(`(function() {
		// a required field's mark ("Nombre *") is not part of its words, and may be an element
		// of its own: the words are matched on an element's whole text, the innermost first
		var plain = function(t) { return t.normalize("NFC").replace(/\s+/g, " ").trim().replace(/\s*\*$/, "").toLowerCase() }
		var wanted = plain(%q)
		var drawn = function(e) { return e.getClientRects().length > 0 }
		var all = document.querySelectorAll("body *")
		var at = -1
		for (var i = 0; i < all.length; i++) {
			var e = all[i]
			if (!drawn(e) || plain(e.textContent || "") !== wanted) { continue }
			var inner = Array.prototype.some.call(e.children, function(c) { return plain(c.textContent || "") === wanted })
			if (!inner) { at = i; break }
		}
		if (at < 0) { return null }
		for (var j = at + 1; j < all.length; j++) {
			var f = all[j]
			if ((f.tagName == "TEXTAREA" || f.tagName == "INPUT" && /^(text|email|search|tel|url|number|password|)$/.test(f.type)) && drawn(f)) { return f }
		}
		return null
	})()`, text)
}
