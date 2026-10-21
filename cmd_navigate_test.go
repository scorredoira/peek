package main

import "testing"

// A page that lands on a sign-in page it was not asked for has lost its session; asking for the
// sign-in page, or landing where asked, is not that.
func TestSignedOutIsASignInPageNobodyAskedFor(t *testing.T) {
	cases := []struct {
		asked, landed string
		out           bool
	}{
		{"https://play.google.com/console/u/0/developers", "https://accounts.google.com/v3/signin/identifier?x=1", true},
		{"https://appstoreconnect.apple.com/apps", "https://appstoreconnect.apple.com/login?targetUrl=%2Fapps", true},
		{"https://developer.apple.com/account", "https://idmsa.apple.com/IDMSWebAuth/signin", true},
		{"https://accounts.google.com/", "https://accounts.google.com/v3/signin/identifier", false},
		{"https://appstoreconnect.apple.com/apps", "https://appstoreconnect.apple.com/apps", false},
		{"https://example.com/login-help", "https://example.com/login-help", false},
		{"", "https://accounts.google.com/v3/signin", false},
	}
	for _, c := range cases {
		if got := signedOut(c.asked, c.landed); got != c.out {
			t.Errorf("asked %s, landed %s: signed out %v, want %v", c.asked, c.landed, got, c.out)
		}
	}
}
