package main

import (
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

// A tab is picked by its index, or by words of its address that only it has: the index of a
// tab the person moved names another one.
func TestPickPage(t *testing.T) {
	pages := []chromeTarget{
		{ID: "a", URL: "https://play.google.com/console/app/1/publishing"},
		{ID: "b", URL: "https://appstoreconnect.apple.com/apps"},
		{ID: "c", URL: "https://play.google.com/console/app/1/store-listings"},
	}
	cases := []struct {
		arg  string
		want int
		err  string
	}{
		{"1", 1, ""},
		{"appstoreconnect", 1, ""},
		{"STORE-LISTINGS", 2, ""},
		{"play.google", 0, "2 tabs"},
		{"nothing", 0, "no tab"},
		{"7", 0, "out of range"},
	}
	for _, c := range cases {
		got, err := pickPage(pages, c.arg)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Fatalf("%s: want an error with %q, got %v", c.arg, c.err, err)
			}
			if !strings.Contains(err.Error(), "appstoreconnect.apple.com") {
				t.Fatalf("%s: the error lists the tabs: %v", c.arg, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("%s: want %d, got %d (%v)", c.arg, c.want, got, err)
		}
	}
}

// A script that throws says what it threw, not Chrome's bare "Uncaught".
func TestJSExceptionSaysWhatWasThrown(t *testing.T) {
	d := &proto.RuntimeExceptionDetails{Text: "Uncaught", Exception: &proto.RuntimeRemoteObject{
		Description: "TypeError: Cannot read properties of null (reading 'innerText')\n    at <anonymous>:1:41"}}
	if got := jsException(d); !strings.Contains(got, "TypeError: Cannot read properties of null") {
		t.Fatal(got)
	}
	bare := &proto.RuntimeExceptionDetails{Text: "Uncaught", LineNumber: 0, ColumnNumber: 9}
	if got := jsException(bare); got != "Uncaught at line 1, column 10" {
		t.Fatal(got)
	}
}
