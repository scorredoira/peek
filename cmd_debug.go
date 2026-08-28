package main

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func cmdConsole(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	result, err := page.Eval(`() => {
		if (!window.__peekConsole) { return [] }
		return window.__peekConsole.map(m => m.level + ': ' + m.text)
	}`)
	if err != nil {
		return fmt.Errorf("failed to read console messages: %w", err)
	}

	arr := result.Value.Val()
	if arr == nil {
		fmt.Println("(no console messages)")
		return nil
	}

	switch messages := arr.(type) {
	case []interface{}:
		for _, m := range messages {
			fmt.Println(m)
		}
	default:
		fmt.Println(arr)
	}

	return nil
}

func getConsoleFromRuntime(page *rod.Page) error {
	result, err := page.Eval(`() => {
		return 'Console capture installed. Run your action, then call "peek console" again to see messages.';
	}`)
	if err != nil {
		return err
	}
	fmt.Println(result.Value.Str())
	return nil
}

func cmdNetwork(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	result, err := page.Eval(`() => {
		const entries = performance.getEntriesByType('resource');
		return entries.slice(-50).map(e => {
			return {
				name: e.name,
				type: e.initiatorType,
				duration: Math.round(e.duration) + 'ms',
				size: e.transferSize || 0,
			};
		});
	}`)
	if err != nil {
		return fmt.Errorf("failed to get network entries: %w", err)
	}

	arr := result.Value.Val()
	if arr == nil {
		fmt.Println("(no network entries)")
		return nil
	}

	entries, ok := arr.([]interface{})
	if !ok {
		fmt.Println(arr)
		return nil
	}

	for _, entry := range entries {
		m, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		name := fmt.Sprint(m["name"])
		if len(name) > 80 {
			name = name[:77] + "..."
		}
		fmt.Printf("%-6s %-8s %s  %s\n",
			m["duration"], m["type"],
			formatBytes(m["size"]), name)
	}

	return nil
}

func formatBytes(v interface{}) string {
	var n float64
	switch val := v.(type) {
	case float64:
		n = val
	case int:
		n = float64(val)
	default:
		return "?"
	}

	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1fMB", n/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", n/1024)
	default:
		return fmt.Sprintf("%.0fB", n)
	}
}

func cmdDialog(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek dialog accept [text] | dismiss")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	action := strings.ToLower(args[0])
	switch action {
	case "accept":
		text := ""
		if len(args) > 1 {
			text = strings.Join(args[1:], " ")
		}
		wait := page.EachEvent(func(e *proto.PageJavascriptDialogOpening) bool {
			_ = proto.PageHandleJavaScriptDialog{
				Accept:     true,
				PromptText: text,
			}.Call(page)
			return true
		})
		go wait()
		fmt.Println("dialog handler set to accept")

	case "dismiss":
		wait := page.EachEvent(func(e *proto.PageJavascriptDialogOpening) bool {
			_ = proto.PageHandleJavaScriptDialog{
				Accept: false,
			}.Call(page)
			return true
		})
		go wait()
		fmt.Println("dialog handler set to dismiss")

	default:
		return fmt.Errorf("unknown dialog action: %s (use accept or dismiss)", action)
	}

	return nil
}
