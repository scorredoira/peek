package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

	// The backstop answer for native dialogs. Lives here rather than in a global because
	// `peek test -w N` runs its files as goroutines in one process, each with its own
	// context and its own port.
	dialogs *dialogPolicy
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

	// Same reason, one step further: what the page's overrides cannot reach — a beforeunload
	// above all — stops the page dead, and nothing this process does afterwards can reach it.
	// See dialogs.go.
	armDialogs(page, ctx.dialogPolicy())

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

// listPageTargets returns the real page targets, in Chrome's own order, skipping the
// chrome:// internals. Every command that needs to know about tabs reads THIS, so the
// index `peek pages` prints is the index `peek page` selects.
func listPageTargets(port int) ([]chromeTarget, error) {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/json/list", port))
	if err != nil {
		return nil, fmt.Errorf("failed to query Chrome targets: %w", err)
	}
	defer resp.Body.Close()

	var targets []chromeTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, fmt.Errorf("failed to parse Chrome targets: %w", err)
	}

	var pages []chromeTarget
	for _, t := range targets {
		if t.Type == "page" && !strings.HasPrefix(t.URL, "chrome://") {
			pages = append(pages, t)
		}
	}

	if len(pages) == 0 {
		for _, t := range targets {
			if t.Type == "page" {
				pages = append(pages, t)
			}
		}
	}

	return pages, nil
}

// selectedTabFile is where the chosen tab is remembered between commands.
//
// peek is a CLI: every command is a process that connects, acts and exits, so a tab
// picked in one command was forgotten by the next, which went back to guessing "the last
// page target". A click that opens a popup silently moved every following command onto
// the popup — or failed to, equally invisibly — and the form you thought you were
// verifying was in the other tab.
func selectedTabFile(port int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("peek-%d.tab", port))
}

func rememberTab(port int, targetID string) error {
	return os.WriteFile(selectedTabFile(port), []byte(targetID), 0644)
}

func forgetTab(port int) {
	_ = os.Remove(selectedTabFile(port))
}

func rememberedTab(port int) string {
	data, err := os.ReadFile(selectedTabFile(port))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// findPageTarget returns the tab every command acts on: the one explicitly selected while
// it still exists, and otherwise the most recently created page.
func findPageTarget(port int) (string, error) {
	pages, err := listPageTargets(port)
	if err != nil {
		return "", err
	}

	if len(pages) == 0 {
		return "", fmt.Errorf("no pages open in Chrome")
	}

	if chosen := rememberedTab(port); chosen != "" {
		for _, t := range pages {
			if t.ID == chosen {
				return chosen, nil
			}
		}
		// The tab was closed. Fall back rather than fail, but stop claiming it.
		forgetTab(port)
	}

	// No tab was ever named — attaching to a Chrome peek did not open. Chrome lists its
	// targets newest first, so the last entry is the oldest tab: the one that was there
	// before anything peek did, which is the safer guess than following a popup.
	return pages[len(pages)-1].ID, nil
}
