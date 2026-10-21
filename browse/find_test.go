package browse

import (
	"strings"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func TestNodeResultRefusesAThrownLookup(t *testing.T) {
	// What Chrome answers for document.querySelector("#1st"): the DOMException, with an
	// ObjectID of its own, next to the exception details.
	res := &proto.RuntimeEvaluateResult{
		Result: &proto.RuntimeRemoteObject{
			Type:        proto.RuntimeRemoteObjectTypeObject,
			Subtype:     proto.RuntimeRemoteObjectSubtypeError,
			ObjectID:    "exception-1",
			Description: "SyntaxError: '#1st' is not a valid selector.",
		},
		ExceptionDetails: &proto.RuntimeExceptionDetails{
			Text: "Uncaught",
			Exception: &proto.RuntimeRemoteObject{
				Description: "SyntaxError: '#1st' is not a valid selector.",
			},
		},
	}

	obj, err := nodeResult(res)
	if err == nil {
		t.Fatalf("want an error, got the object %+v", obj)
	}
	if !strings.Contains(err.Error(), "not a valid selector") {
		t.Fatalf("the error should say why: %v", err)
	}
}

func TestNodeResultReadsNoMatchAsNone(t *testing.T) {
	cases := []*proto.RuntimeEvaluateResult{
		{Result: &proto.RuntimeRemoteObject{
			Type:    proto.RuntimeRemoteObjectTypeObject,
			Subtype: proto.RuntimeRemoteObjectSubtypeNull,
		}},
		{Result: &proto.RuntimeRemoteObject{Type: proto.RuntimeRemoteObjectTypeUndefined}},
		{},
	}

	for i, res := range cases {
		obj, err := nodeResult(res)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if obj != nil {
			t.Fatalf("case %d: want none, got %+v", i, obj)
		}
	}
}

func TestNodeResultRefusesWhatIsNotAnElement(t *testing.T) {
	res := &proto.RuntimeEvaluateResult{Result: &proto.RuntimeRemoteObject{
		Type:        proto.RuntimeRemoteObjectTypeObject,
		Subtype:     proto.RuntimeRemoteObjectSubtypeArray,
		ObjectID:    "array-1",
		Description: "Array(2)",
	}}

	obj, err := nodeResult(res)
	if err == nil {
		t.Fatalf("want an error, got the object %+v", obj)
	}
	if !strings.Contains(err.Error(), "Array(2)") {
		t.Fatalf("the error should name what came back: %v", err)
	}
}

func TestNodeResultTakesAnElement(t *testing.T) {
	res := &proto.RuntimeEvaluateResult{Result: &proto.RuntimeRemoteObject{
		Type:     proto.RuntimeRemoteObjectTypeObject,
		Subtype:  proto.RuntimeRemoteObjectSubtypeNode,
		ObjectID: "node-1",
	}}

	obj, err := nodeResult(res)
	if err != nil {
		t.Fatal(err)
	}
	if obj == nil || obj.ObjectID != "node-1" {
		t.Fatalf("want the node, got %+v", obj)
	}
}

// The same text twice on a page, one of them folded away — a phone's menu link, a calendar
// popup kept in the DOM — means the one a person can see, whichever comes first in the tree.
func TestFindByTextPrefersTheOneThatIsDrawn(t *testing.T) {
	page := chromePage(t)
	html := `<html><body>
		<nav><a id="hidden" href="#a" style="display: block; width: 0; height: 0; overflow: hidden">Book a court</a></nav>
		<main><a id="shown" href="#b">Book a court</a></main>
	</body></html>`
	if err := page.SetDocumentContent(html); err != nil {
		t.Fatalf("cannot set the page content: %v", err)
	}

	el, err := FindByText(page, "Book a court")
	if err != nil {
		t.Fatal(err)
	}
	id, err := el.Attribute("id")
	if err != nil || id == nil {
		t.Fatalf("cannot read the element's id: %v", err)
	}
	if *id != "shown" {
		t.Fatalf("picked %q, want the drawn one", *id)
	}
}

func TestFindByRoleTakesTheTabOverTheLink(t *testing.T) {
	page := chromePage(t)
	html := `<html><body>
		<nav><a id="menu" href="#pages">Pages</a></nav>
		<main><div role="tablist"><div id="tab" role="tab" tabindex="0">Pages</div></div></main>
	</body></html>`
	if err := page.SetDocumentContent(html); err != nil {
		t.Fatalf("cannot set the page content: %v", err)
	}

	byText, err := FindByText(page, "Pages")
	if err != nil {
		t.Fatal(err)
	}
	byRole, err := FindByRole(page, "tab", "Pages")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		el   *rod.Element
		want string
	}{{byText, "menu"}, {byRole, "tab"}} {
		id, err := c.el.Attribute("id")
		if err != nil || id == nil {
			t.Fatalf("cannot read the element's id: %v", err)
		}
		if *id != c.want {
			t.Fatalf("picked %q, want %q", *id, c.want)
		}
	}

	if _, err := FindByRole(page, "checkbox", "Pages"); err == nil {
		t.Fatal("found a checkbox named Pages, and there is none")
	}
}

func TestParseElementArgsRole(t *testing.T) {
	q, err := ParseElementArgs([]string{"--role", "tab", "Páginas"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Role != "tab" || q.Text != "Páginas" {
		t.Fatalf("got role %q text %q", q.Role, q.Text)
	}
	for _, args := range [][]string{{"--role"}, {"--role", "tab"}, {"--role", "tab", "--css", "a", "x"}} {
		if _, err := ParseElementArgs(args); err == nil {
			t.Fatalf("%v: accepted", args)
		}
	}
}

func TestACoveredLinkSaysWhereItLeads(t *testing.T) {
	page := chromePage(t)
	html := `<html><body>
		<a id="mail" href="/mailbox/7" style="position: absolute; top: 40px; left: 40px">santi</a>
		<div style="position: fixed; inset: 0; background: white">Offer</div>
	</body></html>`
	if err := page.SetDocumentContent(html); err != nil {
		t.Fatalf("cannot set the page content: %v", err)
	}
	el, err := page.Element("#mail")
	if err != nil {
		t.Fatal(err)
	}

	err = refuseUnclickableNow(el, `"santi"`)
	if err == nil {
		t.Fatal("pressed a link a banner covers")
	}
	if !strings.Contains(err.Error(), "peek navigate") || !strings.Contains(err.Error(), "/mailbox/7") {
		t.Fatalf("the refusal does not say where the link leads: %v", err)
	}
}
