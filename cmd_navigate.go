package main

import (
	"fmt"
	neturl "net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func cmdNavigate(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek navigate <url>")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	if err := page.Navigate(args[0]); err != nil {
		return fmt.Errorf("navigate failed: %w", err)
	}

	if err := page.Timeout(10 * time.Second).WaitLoad(); err != nil {
		return fmt.Errorf("wait load failed: %w", err)
	}

	return reportLanding(page, args[0])
}

// reportLanding prints where the browser ended up and, when the server said something
// other than OK, says so.
//
// A 404 or a 500 navigates perfectly: the document loads, the URL prints and the command
// exits 0. Every screenshot after that is of a stack trace, and the caller has been told
// nothing. The status is on the navigation timing entry, so there is nothing to intercept.
func reportLanding(page *rod.Page, asked string) error {
	res, err := page.Eval(`() => {
		const nav = performance.getEntriesByType("navigation")[0]
		return {
			url: location.href,
			status: nav && nav.responseStatus !== undefined ? nav.responseStatus : 0,
		}
	}`)
	if err != nil {
		return fmt.Errorf("cannot read the landing page: %w", err)
	}

	landing, ok := res.Value.Val().(map[string]interface{})
	if !ok {
		return fmt.Errorf("cannot read the landing page")
	}

	url := fmt.Sprint(landing["url"])
	status := int(asFloat(landing["status"]))

	if status >= 400 {
		return fmt.Errorf("%s answered %d — the page that loaded is the server's error page",
			url, status)
	}

	if signedOut(asked, url) {
		return fmt.Errorf("%s is a sign-in page: the site's session has ended — sign in by hand "+
			"in the browser's window (peek unlock), then go on", url)
	}

	fmt.Println(url)
	return nil
}

// signInHosts are the hosts a site sends a person to sign in, which nobody opens to do anything
// else: Google's, Apple's, Microsoft's and Amazon's accounts.
var signInHosts = []string{"accounts.google.com", "idmsa.apple.com", "appleid.apple.com",
	"login.microsoftonline.com", "signin.aws.amazon.com"}

// signedOut says whether a page that was asked for landed on a sign-in page instead: the session
// it rode on has ended. Asking for the sign-in page itself is not that, nor a reload (asked "").
func signedOut(asked, landed string) bool {
	if asked == "" {
		return false
	}
	a, err := neturl.Parse(asked)
	if err != nil {
		return false
	}
	l, err := neturl.Parse(landed)
	if err != nil {
		return false
	}
	signIn := func(u *neturl.URL) bool {
		path := strings.ToLower(u.Path)
		return slices.Contains(signInHosts, u.Hostname()) || path == "/login" ||
			strings.HasPrefix(path, "/login/") || path == "/signin" || strings.HasPrefix(path, "/signin/")
	}
	return signIn(l) && !signIn(a)
}

func cmdReload(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	if err := page.Reload(); err != nil {
		return fmt.Errorf("reload failed: %w", err)
	}

	if err := page.Timeout(10 * time.Second).WaitLoad(); err != nil {
		return fmt.Errorf("wait load failed: %w", err)
	}

	return reportLanding(page, "")
}

func cmdBack(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	_, err = page.Eval(`() => window.history.back()`)
	if err != nil {
		return fmt.Errorf("back failed: %w", err)
	}

	page.Timeout(10 * time.Second).WaitLoad()
	info, _ := page.Info()
	if info != nil {
		fmt.Println(info.URL)
	}
	return nil
}

func cmdResize(ctx *cmdContext, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: peek resize <width> <height>")
	}

	width, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid width: %s", args[0])
	}

	height, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("invalid height: %s", args[1])
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("width and height must be positive")
	}

	browser, err := connectBrowser(ctx)
	if err != nil {
		return err
	}

	// Find the window for the active target.
	targetID, err := findPageTarget(ctx.port)
	if err != nil {
		return err
	}

	result, err := proto.BrowserGetWindowForTarget{
		TargetID: proto.TargetTargetID(targetID),
	}.Call(browser)
	if err != nil {
		return fmt.Errorf("get window failed: %w", err)
	}

	setWindow := func(w, h int) error {
		err := proto.BrowserSetWindowBounds{
			WindowID: result.WindowID,
			Bounds:   &proto.BrowserBounds{Width: &w, Height: &h},
		}.Call(browser)
		if err != nil {
			return fmt.Errorf("resize failed: %w", err)
		}
		return nil
	}
	if err := setWindow(width, height); err != nil {
		return err
	}

	// The window is sized with its frame, so the page gets less: grow it by the frame until
	// the page has what was asked. The page is then that size by itself, with no override.
	// An override is the fallback for what the window cannot reach (Chrome's desktop window is
	// at least 500px wide, even headless; a 390px phone needs the page set to it) — and only
	// then, because Chrome drops an override when the connection that set it closes and every
	// command sets it again: a resize each time, and every popup of the client closes on resize.
	forgetViewport(ctx.port)
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}
	if err := (proto.EmulationClearDeviceMetricsOverride{}).Call(page); err != nil {
		return fmt.Errorf("clear the viewport: %w", err)
	}
	inner := func() (int, int, error) {
		size, err := page.Eval(`() => [innerWidth, innerHeight, outerWidth, outerHeight]`)
		if err != nil {
			return 0, 0, fmt.Errorf("read the viewport: %w", err)
		}
		all := size.Value.Arr()
		w, h := all[0].Int(), all[1].Int()
		if err := setWindow(width+all[2].Int()-w, height+all[3].Int()-h); err != nil {
			return 0, 0, err
		}
		size, err = page.Eval(`() => [innerWidth, innerHeight]`)
		if err != nil {
			return 0, 0, fmt.Errorf("read the viewport: %w", err)
		}
		all = size.Value.Arr()
		return all[0].Int(), all[1].Int(), nil
	}
	gotWidth, gotHeight, err := inner()
	if err != nil {
		return err
	}
	if gotWidth != width || gotHeight != height {
		err = proto.EmulationSetDeviceMetricsOverride{
			Width: width, Height: height, DeviceScaleFactor: 1, Mobile: false,
		}.Call(page)
		if err != nil {
			return fmt.Errorf("resize viewport failed: %w", err)
		}
		if err := rememberViewport(ctx.port, width, height); err != nil {
			return fmt.Errorf("remember the viewport: %w", err)
		}
	}

	fmt.Printf("resized to %dx%d\n", width, height)
	return nil
}

func cmdForward(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	_, err = page.Eval(`() => window.history.forward()`)
	if err != nil {
		return fmt.Errorf("forward failed: %w", err)
	}

	page.Timeout(10 * time.Second).WaitLoad()
	info, _ := page.Info()
	if info != nil {
		fmt.Println(info.URL)
	}
	return nil
}
