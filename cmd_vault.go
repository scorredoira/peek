package main

import (
	"github.com/scorredoira/peek/browse"

	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// A vault is a Chrome profile that keeps its logins — Apple, Google, any site with no API —
// inside an AES-256 encrypted disk image in the user's home. Locked, it is an encrypted file
// and nothing can drive it. Unlocking asks for the password on the terminal, so only the
// person at it can open it: a command run by an agent has no terminal and is refused. Closing
// the vault's Chrome locks it again.
//
// macOS only: the image is the system's own encrypted disk image, which keeps the profile encrypted on disk even while it
// is open and a crash leaves nothing in the clear (diskutil image, AES-256).

func cmdVault(ctx *cmdContext, args []string, portSet bool) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("peek vault works on macOS only")
	}
	if len(args) == 0 {
		fmt.Print(vaultUsage)
		return fmt.Errorf("no vault command")
	}

	verb := args[0]
	if verb == "-h" || verb == "--help" || verb == "help" {
		fmt.Print(vaultUsage)
		return nil
	}
	if verb != "new" {
		return fmt.Errorf("unknown vault command: %s (peek vault -h)", verb)
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: peek vault new <name>")
	}
	name := args[1]
	if !vaultNameRe.MatchString(name) {
		return fmt.Errorf("a vault's name is letters and digits only: %q", name)
	}
	return vaultNew(name)
}

// cmdUnlock is peek unlock [name] [url] [--paced]. With no name it is the main vault. A name is
// letters and digits only, so anything else (a URL has ':' or '.') is the URL.
func cmdUnlock(ctx *cmdContext, args []string, portSet bool) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("peek unlock works on macOS only")
	}
	name := defaultVault
	url := ""
	paced := false
	for i, a := range args {
		if a == "--paced" {
			paced = true
		} else if i == 0 && vaultNameRe.MatchString(a) {
			name = a
		} else if url == "" {
			url = a
		} else {
			return fmt.Errorf("usage: peek unlock [name] [url] [--paced]")
		}
	}
	return vaultUnlock(ctx, name, url, paced, portSet)
}

// cmdLock is peek lock [name]. With no name it is the main vault.
func cmdLock(args []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("peek lock works on macOS only")
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: peek lock [name]")
	}
	name := defaultVault
	if len(args) == 1 {
		name = args[0]
	}
	if !vaultNameRe.MatchString(name) {
		return fmt.Errorf("a vault's name is letters and digits only: %q", name)
	}
	return vaultLock(name)
}

// cmdStatus is peek status: every vault, locked or unlocked, and the port of an unlocked one's
// Chrome.
func cmdStatus(args []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("peek status works on macOS only")
	}
	if len(args) > 0 {
		return fmt.Errorf("usage: peek status")
	}
	return vaultList()
}

// defaultVault is the vault unlock and lock take when given no name.
const defaultVault = "main"

const vaultUsage = `peek vault — a Chrome profile that keeps its logins, in an encrypted disk image

  peek unlock [name] [url]         Ask for the password, open a visible Chrome on the
                                   profile and print its port; name defaults to main;
                                   creates the vault the first time; --paced: each press
                                   and keystroke at a person's pace until it is locked
  peek lock [name]                 Close its Chrome and lock it (closing the window does
                                   too); name defaults to main
  peek vault new <name>            Create one; asks for its password twice
  peek restart [name]              Close the unlocked vault's Chrome and start it again,
                                   without locking it; name defaults to main
  peek status                      Every vault, locked or unlocked, and its port
  peek recipes [vault]             The vault's recipes: how each task is done on a
                                   site with no API, and its first line
  peek recipe [vault/]<name>       Print one; --save writes it from standard input

Vaults live in ~/.config/peek/vaults. The password is read from the terminal only.
`

var vaultNameRe = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// vaultImageSize is the most the image can grow to. A sparse bundle takes only what it holds.
const vaultImageSize = "50G"

// vaultState is what an unlocked vault records about its Chrome, inside the vault itself.
type vaultState struct {
	Port int `json:"port"`
	Pid  int `json:"pid"`
	// Paced: every press and keystroke goes at a person's pace (unlock --paced), for the sites
	// that would rather not be driven by a machine
	Paced bool `json:"paced"`
	// Watch is the process that locks the vault when its Chrome closes (_vaultwatch); restart
	// stops it first, so closing Chrome to start it again does not lock the vault
	Watch int `json:"watch"`
}

const vaultStateFile = ".peek-vault.json"

func vaultsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory: %w", err)
	}
	return filepath.Join(home, ".config", "peek", "vaults"), nil
}

func vaultImage(name string) (string, error) {
	dir, err := vaultsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".sparsebundle"), nil
}

// vaultMount is where an unlocked vault is mounted. os.TempDir is the user's own on macOS.
func vaultMount(name string) string {
	return filepath.Join(os.TempDir(), "peek-vault-"+name)
}

func vaultNew(name string) error {
	image, err := vaultImage(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(image); err == nil {
		return fmt.Errorf("vault %s already exists: %s", name, image)
	}
	if _, err := createVault(name, image); err != nil {
		return err
	}
	fmt.Printf("vault %s created, locked: peek unlock %s\n", name, name)
	return nil
}

// createVault asks for a new password twice and creates the vault's image with it.
func createVault(name, image string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(image), 0700); err != nil {
		return "", fmt.Errorf("create the vaults directory: %w", err)
	}

	password, err := readPassword("Password for the new vault " + name + ": ")
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", fmt.Errorf("the password is empty")
	}
	again, err := readPassword("Again: ")
	if err != nil {
		return "", err
	}
	if again != password {
		return "", fmt.Errorf("the passwords do not match")
	}

	if err := createVaultImage(image, name, password, vaultImageSize); err != nil {
		return "", err
	}
	return password, nil
}

// vaultUnlock opens the vault, creating it first when it does not exist yet.
func vaultUnlock(ctx *cmdContext, name, url string, paced, portSet bool) error {
	image, err := vaultImage(name)
	if err != nil {
		return err
	}
	password := ""
	_, err = os.Stat(image)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "vault %s does not exist yet: creating it\n", name)
		password, err = createVault(name, image)
		if err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("read the vault %s: %w", image, err)
	}
	mount := vaultMount(name)

	mounted, err := isMounted(mount)
	if err != nil {
		return err
	}

	// Already open with its Chrome alive: the same answer peek open gives.
	if mounted {
		state, err := readVaultState(mount)
		if err != nil {
			return err
		}
		if state != nil && processAlive(state.Pid) && isPortOpen(state.Port) {
			// unlocking again says again whether it goes at a person's pace
			if state.Paced != paced {
				state.Paced = paced
				if err := writeVaultState(mount, *state); err != nil {
					return err
				}
			}
			fmt.Println(state.Port)
			if url == "" {
				return nil
			}
			ctx.port = state.Port
			return cmdNavigate(ctx, []string{url})
		}
	} else {
		if password == "" {
			password, err = readPassword("Password for vault " + name + ": ")
			if err != nil {
				return err
			}
		}
		if err := attachVaultImage(image, mount, password); err != nil {
			return err
		}
	}

	port := ctx.port
	if !portSet {
		port = findFreePort(ctx.port)
	}
	pid, err := startVaultChrome(name, mount, port, paced)
	if err != nil {
		// Never left open with no Chrome to close it.
		if derr := detachVaultImage(mount); derr != nil {
			return fmt.Errorf("%w; and the vault stays unlocked: %v", err, derr)
		}
		return err
	}

	if err := watchVault(name, mount, vaultState{Port: port, Pid: pid, Paced: paced}); err != nil {
		return err
	}

	fmt.Println(port)
	if url == "" {
		return nil
	}
	ctx.port = port
	return cmdNavigate(ctx, []string{url})
}

// startVaultChrome launches a visible Chrome on the vault's profile and waits for its port.
//
// Unlike peek open, it writes no preferences: the profile is the person's and keeps what they
// set in it. The keychain flags are peek open's: the profile is already encrypted by the image.
func startVaultChrome(name, mount string, port int, paced bool) (int, error) {
	forgetTab(port)
	forgetViewport(port)
	if err := paintVaultProfile(filepath.Join(mount, "chrome"), vaultColour(name)); err != nil {
		return 0, err
	}

	chromePath, err := browse.FindChromePath()
	if err != nil {
		return 0, err
	}
	chromeArgs := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", filepath.Join(mount, "chrome")),
		"--no-first-run",
		"--no-default-browser-check",
		"--password-store=basic",
		"--use-mock-keychain",
		// a covered window still draws and runs its page: macOS reports it hidden, and a
		// hidden page runs no frames, so a site like Search Console ignored what was typed
		"--disable-backgrounding-occluded-windows",
		"--window-size=1280,900",
		"about:blank",
	}
	chromeCmd := exec.Command(chromePath, chromeArgs...)
	// Its own session: closing the terminal that unlocked the vault does not close Chrome.
	chromeCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := chromeCmd.Start(); err != nil {
		return 0, fmt.Errorf("start Chrome: %w", err)
	}
	pid := chromeCmd.Process.Pid

	deadline := time.Now().Add(10 * time.Second)
	for !isPortOpen(port) {
		if time.Now().After(deadline) {
			if err := stopProcess(pid); err != nil {
				return 0, fmt.Errorf("Chrome started but port %d not ready after 10s, "+
					"and it could not be stopped: %w", port, err)
			}
			return 0, fmt.Errorf("Chrome started but port %d not ready after 10s", port)
		}
		time.Sleep(200 * time.Millisecond)
	}

	if err := writeVaultState(mount, vaultState{Port: port, Pid: pid, Paced: paced}); err != nil {
		return 0, err
	}
	return pid, nil
}

func writeVaultState(mount string, state vaultState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode the vault state: %w", err)
	}
	if err := os.WriteFile(filepath.Join(mount, vaultStateFile), data, 0600); err != nil {
		return fmt.Errorf("write the vault state: %w", err)
	}
	return nil
}

func vaultLock(name string) error {
	mount := vaultMount(name)
	mounted, err := isMounted(mount)
	if err != nil {
		return err
	}
	if !mounted {
		fmt.Printf("vault %s is locked\n", name)
		return nil
	}

	state, err := readVaultState(mount)
	if err != nil {
		return err
	}
	if state != nil {
		// Closed through DevTools, as peek close does: a SIGTERM loses the cookies Chrome
		// has not written yet, and with them the logins.
		if processAlive(state.Pid) && isPortOpen(state.Port) {
			browser, err := connectBrowser(&cmdContext{port: state.Port})
			if err != nil {
				return err
			}
			if err := browser.Close(); err != nil {
				return fmt.Errorf("close Chrome: %w", err)
			}
			waitExit(state.Pid, 10*time.Second)
		}
		if err := stopProcess(state.Pid); err != nil {
			return err
		}
		forgetTab(state.Port)
		forgetViewport(state.Port)
	}

	if err := detachVaultImage(mount); err != nil {
		return err
	}
	fmt.Printf("vault %s locked\n", name)
	return nil
}

func vaultList() error {
	dir, err := vaultsDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("no vaults (peek vault new <name>)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".sparsebundle")
		if !ok {
			continue
		}
		mount := vaultMount(name)
		mounted, err := isMounted(mount)
		if err != nil {
			return err
		}
		if !mounted {
			fmt.Printf("%s\tlocked\n", name)
			continue
		}
		state, err := readVaultState(mount)
		if err != nil {
			return err
		}
		if state != nil && processAlive(state.Pid) {
			fmt.Printf("%s\tunlocked, Chrome on port %d\n", name, state.Port)
		} else {
			fmt.Printf("%s\tunlocked, no Chrome\n", name)
		}
	}
	return nil
}

// readPassword reads a password from the controlling terminal, without echo. A process with
// no terminal — an agent's shell — gets an error: unlocking is the person's act.
func readPassword(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("the password is typed in a terminal; run this in yours: %w", err)
	}
	defer tty.Close()

	if _, err := fmt.Fprint(tty, prompt); err != nil {
		return "", fmt.Errorf("write the prompt: %w", err)
	}
	b, err := term.ReadPassword(int(tty.Fd()))
	if _, perr := fmt.Fprintln(tty); perr != nil && err == nil {
		err = perr
	}
	if err != nil {
		return "", fmt.Errorf("read the password: %w", err)
	}
	return string(b), nil
}

func createVaultImage(image, name, password, size string) error {
	cmd := exec.Command("diskutil", "image", "create", "blank", "--encrypt", "--stdinpass",
		"--size", size, "--fs", "APFS", "--volumeName", "peek-"+name, image)
	cmd.Stdin = strings.NewReader(password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create the vault image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func attachVaultImage(image, mount, password string) error {
	if err := os.MkdirAll(mount, 0700); err != nil {
		return fmt.Errorf("create the mount point: %w", err)
	}
	cmd := exec.Command("diskutil", "image", "attach", "--stdinpass", "--nobrowse",
		"--mountPoint", mount, image)
	cmd.Stdin = strings.NewReader(password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "Incorrect passphrase") {
			return fmt.Errorf("wrong password")
		}
		return fmt.Errorf("unlock the vault: %w: %s", err, msg)
	}
	return nil
}

// detachVaultImage unmounts the vault. Chrome's helpers can hold files a moment after it
// exits, so a busy volume is retried before it is an error.
func detachVaultImage(mount string) error {
	var last error
	for range 20 {
		out, err := exec.Command("diskutil", "eject", mount).CombinedOutput()
		if err == nil {
			return nil
		}
		// Somebody else locked it in between: done all the same.
		mounted, merr := isMounted(mount)
		if merr != nil {
			return merr
		}
		if !mounted {
			return nil
		}
		last = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("lock the vault (unmount %s): %w", mount, last)
}

// isMounted says whether a volume is mounted at dir: its device differs from its parent's.
func isMounted(dir string) (bool, error) {
	var st, parent syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", dir, err)
	}
	if err := syscall.Stat(filepath.Dir(dir), &parent); err != nil {
		return false, fmt.Errorf("stat %s: %w", filepath.Dir(dir), err)
	}
	return st.Dev != parent.Dev, nil
}

// readVaultState reads what the vault's last Chrome recorded; nil when none ran yet.
func readVaultState(mount string) (*vaultState, error) {
	b, err := os.ReadFile(filepath.Join(mount, vaultStateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the vault state: %w", err)
	}
	var state vaultState
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, fmt.Errorf("decode the vault state: %w", err)
	}
	return &state, nil
}

func processAlive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

// stopProcess asks Chrome to quit and kills it if it has not gone after ten seconds.
func stopProcess(pid int) error {
	if !processAlive(pid) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop Chrome (pid %d): %w", pid, err)
	}
	if waitExit(pid, 10*time.Second) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill Chrome (pid %d): %w", pid, err)
	}
	if !waitExit(pid, 5*time.Second) {
		return fmt.Errorf("Chrome (pid %d) is still running after a kill", pid)
	}
	return nil
}

// waitExit waits up to d for the process to exit and says whether it did.
func waitExit(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
	return true
}

// startVaultWatch spawns the process that locks the vault once its Chrome is gone — the last
// window closed, Chrome quit or crashed. It replaces peek open's monitor, which kills Chrome
// outright and loses the cookies it has not written. Its errors go to <vaults>/<name>.log, the only place a
// background process has.
// watchVault starts the process that locks the vault when its Chrome closes and records it in
// the vault's state. A vault whose watch did not start stays unlocked with nothing to lock it, so
// that is said, not returned: the Chrome is already open and usable.
func watchVault(name, mount string, state vaultState) error {
	watch, err := startVaultWatch(name, state.Port, state.Pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peek: vault %s will not lock when its Chrome closes: %v\n",
			name, err)
		return nil
	}
	state.Watch = watch
	return writeVaultState(mount, state)
}

// cmdRestart is peek restart [name]: the unlocked vault's Chrome closed and started again, with
// peek's current flags, without locking the vault, so without the password. With no name it is
// the main vault.
func cmdRestart(args []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("peek restart works on macOS only")
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: peek restart [name]")
	}
	name := defaultVault
	if len(args) == 1 {
		name = args[0]
	}
	if !vaultNameRe.MatchString(name) {
		return fmt.Errorf("a vault's name is letters and digits only: %q", name)
	}

	mount := vaultMount(name)
	mounted, err := isMounted(mount)
	if err != nil {
		return err
	}
	if !mounted {
		return fmt.Errorf("vault %s is locked: peek unlock %s", name, name)
	}
	state, err := readVaultState(mount)
	if err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("vault %s is unlocked but records no Chrome", name)
	}

	// The watch goes first: it would lock the vault the moment the old Chrome is gone.
	if state.Watch != 0 && processAlive(state.Watch) {
		if err := stopProcess(state.Watch); err != nil {
			return fmt.Errorf("stop the vault's watch: %w", err)
		}
	}
	// Closed through DevTools, as lock does, so Chrome writes its cookies first.
	if processAlive(state.Pid) && isPortOpen(state.Port) {
		browser, err := connectBrowser(&cmdContext{port: state.Port})
		if err != nil {
			return err
		}
		if err := browser.Close(); err != nil {
			return fmt.Errorf("close Chrome: %w", err)
		}
		waitExit(state.Pid, 10*time.Second)
	}
	if err := stopProcess(state.Pid); err != nil {
		return err
	}
	for isPortOpen(state.Port) {
		time.Sleep(200 * time.Millisecond)
	}

	pid, err := startVaultChrome(name, mount, state.Port, state.Paced)
	if err != nil {
		return err
	}
	restarted := vaultState{Port: state.Port, Pid: pid, Paced: state.Paced}
	if err := watchVault(name, mount, restarted); err != nil {
		return err
	}
	fmt.Println(state.Port)
	return nil
}

func startVaultWatch(name string, port, pid int) (int, error) {
	exePath, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("find peek's own binary: %w", err)
	}
	dir, err := vaultsDir()
	if err != nil {
		return 0, err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, name+".log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 0, fmt.Errorf("open the vault log: %w", err)
	}
	defer logFile.Close()

	w := exec.Command(exePath, "_vaultwatch", name, strconv.Itoa(port), strconv.Itoa(pid))
	w.Stdout = logFile
	w.Stderr = logFile
	// Its own session, so the terminal that unlocked the vault can close without taking it.
	w.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := w.Start(); err != nil {
		return 0, fmt.Errorf("start the vault watch: %w", err)
	}
	return w.Process.Pid, nil
}

func cmdVaultWatch(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: peek _vaultwatch <name> <port> <pid>")
	}
	name := args[0]
	port, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("invalid port: %s", args[1])
	}
	pid, err := strconv.Atoi(args[2])
	if err != nil {
		return fmt.Errorf("invalid pid: %s", args[2])
	}

	// On macOS Chrome stays running with no window: the last window closed is the signal.
	for processAlive(pid) {
		time.Sleep(2 * time.Second)
		pages, err := hasPages(port)
		if err != nil || pages {
			// No answer from the port is Chrome on its way out; the loop sees it gone.
			continue
		}
		browser, err := connectBrowser(&cmdContext{port: port})
		if err != nil {
			return err
		}
		if err := browser.Close(); err != nil {
			return fmt.Errorf("close Chrome: %w", err)
		}
		if !waitExit(pid, 10*time.Second) {
			if err := stopProcess(pid); err != nil {
				return err
			}
		}
	}

	mount := vaultMount(name)
	mounted, err := isMounted(mount)
	if err != nil {
		return err
	}
	if !mounted {
		return nil
	}
	// A later unlock may have started another Chrome on the same vault: that one keeps it.
	state, err := readVaultState(mount)
	if err != nil {
		return err
	}
	if state != nil && state.Pid != pid && processAlive(state.Pid) {
		return nil
	}
	if err := detachVaultImage(mount); err != nil {
		return fmt.Errorf("%s: vault %s: %w", time.Now().Format(time.RFC3339), name, err)
	}
	return nil
}

// hasPages says whether Chrome on this port has a window open on a page.
func hasPages(port int) (bool, error) {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/json/list", port))
	if err != nil {
		return false, fmt.Errorf("list Chrome's targets: %w", err)
	}
	defer resp.Body.Close()
	var targets []chromeTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return false, fmt.Errorf("decode Chrome's targets: %w", err)
	}
	for _, t := range targets {
		if t.Type == "page" {
			return true, nil
		}
	}
	return false, nil
}

// vaultColour is the colour of a vault's window: the main vault's is its own, the rest peek's.
func vaultColour(name string) int {
	if name == defaultVault {
		return mainVaultColour
	}
	return peekColour
}

// paintVaultProfile gives the vault's profile its colour, keeping every other preference the
// person set in it: the window says it is the one peek drives.
func paintVaultProfile(dataDir string, colour int) error {
	dir := filepath.Join(dataDir, "Default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the vault's profile: %w", err)
	}
	path := filepath.Join(dir, "Preferences")
	prefs := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read the vault's preferences: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &prefs); err != nil {
			return fmt.Errorf("read the vault's preferences: %w", err)
		}
	}
	browser, _ := prefs["browser"].(map[string]any)
	if browser == nil {
		browser = map[string]any{}
	}
	theme, _ := browser["theme"].(map[string]any)
	if theme == nil {
		theme = map[string]any{}
	}
	theme["user_color2"] = colour
	theme["color_variant2"] = 3
	browser["theme"] = theme
	prefs["browser"] = browser
	keepThemeLocal(prefs)
	out, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("write the vault's preferences: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write the vault's preferences: %w", err)
	}
	return nil
}

// keepThemeLocal turns off the themes type for every Google account signed in to the profile.
// Chrome keeps a signed-in account's theme in the account and applies it over the profile's own
// on start, so the vault's window took the person's colour instead of peek's. Only this profile
// stops reading it: the account's theme is not touched, nor any other Chrome of the person.
func keepThemeLocal(prefs map[string]any) {
	sync, _ := prefs["sync"].(map[string]any)
	if sync == nil {
		return
	}
	accounts, _ := sync["selected_types_per_account"].(map[string]any)
	for _, a := range accounts {
		types, ok := a.(map[string]any)
		if !ok {
			continue
		}
		types["sync.themes"] = false
	}
}

// vaultPaced says whether the Chrome on this port is an unlocked vault's that was asked to go at a
// person's pace (unlock --paced).
func vaultPaced(port int) bool {
	dir, err := vaultsDir()
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".sparsebundle")
		if !ok {
			continue
		}
		state, err := readVaultState(vaultMount(name))
		if err == nil && state != nil && state.Port == port && processAlive(state.Pid) {
			return state.Paced
		}
	}
	return false
}

// pace waits before a press or a keystroke when asked to (--paced, or a vault unlocked with it):
// at least half a second and never the same, as a person, for the sites that would rather not be
// driven by a machine. Otherwise peek goes as fast as it can.
func pace(ctx *cmdContext) {
	if !pacedFor(ctx) {
		return
	}
	time.Sleep(500*time.Millisecond + time.Duration(rand.Int64N(int64(700*time.Millisecond))))
}
