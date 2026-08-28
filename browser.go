package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

type cmdContext struct {
	port     int
	headless bool
	workers  int
	pid      int // Chrome process ID (used by tests for cleanup)
}

// chromeTarget represents a target from Chrome's /json/list endpoint.
type chromeTarget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

func connect(ctx *cmdContext) (*rod.Browser, *rod.Page, error) {
	browser, err := connectBrowser(ctx)
	if err != nil {
		return nil, nil, err
	}

	// Find a real page target (skip chrome:// internal pages).
	targetID, err := findPageTarget(ctx.port)
	if err != nil {
		return nil, nil, err
	}

	page, err := browser.PageFromTarget(proto.TargetTargetID(targetID))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to page: %w", err)
	}

	// Every command arms the page's own account of itself — this document and the next one.
	// Here rather than in the commands that navigate, because the ones that DON'T navigate are
	// the problem: a click that submits a form lands on a document nobody instrumented, and the
	// throw it does on the way in is the message you most wanted. See capture.go.
	installCapture(page)

	return browser, page, nil
}

func connectBrowser(ctx *cmdContext) (*rod.Browser, error) {
	u, err := launcher.ResolveURL(fmt.Sprintf("localhost:%d", ctx.port))
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Chrome on port %d: %w\nRun: peek open", ctx.port, err)
	}

	browser := rod.New().ControlURL(u).NoDefaultDevice()
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return browser, nil
}

// findPageTarget queries Chrome's /json/list and returns the ID of
// the last real page target (skipping chrome:// internal pages).
func findPageTarget(port int) (string, error) {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/json/list", port))
	if err != nil {
		return "", fmt.Errorf("failed to query Chrome targets: %w", err)
	}
	defer resp.Body.Close()

	var targets []chromeTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return "", fmt.Errorf("failed to parse Chrome targets: %w", err)
	}

	// Find the last real page (most recently created).
	var best string
	for _, t := range targets {
		if t.Type == "page" && !strings.HasPrefix(t.URL, "chrome://") {
			best = t.ID
		}
	}

	if best == "" {
		// Fallback: any page target.
		for _, t := range targets {
			if t.Type == "page" {
				return t.ID, nil
			}
		}
		return "", fmt.Errorf("no pages open in Chrome")
	}

	return best, nil
}
