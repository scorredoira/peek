package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func cmdPages(ctx *cmdContext) error {
	pages, err := listPageTargets(ctx.port)
	if err != nil {
		return err
	}

	if len(pages) == 0 {
		fmt.Println("(no pages open)")
		return nil
	}

	current, err := findPageTarget(ctx.port)
	if err != nil {
		return err
	}

	for i, t := range pages {
		mark := " "
		if t.ID == current {
			mark = "*"
		}
		fmt.Printf("%s [%d] %s\n", mark, i, t.URL)
	}

	fmt.Println("\n* is the tab every command acts on. `peek page <index>` moves it.")
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

	pages, err := listPageTargets(ctx.port)
	if err != nil {
		return err
	}

	if idx < 0 || idx >= len(pages) {
		return fmt.Errorf("index %d out of range (0-%d)", idx, len(pages)-1)
	}

	target := pages[idx]

	browser, err := connectBrowser(ctx)
	if err != nil {
		return err
	}

	page, err := browser.PageFromTarget(proto.TargetTargetID(target.ID))
	if err != nil {
		return fmt.Errorf("failed to connect to page: %w", err)
	}

	if _, err := page.Activate(); err != nil {
		return fmt.Errorf("failed to activate page: %w", err)
	}

	// The choice has to outlive this process or the next command guesses again.
	if err := rememberTab(ctx.port, target.ID); err != nil {
		return fmt.Errorf("cannot remember the selected tab: %w", err)
	}

	fmt.Printf("switched to [%d] %s\n", idx, target.URL)
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

	// A new tab is the one you meant to work in, so it becomes the current one.
	if err := rememberTab(ctx.port, string(page.TargetID)); err != nil {
		return fmt.Errorf("cannot remember the new tab: %w", err)
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

	forgetTab(ctx.port)

	fmt.Printf("closed %q\n", title)
	return nil
}
