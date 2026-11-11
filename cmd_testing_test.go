package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A directory of tests that peek cannot read all of is an error, never a smaller suite: a
// sub-directory it could not open would drop its tests and still report green.
func TestPeekTestFailsWhenItCannotReadADirectory(t *testing.T) {
	dir := t.TempDir()
	hidden := filepath.Join(dir, "hidden")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "a.peek"), []byte("open x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hidden, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(hidden, 0o755) })

	err := cmdTest(&cmdContext{workers: 1}, []string{dir})
	if err == nil || !strings.Contains(err.Error(), "walk "+dir) {
		t.Fatalf("peek test over an unreadable directory: %v, want the walk's error", err)
	}
}

// Only ${NAME} is a variable: os.ExpandEnv also took $NAME and $1, so "$25.00" in an
// assertion became "5.00" and the check passed against the wrong amount.
func TestExpandVarsOnlyTakesTheBracedForm(t *testing.T) {
	t.Setenv("PEEK_TEST_AMOUNT", "25.00")
	got := expandVars(`assert text "$25.00 ${PEEK_TEST_AMOUNT} ${ID} $HOME"`, map[string]string{"ID": "7"})
	want := `assert text "$25.00 25.00 7 $HOME"`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A promise is always truthy: a check written as fetch().then(...) passed without its answer
// being read. assert js waits for it.
func TestAssertJSAwaitsAPromise(t *testing.T) {
	ctx := chromeForTest(t)
	if err := assertJS(ctx, "Promise.resolve(false)", time.Second); err == nil {
		t.Fatal("a promise of false passed")
	}
	if err := assertJS(ctx, "new Promise(r => setTimeout(() => r(1), 50))", time.Second); err != nil {
		t.Fatalf("a promise of 1 failed: %v", err)
	}
	if err := assertJS(ctx, "1 + 1 === 2", time.Second); err != nil {
		t.Fatalf("a plain expression failed: %v", err)
	}
}

// A test of a refusal (booking a blocked slot, saving a form with a field missing) gets a 4xx
// on purpose: `assert error` says so, and the errors that say it no longer fail the test. The
// failed request carries what the server answered, so it can be told by those words too.
func TestAssertErrorExcusesTheRefusalItAsserts(t *testing.T) {
	ctx := chromeForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/save" {
			http.Error(w, `{"error":"No availability at 08:00"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body><button onclick="fetch('/save').then(r => r.text()).then(t => {
			throw new Error(JSON.parse(t).error) })">Book</button></body></html>`)
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
	if _, page, err = connect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := page.Eval(`() => document.querySelector("button").click()`); err != nil {
		t.Fatal(err)
	}

	if err := assertError(ctx, "Something else", 500*time.Millisecond); err == nil {
		t.Fatal("an error the page never reported was asserted")
	}
	if err := assertError(ctx, "No availability", 3*time.Second); err != nil {
		t.Fatal(err)
	}

	all, err := pageErrors(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	fetched := false
	for _, e := range all {
		if strings.HasPrefix(e, "fetch ") && strings.Contains(e, "No availability") {
			fetched = true
		}
	}
	if !fetched {
		t.Fatalf("the failed request does not say what the server answered: %q", all)
	}
	if left := unexpectedErrors(ctx, all); len(left) != 0 {
		t.Fatalf("the asserted refusal still fails the test: %q", left)
	}
}
