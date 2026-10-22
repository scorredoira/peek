package main

import (
	"github.com/scorredoira/peek/browse"

	"fmt"
	"os"
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
	nodes, err := browse.FullAXTree(page)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, node := range nodes {
		role := browse.AXValueStr(node.Role)
		name := browse.AXValueStr(node.Name)
		value := browse.AXValueStr(node.Value)

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

	// A crop is looked at up close, and JPEG artifacts are what you see there. A path that
	// ends in .png is a PNG too: writing JPEG bytes under that name made a file that no viewer
	// opens ("invalid PNG signature").
	if zoomed || strings.HasSuffix(strings.ToLower(path), ".png") {
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
	// The tab being photographed becomes the one in front, and THAT is what makes Chrome
	// draw it. Left to itself, a capture is served from the compositor's last presented
	// frame, and a tab that is not in front — a second instance, one window behind another,
	// another tab of this one — has presented none: the picture is whatever was lying around
	// when it last had the screen, or nothing at all, with no error to say so.
	//
	// It used to ask for the drawing with captureBeyondViewport, and that is the bug this
	// replaces: Chrome grants it by MOVING THE DEVICE METRICS, the page gets a resize, and
	// every popup in the client closes on resize (Popup.ts). The panel you opened the
	// browser to photograph was gone before the shutter — bell, menu, select, all of them —
	// and the file came out of a page that had already reacted, exit 0, no error. Bringing
	// the tab to the front costs the page nothing and draws just the same.
	if err := (proto.PageBringToFront{}).Call(page); err != nil {
		return fmt.Errorf("cannot bring the page to the front: %w", err)
	}
	if err := ensureDrawn(ctx, page); err != nil {
		return err
	}

	// EVERY capture names its region, the plain one included — and Chrome reads a region in
	// DOCUMENT coordinates, not in what is on screen. So --clip, which is documented from
	// the viewport, and an element's box, which is measured from it, are both carried over
	// by the scroll: without that, a capture of a page scrolled down came back showing the
	// top of the document — or white, the region not being drawn at all.
	if css != "" {
		req.Clip, err = elementClip(page, css, scale)
		if err != nil {
			return err
		}
	}
	if clip != "" {
		req.Clip, err = parseClip(page, clip, scale)
		if err != nil {
			return err
		}
	}
	if fullPage {
		req.Clip, err = contentClip(page)
		if err != nil {
			return err
		}
	}
	if req.Clip == nil {
		req.Clip, err = viewportClip(page, scale)
		if err != nil {
			return err
		}
	}

	// A region that reaches past what is on screen — the whole page, an element taller than
	// the window — was never rasterized, and a front tab does not have it to give: it comes
	// out white. Chrome draws it only by MOVING THE DEVICE METRICS, which the page feels as
	// a resize, and every popup in the client closes on resize (Popup.ts). So it is asked
	// for only when the region needs it, and said out loud when it is: a picture that cost
	// the page its open panel must never pass for one that did not.
	onScreen, err := viewportClip(page, 1)
	if err != nil {
		return err
	}
	if !within(req.Clip, onScreen) {
		req.CaptureBeyondViewport = true
		fmt.Fprintln(os.Stderr,
			"peek: this capture reaches past what is on screen, and Chrome draws that only by "+
				"resizing the page — so anything that closes on resize (a popup, a menu, a "+
				"select) is not in the picture")
	}

	data, err := page.Screenshot(false, req)
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
	el, err := browse.FindByCSS(page, css, "")
	if err != nil {
		return nil, err
	}

	// Measured and shifted in one go: scrollIntoView moves the page under the box, so the
	// scroll it ends at is the only one the rectangle can be read against.
	res, err := el.Eval(`() => {
		this.scrollIntoView({ block: "center", inline: "center" });
		const r = this.getBoundingClientRect();
		return [r.x + window.scrollX, r.y + window.scrollY, r.width, r.height];
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

// parseClip reads "x,y,w,h" in viewport CSS pixels — what the caller sees on screen — and
// answers the same region in the document coordinates Chrome captures by.
func parseClip(page *rod.Page, spec string, scale float64) (*proto.PageViewport, error) {
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

	onScreen, err := viewportClip(page, 1)
	if err != nil {
		return nil, err
	}

	return &proto.PageViewport{
		X:      onScreen.X + nums[0],
		Y:      onScreen.Y + nums[1],
		Width:  nums[2],
		Height: nums[3],
		Scale:  scale,
	}, nil
}

// within says whether a capture region is inside what the window has on screen — the only
// regions Chrome has already drawn.
func within(region, onScreen *proto.PageViewport) bool {
	// A box measured in CSS pixels lands on halves; a pixel of slack is not "beyond".
	const slack = 1

	return region.X >= onScreen.X-slack &&
		region.Y >= onScreen.Y-slack &&
		region.X+region.Width <= onScreen.X+onScreen.Width+slack &&
		region.Y+region.Height <= onScreen.Y+onScreen.Height+slack
}

// contentClip is the whole document as a region: what --full captures.
func contentClip(page *rod.Page) (*proto.PageViewport, error) {
	metrics, err := proto.PageGetLayoutMetrics{}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("cannot read the page size: %w", err)
	}
	if metrics.CSSContentSize == nil {
		return nil, fmt.Errorf("cannot read the page size")
	}

	return &proto.PageViewport{
		X:      0,
		Y:      0,
		Width:  metrics.CSSContentSize.Width,
		Height: metrics.CSSContentSize.Height,
		Scale:  1,
	}, nil
}

// viewportClip is what is on screen as a region, so --scale alone works too. Its X and Y
// are the scroll: a region is read in document coordinates, and the window sits wherever
// the page was left.
func viewportClip(page *rod.Page, scale float64) (*proto.PageViewport, error) {
	metrics, err := proto.PageGetLayoutMetrics{}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("cannot read the viewport: %w", err)
	}
	if metrics.CSSVisualViewport == nil {
		return nil, fmt.Errorf("cannot read the viewport")
	}

	return &proto.PageViewport{
		X:      metrics.CSSVisualViewport.PageX,
		Y:      metrics.CSSVisualViewport.PageY,
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

	q, err := browse.ParseElementArgs(args)
	if err != nil {
		return err
	}

	el, err := browse.FindElement(page, q)
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
			textAlign: s.textAlign,
			flex: s.display.indexOf("flex") < 0 ? "" :
				s.flexDirection + ", items " + s.alignItems + ", content " + s.justifyContent,
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
		return fmt.Errorf("cannot inspect %s: %w", browse.DescribeQuery(q), err)
	}

	style, ok := res.Value.Val().(map[string]interface{})
	if !ok {
		return fmt.Errorf("cannot read the computed style of %s", browse.DescribeQuery(q))
	}

	printStyles(style)
	return nil
}

var styleOrder = []string{
	"tag", "classes", "box", "text", "display", "visibility", "opacity",
	"position", "zIndex", "overflow", "flex", "textAlign", "font", "color", "background",
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

// ensureDrawn makes sure the page is being drawn before a capture: a page Chrome holds hidden (its
// window minimized, or covered by another one, on a Chrome started without
// --disable-backgrounding-occluded-windows) presents no frame, and a capture then waits for one
// for ever. A minimized window is put back; one still hidden after that is said, never waited on.
func ensureDrawn(ctx *cmdContext, page *rod.Page) error {
	hidden := func() (bool, error) {
		state, err := page.Eval(`() => document.visibilityState`)
		if err != nil {
			return false, fmt.Errorf("cannot read whether the page is drawn: %w", err)
		}
		return state.Value.Str() == "hidden", nil
	}
	gone, err := hidden()
	if err != nil || !gone {
		return err
	}

	browser, err := connectBrowser(ctx)
	if err != nil {
		return err
	}
	targetID, err := findPageTarget(ctx.port)
	if err != nil {
		return err
	}
	window, err := proto.BrowserGetWindowForTarget{TargetID: proto.TargetTargetID(targetID)}.Call(browser)
	if err != nil {
		return fmt.Errorf("cannot find the page's window: %w", err)
	}
	err = proto.BrowserSetWindowBounds{
		WindowID: window.WindowID,
		Bounds:   &proto.BrowserBounds{WindowState: proto.BrowserWindowStateNormal},
	}.Call(browser)
	if err != nil {
		return fmt.Errorf("cannot put the page's window back: %w", err)
	}

	gone, err = hidden()
	if err != nil {
		return err
	}
	if gone {
		return fmt.Errorf("the page is hidden (its window is covered by another), so Chrome " +
			"draws nothing to capture: uncover it, or open this Chrome again with peek, " +
			"which starts it drawing even when covered")
	}
	return nil
}
