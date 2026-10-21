package browse

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

// A Chrome of a program's own: started headless on a throwaway profile, driven over the port
// Chrome picks itself, and closed with its profile when the program is done with it. It is the
// Chrome a test drives; the peek command's own outlives the command that opened it and is kept
// apart for that reason.

// OwnerFile is the mark Launch leaves in a Chrome's profile: the pid of the process that
// launched it. `scl kill` reads it to tell a Chrome whose program is gone (its browser process
// then has another parent) from one a running test still drives.
const OwnerFile = ".scl-owner"

// LaunchOptions are what a launched Chrome is told at start.
type LaunchOptions struct {
	// Server sends every connection to a host under localhost to this address
	// ("127.0.0.1:52341"): a page names "http://demo.localhost/admin" as the server it
	// talks to writes it, with no port, and still reaches the one server that is listening.
	Server string
	// Headed opens Chrome's window, for a person to watch; without it Chrome draws to nothing.
	Headed bool
	// Width and Height are the window's size; 1280×900 when zero.
	Width  int
	Height int
}

// Chrome is a launched Chrome and the browser driving it.
type Chrome struct {
	Browser *rod.Browser
	cmd     *exec.Cmd
	dir     string
}

// Launch starts Chrome — headless unless opts.Headed — and connects to it. The profile dir
// carries peek's prefix and the OwnerFile mark, so `scl kill` sweeps one that ever leaks.
func Launch(opts LaunchOptions) (*Chrome, error) {
	path, err := FindChromePath()
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "peek-chrome-test-")
	if err != nil {
		return nil, fmt.Errorf("cannot make a profile dir: %w", err)
	}
	owner := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(filepath.Join(dir, OwnerFile), []byte(owner), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("cannot mark the profile dir: %w", err)
	}

	width := opts.Width
	if width == 0 {
		width = 1280
	}
	height := opts.Height
	if height == 0 {
		height = 900
	}

	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + dir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-component-update",
		"--disable-popup-blocking",
		// a window covered by another (a simulator) still draws: macOS reports it hidden,
		// and a hidden page presents no frame, so a capture waited for one for ever
		"--disable-backgrounding-occluded-windows",
		"--disable-features=PasswordManagerOnboarding,PasswordManagerBubble,TranslateUI,Translate",
		"--password-store=basic",
		"--use-mock-keychain",
		fmt.Sprintf("--window-size=%d,%d", width, height),
	}
	if !opts.Headed {
		args = append(args, "--headless=new", "--disable-gpu")
	}
	if opts.Server != "" {
		rules := fmt.Sprintf("MAP localhost %s, MAP *.localhost %s", opts.Server, opts.Server)
		args = append(args, "--host-resolver-rules="+rules)
	}
	args = append(args, "about:blank")

	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("cannot start Chrome: %w", err)
	}

	c := &Chrome{cmd: cmd, dir: dir}

	control, err := devtoolsURL(dir)
	if err != nil {
		c.Close()
		return nil, err
	}

	browser := rod.New().ControlURL(control)
	if err := browser.Connect(); err != nil {
		c.Close()
		return nil, fmt.Errorf("cannot drive Chrome: %w", err)
	}
	c.Browser = browser

	return c, nil
}

// devtoolsURL waits for the file Chrome writes once its debugging port is open — the port it
// picked and its browser target — and answers the address to drive it at.
func devtoolsURL(dir string) (string, error) {
	file := filepath.Join(dir, "DevToolsActivePort")
	deadline := time.Now().Add(20 * time.Second)
	for {
		data, err := os.ReadFile(file)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) == 2 {
				return "ws://127.0.0.1:" + lines[0] + lines[1], nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("Chrome started but never opened its debugging port")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Close closes Chrome through the browser — its own children go with it — and removes the
// profile once nothing writes to it.
func (c *Chrome) Close() {
	if c.Browser != nil {
		_ = c.Browser.Close()
	}

	done := make(chan struct{})
	go func() {
		_, _ = c.cmd.Process.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}

	_ = os.RemoveAll(c.dir)
}
