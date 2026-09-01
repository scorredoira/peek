package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func cmdSnapshot(ctx *cmdContext, args []string) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	verbose := len(args) > 0 && args[0] == "--verbose"

	tree, err := getAccessibilityTree(page, verbose)
	if err != nil {
		return err
	}

	fmt.Print(tree)
	return nil
}

func getAccessibilityTree(page *rod.Page, verbose bool) (string, error) {
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		return "", fmt.Errorf("accessibility tree failed: %w", err)
	}

	var sb strings.Builder
	for _, node := range result.Nodes {
		role := axValueStr(node.Role)
		name := axValueStr(node.Name)
		value := axValueStr(node.Value)

		if node.Ignored {
			continue
		}

		if !verbose {
			if !isUsefulRole(role) {
				continue
			}
		}

		line := fmt.Sprintf("[%s] %s", node.NodeID, role)
		if name != "" {
			line += fmt.Sprintf(" %q", name)
		}
		if value != "" {
			line += fmt.Sprintf(" value=%q", value)
		}

		if verbose && node.Properties != nil {
			for _, prop := range node.Properties {
				if prop.Value != nil {
					line += fmt.Sprintf(" %s=%s", prop.Name, prop.Value.Value.Str())
				}
			}
		}

		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	return sb.String(), nil
}

func axValueStr(v *proto.AccessibilityAXValue) string {
	if v == nil {
		return ""
	}
	return v.Value.Str()
}

var usefulRoles = map[string]bool{
	"button":      true,
	"link":        true,
	"textbox":     true,
	"searchbox":   true,
	"combobox":    true,
	"checkbox":    true,
	"radio":       true,
	"switch":      true,
	"tab":         true,
	"menuitem":    true,
	"option":      true,
	"slider":      true,
	"spinbutton":  true,
	"heading":     true,
	"RootWebArea": true,
}

func isUsefulRole(role string) bool {
	return usefulRoles[role]
}

func cmdScreenshot(ctx *cmdContext, args []string) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	path := ""
	fullPage := false
	quality := 80
	format := proto.PageCaptureScreenshotFormatJpeg
	css := ""
	clip := ""
	scale := 1.0
	zoomed := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--full":
			fullPage = true
		case "--quality":
			i++
			if i >= len(args) {
				return fmt.Errorf("--quality needs a number")
			}
			quality, err = strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("--quality: %q is not a number", args[i])
			}
		case "--png":
			format = proto.PageCaptureScreenshotFormatPng
		case "--css":
			i++
			if i >= len(args) {
				return fmt.Errorf("--css needs a selector")
			}
			css = args[i]
			zoomed = true
		case "--clip":
			i++
			if i >= len(args) {
				return fmt.Errorf("--clip needs x,y,w,h")
			}
			clip = args[i]
			zoomed = true
		case "--scale":
			i++
			if i >= len(args) {
				return fmt.Errorf("--scale needs a factor")
			}
			scale, err = strconv.ParseFloat(args[i], 64)
			if err != nil || scale <= 0 {
				return fmt.Errorf("invalid scale: %s", args[i])
			}
			zoomed = true
		default:
			// An unknown flag used to become the output filename: `peek ss --full-page`
			// wrote a file called "--full-page", printed it and exited 0, and the caller
			// believed it held a full-page capture.
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag: %s", args[i])
			}
			path = args[i]
		}
	}

	// A crop is looked at up close, and JPEG artifacts are what you see there.
	if zoomed {
		format = proto.PageCaptureScreenshotFormatPng
	}
	if path == "" {
		path = "/tmp/peek.jpg"
		if format == proto.PageCaptureScreenshotFormatPng {
			path = "/tmp/peek.png"
		}
	}

	req := &proto.PageCaptureScreenshot{
		Format:  format,
		Quality: &quality,
	}

	if css != "" && clip != "" {
		return fmt.Errorf("--css and --clip are exclusive")
	}
	if fullPage && zoomed {
		return fmt.Errorf("--full takes the whole page; --css, --clip and --scale zoom into part of it")
	}
	if css != "" {
		req.Clip, err = elementClip(page, css, scale)
		if err != nil {
			return err
		}
	}
	if clip != "" {
		req.Clip, err = parseClip(clip, scale)
		if err != nil {
			return err
		}
	}
	if req.Clip == nil && scale != 1 {
		req.Clip, err = viewportClip(page, scale)
		if err != nil {
			return err
		}
	}
	data, err := page.Screenshot(fullPage, req)
	if err != nil {
		return fmt.Errorf("screenshot failed: %w", err)
	}

	if err := writeFile(path, data); err != nil {
		return err
	}

	fmt.Println(path)
	return nil
}

// elementClip scrolls the element into view and returns its box as a capture region.
// Chrome re-renders the region at the given scale, so small text comes out sharp
// instead of upscaled from the viewport raster.
func elementClip(page *rod.Page, css string, scale float64) (*proto.PageViewport, error) {
	el, err := findByCSS(page, css, "")
	if err != nil {
		return nil, err
	}

	res, err := el.Eval(`() => {
		this.scrollIntoView({ block: "center", inline: "center" });
		const r = this.getBoundingClientRect();
		return [r.x, r.y, r.width, r.height];
	}`)
	if err != nil {
		return nil, fmt.Errorf("cannot measure %s: %w", css, err)
	}

	box := res.Value.Arr()
	if len(box) != 4 || box[2].Num() <= 0 || box[3].Num() <= 0 {
		return nil, fmt.Errorf("%s has no visible box", css)
	}

	return &proto.PageViewport{
		X:      box[0].Num(),
		Y:      box[1].Num(),
		Width:  box[2].Num(),
		Height: box[3].Num(),
		Scale:  scale,
	}, nil
}

// parseClip reads "x,y,w,h" in viewport CSS pixels.
func parseClip(spec string, scale float64) (*proto.PageViewport, error) {
	parts := strings.Split(spec, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("--clip wants x,y,w,h, got %q", spec)
	}

	nums := make([]float64, 4)
	for i, part := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, fmt.Errorf("--clip: %q is not a number", part)
		}
		nums[i] = n
	}
	if nums[2] <= 0 || nums[3] <= 0 {
		return nil, fmt.Errorf("--clip needs a positive width and height")
	}

	return &proto.PageViewport{
		X:      nums[0],
		Y:      nums[1],
		Width:  nums[2],
		Height: nums[3],
		Scale:  scale,
	}, nil
}

// viewportClip is the whole viewport as a region, so --scale alone works too.
func viewportClip(page *rod.Page, scale float64) (*proto.PageViewport, error) {
	metrics, err := proto.PageGetLayoutMetrics{}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("cannot read the viewport: %w", err)
	}
	if metrics.CSSVisualViewport == nil {
		return nil, fmt.Errorf("cannot read the viewport")
	}

	return &proto.PageViewport{
		X:      0,
		Y:      0,
		Width:  metrics.CSSVisualViewport.ClientWidth,
		Height: metrics.CSSVisualViewport.ClientHeight,
		Scale:  scale,
	}, nil
}

func cmdURL(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	info, err := page.Info()
	if err != nil {
		return fmt.Errorf("failed to get page info: %w", err)
	}

	fmt.Println(info.URL)
	return nil
}

func cmdHTML(ctx *cmdContext) error {
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	html, err := page.HTML()
	if err != nil {
		return fmt.Errorf("failed to get HTML: %w", err)
	}

	fmt.Println(html)
	return nil
}

// cmdStyles reports how an element is actually painted: its box, its resolved colors,
// whether it is visible at all, and whether its text is being cut off.
//
// All of it is reachable through `peek js`, which is exactly the problem — nothing in the
// tool said it was worth looking at, so it never got looked at. Truncated text, a control
// clipped by its container and an element parked off screen all survive a screenshot and
// every affirmative line peek prints.
func cmdStyles(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek styles [--css <sel>] [--id <id>] [text]")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	q, err := parseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := findElement(page, q)
	if err != nil {
		return err
	}

	res, err := el.Eval(`() => {
		const s = getComputedStyle(this)
		const r = this.getBoundingClientRect()
		// Overflowing only cuts the content off when the box actually clips it. With
		// overflow:visible the text spills out and stays perfectly readable, so calling
		// that "truncated" sends the reader chasing a bug that is not there.
		const cuts = axis => axis !== "visible"
		const clippedX = cuts(s.overflowX) && this.scrollWidth > this.clientWidth + 1
		const clippedY = cuts(s.overflowY) && this.scrollHeight > this.clientHeight + 1
		const offScreen = r.bottom < 0 || r.right < 0 ||
			r.top > innerHeight || r.left > innerWidth
		return {
			tag: this.tagName.toLowerCase(),
			classes: this.className || "",
			box: Math.round(r.width) + "x" + Math.round(r.height) +
				" at " + Math.round(r.x) + "," + Math.round(r.y),
			display: s.display,
			visibility: s.visibility,
			opacity: s.opacity,
			color: s.color,
			background: s.backgroundColor,
			font: s.fontSize + " " + s.fontWeight + " " + s.fontFamily.split(",")[0],
			align: s.textAlign,
			overflow: s.overflow,
			zIndex: s.zIndex,
			position: s.position,
			hidden: s.display === "none" || s.visibility === "hidden" || s.opacity === "0",
			empty: r.width === 0 || r.height === 0,
			offScreen: offScreen,
			clipped: clippedX || clippedY,
			text: (this.innerText || "").slice(0, 120),
		}
	}`)
	if err != nil {
		return fmt.Errorf("cannot inspect %s: %w", describeQuery(q), err)
	}

	style, ok := res.Value.Val().(map[string]interface{})
	if !ok {
		return fmt.Errorf("cannot read the computed style of %s", describeQuery(q))
	}

	printStyles(style)
	return nil
}

var styleOrder = []string{
	"tag", "classes", "box", "text", "display", "visibility", "opacity",
	"position", "zIndex", "overflow", "align", "font", "color", "background",
}

// styleWarning is a flag the page can raise about an element and what it means.
type styleWarning struct {
	key     string
	message string
}

var styleWarnings = []styleWarning{
	{key: "hidden", message: "hidden:    it is not painted at all"},
	{key: "empty", message: "empty:     its box is zero — nothing to see"},
	{key: "offScreen", message: "offScreen: it sits outside the viewport"},
	{key: "clipped", message: "clipped:   its content overflows the box and is being cut off"},
}

func printStyles(style map[string]interface{}) {
	for _, key := range styleOrder {
		value := fmt.Sprint(style[key])
		if value == "" {
			continue
		}
		fmt.Printf("%-11s %s\n", key, value)
	}

	for _, warning := range styleWarnings {
		if flag, ok := style[warning.key].(bool); ok && flag {
			fmt.Println(warning.message)
		}
	}
}
