package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func cmdPages(ctx *cmdContext) error {
	browser, err := connectBrowser(ctx)
	if err != nil {
		return err
	}

	pages, err := browser.Pages()
	if err != nil {
		return fmt.Errorf("failed to list pages: %w", err)
	}

	for i, p := range pages {
		info, err := p.Info()
		if err != nil {
			fmt.Printf("[%d] (error: %v)\n", i, err)
			continue
		}
		fmt.Printf("[%d] %s — %s\n", i, info.Title, info.URL)
	}
	return nil
}

func cmdPage(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek page <index>")
	}

	idx, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid index: %s", args[0])
	}

	browser, err := connectBrowser(ctx)
	if err != nil {
		return err
	}

	pages, err := browser.Pages()
	if err != nil {
		return fmt.Errorf("failed to list pages: %w", err)
	}

	if idx < 0 || idx >= len(pages) {
		return fmt.Errorf("index %d out of range (0-%d)", idx, len(pages)-1)
	}

	page := pages[idx]
	if _, err := page.Activate(); err != nil {
		return fmt.Errorf("failed to activate page: %w", err)
	}

	info, _ := page.Info()
	if info != nil {
		fmt.Printf("switched to [%d] %s\n", idx, info.Title)
	}
	return nil
}

func cmdNewPage(ctx *cmdContext, args []string) error {
	browser, err := connectBrowser(ctx)
	if err != nil {
		return err
	}

	url := "about:blank"
	if len(args) > 0 {
		url = args[0]
	}

	page, err := browser.Page(proto.TargetCreateTarget{URL: url})
	if err != nil {
		return fmt.Errorf("failed to create page: %w", err)
	}

	if url != "about:blank" {
		page.Timeout(10 * time.Second).WaitLoad()
	}

	info, _ := page.Info()
	if info != nil {
		fmt.Println(info.URL)
	}
	return nil
}

func cmdClosePage(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	info, _ := page.Info()
	title := ""
	if info != nil {
		title = info.Title
	}

	if err := page.Close(); err != nil {
		return fmt.Errorf("failed to close page: %w", err)
	}

	fmt.Printf("closed %q\n", title)
	return nil
}
