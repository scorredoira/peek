package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

const defaultTimeout = 10 * time.Second

func cmdWait(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek wait <text> | --gone <text> | --url <pattern>")
	}

	timeout := defaultTimeout

	// Parse --timeout flag if present.
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--timeout" {
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return fmt.Errorf("invalid timeout: %s", args[i+1])
			}
			timeout = d
			args = append(args[:i], args[i+2:]...)
			break
		}
	}

	switch {
	case args[0] == "--gone":
		if len(args) < 2 {
			return fmt.Errorf("usage: peek wait --gone <text>")
		}
		text := strings.Join(args[1:], " ")
		return waitGone(ctx, text, timeout)

	case args[0] == "--url":
		if len(args) < 2 {
			return fmt.Errorf("usage: peek wait --url <pattern>")
		}
		return waitURL(ctx, args[1], timeout)

	default:
		text := strings.Join(args, " ")
		return waitText(ctx, text, timeout)
	}
}

// waitText waits for text to appear in the accessibility tree.
func waitText(ctx *cmdContext, text string, timeout time.Duration) error {
	lower := strings.ToLower(text)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		_, page, err := connect(ctx)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		tree, err := proto.AccessibilityGetFullAXTree{}.Call(page)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		for _, node := range tree.Nodes {
			if node.Ignored {
				continue
			}
			name := axValueStr(node.Name)
			if name != "" && strings.Contains(strings.ToLower(name), lower) {
				fmt.Printf("found %q\n", text)
				return nil
			}
		}

		time.Sleep(300 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for text %q", text)
}

// waitGone waits for text to disappear from the accessibility tree.
func waitGone(ctx *cmdContext, text string, timeout time.Duration) error {
	lower := strings.ToLower(text)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		_, page, err := connect(ctx)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		tree, err := proto.AccessibilityGetFullAXTree{}.Call(page)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		found := false
		for _, node := range tree.Nodes {
			if node.Ignored {
				continue
			}
			name := axValueStr(node.Name)
			if name != "" && strings.Contains(strings.ToLower(name), lower) {
				found = true
				break
			}
		}

		if !found {
			fmt.Printf("gone %q\n", text)
			return nil
		}

		time.Sleep(300 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for %q to disappear", text)
}

// waitURL waits for the URL to contain a pattern.
func waitURL(ctx *cmdContext, pattern string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		_, page, err := connect(ctx)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		info, err := page.Info()
		if err == nil && strings.Contains(info.URL, pattern) {
			fmt.Println(info.URL)
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for URL matching %q", pattern)
}
