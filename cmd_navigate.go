package main

import (
	"fmt"
	"strconv"
	"time"

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

	info, _ := page.Info()
	if info != nil {
		fmt.Println(info.URL)
	}
	return nil
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

	fmt.Println("reloaded")
	return nil
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

	err = proto.BrowserSetWindowBounds{
		WindowID: result.WindowID,
		Bounds: &proto.BrowserBounds{
			Width:  &width,
			Height: &height,
		},
	}.Call(browser)
	if err != nil {
		return fmt.Errorf("resize failed: %w", err)
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
