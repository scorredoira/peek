package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// outcomePage answers a save after a while with a toast that is gone a moment later, marks a
// field invalid, and leads to a page that greets.
const outcomePage = `<html><body>
<div id="toasts" aria-live="polite"></div>
<label>Email <input id="email"></label><p id="err"></p>
<button onclick="fetch('/slow').then(r => r.text()).then(() => {
	const t = document.createElement('div'); t.textContent = 'Record saved'
	document.getElementById('toasts').append(t); setTimeout(() => t.remove(), 100) })">Save</button>
<button onclick="const e = document.getElementById('email'); e.setAttribute('aria-invalid', 'true')
	e.setAttribute('aria-describedby', 'err'); document.getElementById('err').textContent = 'Required'">Check</button>
<button onclick="confirm('Delete Ana?')">Delete</button>
<a href="/next">Next</a>
<button>Nothing</button>
</body></html>`

// What a click caused is said under it: the toast that came and went while the save was on its
// way, the field the site refused with its message, the confirm and its answer, the page it went
// to and what that page announced. A click that caused nothing says nothing more.
func TestClickSaysWhatThePageAnswered(t *testing.T) {
	ctx := chromeForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/slow":
			time.Sleep(400 * time.Millisecond)
			fmt.Fprint(w, "ok")
		case "/next":
			fmt.Fprint(w, `<html><body><div role="status">Welcome back</div></body></html>`)
		default:
			fmt.Fprint(w, outcomePage)
		}
	}))
	defer server.Close()

	_, page, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := page.Navigate(server.URL); err != nil {
		t.Fatal(err)
	}
	if err := page.WaitLoad(); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ button, want string }{
		{"Nothing", `clicked "Nothing"` + "\n"},
		{"Save", `clicked "Save"` + "\n" + `  the page said "Record saved"` + "\n"},
		{"Check", `clicked "Check"` + "\n" + `  "Email" is marked invalid: "Required"` + "\n"},
		{"Delete", `clicked "Delete"` + "\n" + `  confirm "Delete Ana?" → dismissed` + "\n"},
		{"Next", `clicked "Next"` + "\n" + `  the page said "Welcome back"` + "\n" +
			"  went to " + server.URL + "/next\n"},
	}
	for _, c := range cases {
		got := stdoutOf(t, func() error { return cmdClick(ctx, []string{c.button}) })
		if got != c.want {
			t.Fatalf("%s:\ngot  %q\nwant %q", c.button, got, c.want)
		}
	}
}

// stdoutOf is what fn printed.
func stdoutOf(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = saved
	w.Close()
	out, _ := io.ReadAll(r)
	if runErr != nil {
		t.Fatalf("%v (printed %q)", runErr, out)
	}
	return string(out)
}
