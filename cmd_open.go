package main

import (
	"github.com/scorredoira/peek/browse"

	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func cmdOpen(ctx *cmdContext, args []string, portSet bool) error {
	url := ""
	if len(args) > 0 {
		url = args[0]
	}

	// If port was explicitly set, use it. Otherwise find a free one.
	port := ctx.port
	if !portSet {
		port = findFreePort(ctx.port)
	}

	// If already running on this port, navigate if URL given.
	if isPortOpen(port) {
		fmt.Println(port)
		if url != "" {
			ctx.port = port
			return cmdNavigate(ctx, []string{url})
		}
		return nil
	}

	// A fresh Chrome inherits no tab choice and no viewport from whatever ran on this port
	// before.
	forgetTab(port)
	forgetViewport(port)

	// Each port gets its own profile dir for full isolation.
	dataDir := profileDir(port)

	if err := writeProfilePrefs(dataDir); err != nil {
		return err
	}

	chromeArgs := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", dataDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-default-apps",
		"--disable-popup-blocking",
		// a window covered by another (a simulator) still draws: macOS reports it hidden,
		// and a hidden page presents no frame, so a capture waited for one for ever
		"--disable-backgrounding-occluded-windows",
		"--disable-translate",
		"--disable-features=PasswordManagerOnboarding,PasswordManagerBubble,TranslateUI,Translate",
		"--disable-component-update",
		"--disable-extensions",
		"--window-size=1280,900",
	}

	// A Linux box with no display cannot run a headed Chrome, and what that looks like is
	// not an error: Chrome starts, never opens its debugging port, and lingers — so peek
	// gives up with "Chrome started but port not ready" and the reader goes hunting for a
	// port problem. There is exactly one thing to do there, so peek does it and says so.
	if !ctx.headless && runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" &&
		os.Getenv("WAYLAND_DISPLAY") == "" {
		fmt.Fprintln(os.Stderr, "peek: no display — running headless")
		ctx.headless = true
	}

	if ctx.headless {
		// Drawn without the GPU, whose rasterizing of a scaled picture or a gradient moved a
		// few levels from one run to the next, and never put in the background, where a tab
		// paints no frame: two captures of the same page give the same pixels (peek shots).
		chromeArgs = append(chromeArgs, "--headless=new", "--disable-gpu",
			"--force-color-profile=srgb", "--disable-renderer-backgrounding",
			"--disable-background-timer-throttling", "--disable-backgrounding-occluded-windows")
	}

	chromePath, err := browse.FindChromePath()
	if err != nil {
		return err
	}

	// The OS keyring is never peek's business, on any platform — and asking for it is not
	// free: on a Linux box with no desktop session there is no D-Bus to answer, so Chrome
	// BLOCKS on the password store the first time a navigation needs the cookie jar. It opens
	// the TCP connection to the server and never sends the request, so the page hangs forever
	// and every later command queues behind it — which reads as peek being broken while curl
	// fetches the same URL instantly. These two flags were the macOS keychain prompt's cure
	// and are every platform's.
	chromeArgs = append(chromeArgs, "--password-store=basic", "--use-mock-keychain")

	// Launched BLANK, and navigated below once a session exists. Handing Chrome the url here
	// means it is already loading before anything can attach, so the first document — the one
	// you opened peek to look at — is the one document nothing is watching: its console, its
	// throw on the way in, its data call that 404s, all gone before the first command runs.
	//
	// about:blank rather than no url at all: with none, Chrome shows the new-tab page, which
	// findPageTarget skips as a chrome:// target and leaves nothing to connect to.
	chromeArgs = append(chromeArgs, "about:blank")

	chromeCmd := exec.Command(chromePath, chromeArgs...)
	if err := chromeCmd.Start(); err != nil {
		return fmt.Errorf("failed to start Chrome: %w", err)
	}
	ctx.pid = chromeCmd.Process.Pid

	// Wait for Chrome to be ready.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if isPortOpen(port) {
			// From here on this Chrome outlives the process that started it, so the
			// reaper is armed before anything else can go wrong. See cmdMonitor.
			// A monitor that did not start is said, not fatal: Chrome is already running and
			// its port still has to reach the caller, who can close it or `scl kill` it.
			touchUse(port)
			if err := startMonitor(port, chromeCmd.Process.Pid, ctx.headless); err != nil {
				fmt.Fprintf(os.Stderr, "peek: Chrome on port %d will not be reaped: %v\n", port, err)
			}

			// The port goes out FIRST, whatever happens next: Chrome is already running,
			// and a caller who never learns its port has no way to close it.
			fmt.Println(port)

			if url == "" {
				return nil
			}

			// connect() arms the capture — on this blank document and on the next one —
			// so the navigation below is watched from its first script.
			ctx.port = port
			_, page, err := connect(ctx)
			if err != nil {
				return fmt.Errorf("chrome started but could not be driven: %w", err)
			}

			// This tab IS the session. Naming it here is what stops every later command
			// from re-deriving one from the order Chrome happens to list its targets in —
			// an order nothing documents, and which a popup reshuffles.
			if err := rememberTab(port, string(page.TargetID)); err != nil {
				return fmt.Errorf("cannot remember the session tab: %w", err)
			}

			if err := page.Navigate(url); err != nil {
				return fmt.Errorf("navigate failed: %w", err)
			}
			if err := page.Timeout(10 * time.Second).WaitLoad(); err != nil {
				return fmt.Errorf("wait load failed: %w", err)
			}
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("Chrome started but port %d not ready after 10s", port)
}

func cmdClose(ctx *cmdContext) error {
	if !isPortOpen(ctx.port) {
		return fmt.Errorf("no Chrome running on port %d", ctx.port)
	}

	browser, err := connectBrowser(ctx)
	if err != nil {
		return err
	}

	if err := browser.Close(); err != nil {
		return fmt.Errorf("failed to close Chrome: %w", err)
	}

	forgetTab(ctx.port)
	forgetViewport(ctx.port)

	fmt.Printf("Chrome on port %d closed\n", ctx.port)
	return nil
}

// findFreePort finds the first available port starting from base.
func findFreePort(base int) int {
	for port := base; port < base+100; port++ {
		if !isPortOpen(port) {
			return port
		}
	}
	return base + 100
}

func isPortOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// peekColour is the colour of a Chrome peek drives, as Chrome's theme takes it (ARGB as a signed
// 32-bit number: #FF8800): its frame and bar are orange, so a person tells it from their own
// at a glance. The page is not touched: a screenshot is the page's alone.
const peekColour = -30720

// mainVaultColour is the main vault's (#1E8E3E): green, so the window holding the person's own
// logins is told from every other Chrome peek drives.
const mainVaultColour = -14774722

// profilePrefs turns off Chrome's password manager and its translation offers, and paints the
// window peek's colour.
const profilePrefs = `{"credentials_enable_service":false,` +
	`"browser":{"theme":{"user_color2":-30720,"color_variant2":3}},` +
	`"profile":{"password_manager_enabled":false},"translate":{"enabled":false},` +
	`"translate_blocked_languages":["en","es","fr","de","pt","it","zh","ja","ko","ru","ar"]}`

// writeProfilePrefs writes profilePrefs into the profile Chrome is about to start on.
func writeProfilePrefs(dataDir string) error {
	defaultDir := filepath.Join(dataDir, "Default")
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		return fmt.Errorf("create the Chrome profile: %w", err)
	}

	prefsPath := filepath.Join(defaultDir, "Preferences")
	if err := os.WriteFile(prefsPath, []byte(profilePrefs), 0644); err != nil {
		return fmt.Errorf("write the Chrome preferences: %w", err)
	}
	return nil
}

// profileDir is the throwaway Chrome profile a port gets. Its name is also how a leaked
// Chrome is told apart from the user's own on the process list — `scl kill` matches on it.
func profileDir(port int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("peek-chrome-%d", port))
}

// idleLimit is how long a headless Chrome may go undriven before the monitor kills it.
//
// Long enough that a session which pauses to build or to run a test suite comes back to the
// page it left — losing it means losing the login too. Short enough that a session which
// simply ended does not leave a browser running until the machine reboots.
const idleLimit = 30 * time.Minute

const lastUseFile = ".peek-lastuse"

// touchUse records that a command drove this Chrome, which is all the monitor has to go on:
// a CLI leaves nothing else behind between commands.
//
// Not fatal, deliberately: this is a hint for the reaper, and a command that did its work must
// not report failure because the hint could not be written. The cost of it failing is a Chrome
// that outstays its welcome, which is what `scl kill` is for.
func touchUse(port int) {
	path := filepath.Join(profileDir(port), lastUseFile)
	_ = os.WriteFile(path, nil, 0644)
}

// idleFor says how long nothing has driven the Chrome on this port. A missing mark means no
// answer rather than "forever": it is not evidence to kill on.
func idleFor(port int) (time.Duration, bool) {
	info, err := os.Stat(filepath.Join(profileDir(port), lastUseFile))
	if err != nil {
		return 0, false
	}
	return time.Since(info.ModTime()), true
}

// startMonitor spawns the background process that reaps this Chrome.
//
// It has to be a separate process, not a goroutine: peek is a CLI, and the command that
// launched Chrome exits seconds later. This one is what survives it — including when the
// parent is killed outright, which is how a test run that times out used to leave a browser
// behind for good.
func startMonitor(port, pid int, headless bool) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find peek's own binary: %w", err)
	}
	m := exec.Command(exePath, "_monitor", strconv.Itoa(port), strconv.Itoa(pid), boolArg(headless))
	m.Stdout = nil
	m.Stderr = nil
	if err := m.Start(); err != nil {
		return fmt.Errorf("start the monitor: %w", err)
	}
	return nil
}

func boolArg(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// cmdMonitor runs in the background and kills Chrome once nobody wants it.
//
// Two ways nobody wants it, because the two kinds of Chrome die differently:
//
//   - No pages left. A person closed the last window, which is the whole signal for a
//     headful browser.
//   - Nobody has driven it for idleLimit. A HEADLESS Chrome has no window to close and
//     nobody watching it, so that signal never comes: it used to sit there forever, one per
//     peek run, until they were killed by hand. macOS counts every one of them as an app,
//     which is how the Local Network list filled up with a wall of "Google Chrome".
func cmdMonitor(args []string) {
	if len(args) < 3 {
		return
	}
	port, _ := strconv.Atoi(args[0])
	pid, _ := strconv.Atoi(args[1])
	headless := args[2] == "1"

	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}

	// Give Chrome time to start and load the first page.
	time.Sleep(5 * time.Second)

	for {
		time.Sleep(2 * time.Second)

		if headless {
			if idle, known := idleFor(port); known && idle > idleLimit {
				proc.Kill()
				return
			}
		}

		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/json/list", port))
		if err != nil {
			// Chrome is gone: its throwaway profile goes with it.
			forgetProfile(port, proc)
			return
		}

		var targets []chromeTarget
		json.NewDecoder(resp.Body).Decode(&targets)
		resp.Body.Close()

		hasPages := false
		for _, t := range targets {
			if t.Type == "page" && !strings.HasPrefix(t.URL, "chrome://") {
				hasPages = true
				break
			}
		}

		if !hasPages {
			proc.Kill()
			forgetProfile(port, proc)
			return
		}
	}
}

// forgetProfile deletes the throwaway profile of the Chrome on this port once it is gone: each
// one weighs hundreds of megabytes, and they used to pile up in the temporary folder by the
// gigabyte. Not while any Chrome answers on the port: a peek open that came after is using it.
func forgetProfile(port int, proc *os.Process) {
	deadline := time.Now().Add(10 * time.Second)
	for proc.Signal(syscall.Signal(0)) == nil && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if proc.Signal(syscall.Signal(0)) == nil || isPortOpen(port) {
		return
	}
	if err := os.RemoveAll(profileDir(port)); err != nil {
		fmt.Fprintf(os.Stderr, "peek: the profile of port %d stays: %v\n", port, err)
	}
}
