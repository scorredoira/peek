package main

import (
	"strings"
	"testing"
)

// A screen that catches its failure and says it in an alert is as broken as one that throws:
// the shot fails with the alert's words. A dialog that is not an alert, or an alert that is not
// on screen, is no failure.
func TestShotFailsOnAnOpenAlert(t *testing.T) {
	ctx := chromeForTest(t)
	_, page, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := page.Eval(`() => {
		document.body.innerHTML =
			'<div role="dialog">Choose a day</div>' +
			'<div role="alertdialog" style="display:none">An old failure</div>'
	}`); err != nil {
		t.Fatal(err)
	}
	errors, err := readPageErrors(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(errors) != 0 {
		t.Fatalf("no alert is open, and the shot says %q", errors)
	}

	if _, err := page.Eval(`() => {
		document.body.innerHTML += '<div role="alertdialog">Something went wrong. The record could not be read</div>'
	}`); err != nil {
		t.Fatal(err)
	}
	errors, err = readPageErrors(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(errors) != 1 || !strings.Contains(errors[0], "Something went wrong. The record could not be read") {
		t.Fatalf("an alert is open, and the shot says %q", errors)
	}
}
