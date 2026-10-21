package main

import (
	"github.com/scorredoira/peek/browse"

	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// The page every capture test is taken of: it counts the resizes it is given, drops the
// panel when it gets one — which is what every Popup in the client does — and keeps
// something below the fold that only a full-page capture can reach.
const capturePage = `<!doctype html>
<html><head><meta charset="utf-8"><title>capture probe</title><style>
	body { margin: 0; background: #ffffff }
	#panel { position: fixed; top: 40px; left: 40px; width: 300px; height: 200px; background: #ff0000 }
	#spacer { height: 2400px }
	#below { width: 300px; height: 200px; background: #0000ff }
	#tall { width: 300px; height: 1200px; background: #ff00ff }
</style></head><body>
<div id="panel"></div>
<div id="spacer"></div>
<div id="below"></div>
<div id="tall"></div>
<script>
	window.resizes = 0
	window.addEventListener("resize", () => {
		window.resizes++
		const panel = document.getElementById("panel")
		if (panel) panel.remove()
	})
</script>
</body></html>`

// chromeForTest launches a Chrome of this test's own — its own port, its own profile — and
// leaves it serving the capture page. Not peek's `open`: that one spawns the reaper as a
// child process of the running binary, which under `go test` is the test binary itself.
func chromeForTest(t *testing.T) *cmdContext {
	t.Helper()

	if _, err := browse.FindChromePath(); err != nil {
		t.Skip("no Chrome on this machine")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, capturePage)
	}))
	t.Cleanup(server.Close)

	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	dataDir, err := os.MkdirTemp("", "peek-test-profile")
	if err != nil {
		t.Fatal(err)
	}
	chromePath, err := browse.FindChromePath()
	if err != nil {
		t.Fatal(err)
	}

	chrome := exec.Command(chromePath,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", dataDir),
		"--headless=new",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--window-size=1280,900",
		"--password-store=basic",
		"--use-mock-keychain",
		server.URL,
	)
	if err := chrome.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = chrome.Process.Kill()
		_, _ = chrome.Process.Wait()
		_ = os.RemoveAll(dataDir)
	})

	deadline := time.Now().Add(20 * time.Second)
	for !isPortOpen(port) {
		if time.Now().After(deadline) {
			t.Fatalf("Chrome did not open port %d", port)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The page has to be the probe one before a test looks at it: Chrome opens its first
	// tab blank and navigates after, and a capture of about:blank proves nothing.
	forgetTab(port)
	ctx := &cmdContext{port: port, headless: true}
	for {
		_, page, err := connect(ctx)
		if err == nil {
			res, evalErr := page.Eval(`() => !!document.getElementById("panel")`)
			if evalErr == nil && res.Value.Bool() {
				return ctx
			}
			err = evalErr
		}
		if time.Now().After(deadline) {
			t.Fatalf("the probe page never came up on port %d: %v", port, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// pixels counts how much of the capture is one colour, sampled: what is in the picture.
func pixels(t *testing.T, path string, want [3]uint8) int {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s is not an image: %v", path, err)
	}

	box := img.Bounds()
	found := 0
	for y := box.Min.Y; y < box.Max.Y; y += 3 {
		for x := box.Min.X; x < box.Max.X; x += 3 {
			r, g, b, _ := img.At(x, y).RGBA()
			if uint8(r>>8) == want[0] && uint8(g>>8) == want[1] && uint8(b>>8) == want[2] {
				found++
			}
		}
	}
	return found
}

func pageState(t *testing.T, page *rod.Page) (resizes string, panel bool) {
	t.Helper()

	// A resize is delivered asynchronously: ask only after the page has had a turn.
	if _, err := page.Eval(`() => new Promise(done => setTimeout(done, 300))`); err != nil {
		t.Fatal(err)
	}
	res, err := page.Eval(`() => String(window.resizes) + "|" + (document.getElementById("panel") ? "panel" : "gone")`)
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(res.Value.String(), "|")
	return parts[0], parts[1] == "panel"
}

// A capture must not change the page it photographs. It used to: captureBeyondViewport made
// Chrome draw by moving the device metrics, the page got a resize, and every popup closed
// before the shutter — the bell panel, a menu, a select — so the picture proved the opposite
// of what was on screen.
func TestScreenshotDoesNotResizeThePage(t *testing.T) {
	ctx := chromeForTest(t)
	path := filepath.Join(t.TempDir(), "shot.png")

	_, page, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmdScreenshot(ctx, []string{"--png", path}); err != nil {
		t.Fatal(err)
	}

	resizes, panel := pageState(t, page)
	if resizes != "0" {
		t.Fatalf("the page was resized %s time(s) by a screenshot", resizes)
	}
	if !panel {
		t.Fatal("the panel that closes on resize is gone from the page")
	}
	if n := pixels(t, path, [3]uint8{0xff, 0, 0}); n == 0 {
		t.Fatal("the panel is not in the picture")
	}
}

// The other half of the same promise: the capture is the page NOW, not the last frame Chrome
// happened to have. A tab that is not the one in front has presented none, and what used to
// come back was a blank or stale image with no error to say so.
func TestScreenshotDrawsATabThatIsNotInFront(t *testing.T) {
	ctx := chromeForTest(t)
	path := filepath.Join(t.TempDir(), "shot.png")

	browser, page, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Paint the panel a colour no frame has ever held, then send the tab to the back.
	if _, err := page.Eval(`() => { document.getElementById("panel").style.background = "#00ff00" }`); err != nil {
		t.Fatal(err)
	}
	other, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Activate(); err != nil {
		t.Fatal(err)
	}

	if err := cmdScreenshot(ctx, []string{"--png", path}); err != nil {
		t.Fatal(err)
	}

	if n := pixels(t, path, [3]uint8{0, 0xff, 0}); n == 0 {
		t.Fatal("the capture is not the page as it is now")
	}
	if resizes, _ := pageState(t, page); resizes != "0" {
		t.Fatalf("the page was resized %s time(s) by a screenshot", resizes)
	}
}

// A capture past what is on screen is the one the page cannot be spared: Chrome draws it
// only by moving the device metrics. So it says so, and the picture still holds the page.
func TestFullPageSaysItResizesThePage(t *testing.T) {
	ctx := chromeForTest(t)
	path := filepath.Join(t.TempDir(), "shot.png")

	_, page, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}

	said := captureStderr(t, func() {
		if err := cmdScreenshot(ctx, []string{"--full", "--png", path}); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(said, "resizing the page") {
		t.Fatalf("--full says nothing about resizing the page: %q", said)
	}
	if n := pixels(t, path, [3]uint8{0, 0, 0xff}); n == 0 {
		t.Fatal("what is below the fold is not in the picture")
	}
	if resizes, _ := pageState(t, page); resizes == "0" {
		t.Fatal("no resize happened, so --full should not be warning about one")
	}
}

// A capture is what is on screen. Chrome reads its region in document coordinates, so a
// page scrolled down used to be photographed at its top — or white, that region not being
// drawn — while the command said nothing and wrote the file.
func TestScreenshotOfAScrolledPageIsWhatIsOnScreen(t *testing.T) {
	ctx := chromeForTest(t)
	path := filepath.Join(t.TempDir(), "shot.png")

	page := scrollTo(t, ctx, 2000)
	if err := cmdScreenshot(ctx, []string{"--png", path}); err != nil {
		t.Fatal(err)
	}

	if n := pixels(t, path, [3]uint8{0, 0, 0xff}); n == 0 {
		t.Fatal("what the scroll left on screen is not in the picture")
	}
	if resizes, _ := pageState(t, page); resizes != "0" {
		t.Fatalf("the page was resized %s time(s) by a screenshot", resizes)
	}
}

// --clip is documented from the viewport — "that region of the viewport, in CSS pixels" —
// and has to mean that at any scroll.
func TestClipIsReadFromTheViewport(t *testing.T) {
	ctx := chromeForTest(t)
	path := filepath.Join(t.TempDir(), "shot.png")

	// At 2000 the blue block (document 2400) sits 400px down the window.
	page := scrollTo(t, ctx, 2000)
	if err := cmdScreenshot(ctx, []string{"--clip", "0,400,300,200", path}); err != nil {
		t.Fatal(err)
	}

	if n := pixels(t, path, [3]uint8{0xff, 0xff, 0xff}); n != 0 {
		t.Fatal("the clip did not land where the viewport says")
	}
	if n := pixels(t, path, [3]uint8{0, 0, 0xff}); n == 0 {
		t.Fatal("the clip did not land where the viewport says")
	}
	if resizes, _ := pageState(t, page); resizes != "0" {
		t.Fatalf("the page was resized %s time(s) by a screenshot", resizes)
	}
}

// An element that does not fit the window needs the same drawing --full does, and is owed
// the same warning — what it costs the page is the page's open panels.
func TestElementTallerThanTheWindowSaysItResizesThePage(t *testing.T) {
	ctx := chromeForTest(t)
	path := filepath.Join(t.TempDir(), "shot.png")

	said := captureStderr(t, func() {
		if err := cmdScreenshot(ctx, []string{"--css", "#tall", path}); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(said, "resizing the page") {
		t.Fatalf("a capture past the window says nothing about resizing it: %q", said)
	}
	if n := pixels(t, path, [3]uint8{0xff, 0, 0xff}); n == 0 {
		t.Fatal("the element is not in the picture")
	}
}

// An element below the fold that DOES fit the window is scrolled to and photographed with
// the page untouched: no resize, no warning.
func TestElementBelowTheFoldCostsThePageNothing(t *testing.T) {
	ctx := chromeForTest(t)
	path := filepath.Join(t.TempDir(), "shot.png")

	_, page, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	said := captureStderr(t, func() {
		if err := cmdScreenshot(ctx, []string{"--css", "#below", path}); err != nil {
			t.Fatal(err)
		}
	})

	if said != "" {
		t.Fatalf("nothing was resized, so nothing should be said: %q", said)
	}
	if n := pixels(t, path, [3]uint8{0, 0, 0xff}); n == 0 {
		t.Fatal("the element is not in the picture")
	}
	if n := pixels(t, path, [3]uint8{0xff, 0xff, 0xff}); n != 0 {
		t.Fatal("the picture is not the element's own box")
	}
	if resizes, _ := pageState(t, page); resizes != "0" {
		t.Fatalf("the page was resized %s time(s) by a screenshot", resizes)
	}
}

func scrollTo(t *testing.T, ctx *cmdContext, y int) *rod.Page {
	t.Helper()

	_, page, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.Eval(`(y) => { window.scrollTo(0, y); window.resizes = 0 }`, y); err != nil {
		t.Fatal(err)
	}

	return page
}

func captureStderr(t *testing.T, run func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	was := os.Stderr
	os.Stderr = writer

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		done <- buf.String()
	}()

	run()

	os.Stderr = was
	writer.Close()
	said := <-done
	reader.Close()

	return said
}

// A phone-width request must test that viewport, even when the OS refuses a window
// narrower than 500px. Reporting the requested size without applying it hides UI overflow.
func TestResizeUsesTheRequestedViewport(t *testing.T) {
	ctx := chromeForTest(t)
	for _, size := range [][]string{{"390", "844"}, {"1440", "900"}} {
		if err := cmdResize(ctx, size); err != nil {
			t.Fatal(err)
		}
		_, page, err := connect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result, err := page.Eval(`() => innerWidth + "x" + innerHeight`)
		if err != nil {
			t.Fatal(err)
		}
		want := size[0] + "x" + size[1]
		if got := result.Value.String(); got != want {
			t.Fatalf("requested %s, page is %s", want, got)
		}
	}
}

// A path that ends in .png gets a PNG. It used to get JPEG bytes unless --png was also given,
// and the file opened nowhere: "invalid PNG signature".
func TestScreenshotNamedPngIsAPng(t *testing.T) {
	ctx := chromeForTest(t)
	path := filepath.Join(t.TempDir(), "shot.png")

	if _, _, err := connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cmdScreenshot(ctx, []string{path}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("shot.png does not start with the PNG signature: % x", data[:8])
	}
}
