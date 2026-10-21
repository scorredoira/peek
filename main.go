package main

import (
	"fmt"
	"os"
	"strconv"
)

const defaultPort = 9222

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	port := defaultPort
	portSet := false
	headless := false
	paced := false
	workers := 1
	args := os.Args[1:]

	// Parse global flags.
	for len(args) > 0 {
		if args[0] == "--port" {
			if len(args) < 2 {
				fatal("--port requires a value")
			}
			p, err := strconv.Atoi(args[1])
			if err != nil {
				fatal("invalid port: %s", args[1])
			}
			port = p
			portSet = true
			args = args[2:]
		} else if args[0] == "--headless" {
			headless = true
			args = args[1:]
		} else if args[0] == "--paced" {
			paced = true
			args = args[1:]
		} else if args[0] == "--workers" || args[0] == "-w" {
			if len(args) < 2 {
				fatal("--workers requires a value")
			}
			n, err := strconv.Atoi(args[1])
			if err != nil {
				fatal("invalid workers: %s", args[1])
			}
			workers = n
			args = args[2:]
		} else {
			break
		}
	}

	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	cmd := args[0]
	cmdArgs := args[1:]

	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		printUsage()
		return
	}

	ctx := &cmdContext{port: port, headless: headless, workers: workers, paced: paced}

	// a press or a keystroke goes at a person's pace when asked to (--paced, unlock --paced)
	switch cmd {
	case "click", "rightclick", "rclick", "dblclick", "fill", "select", "type", "press", "hover",
		"drag", "upload", "scroll":
		pace(ctx)
	}

	var err error
	switch cmd {
	// Chrome
	case "open":
		err = cmdOpen(ctx, cmdArgs, portSet)
	case "close":
		err = cmdClose(ctx)
	case "vault":
		err = cmdVault(ctx, cmdArgs, portSet)
	case "unlock":
		err = cmdUnlock(ctx, cmdArgs, portSet)
	case "lock":
		err = cmdLock(cmdArgs)
	case "restart":
		err = cmdRestart(cmdArgs)
	case "status":
		err = cmdStatus(cmdArgs)

	// Navigation
	case "navigate", "nav":
		err = cmdNavigate(ctx, cmdArgs)
	case "reload":
		err = cmdReload(ctx)
	case "back":
		err = cmdBack(ctx)
	case "forward":
		err = cmdForward(ctx)
	case "resize":
		err = cmdResize(ctx, cmdArgs)

	// Inspection
	case "snapshot", "snap":
		err = cmdSnapshot(ctx, cmdArgs)
	case "screenshot", "ss":
		err = cmdScreenshot(ctx, cmdArgs)
	case "url":
		err = cmdURL(ctx)
	case "html":
		err = cmdHTML(ctx)
	case "styles", "style":
		err = cmdStyles(ctx, cmdArgs)

	// Interaction
	case "click":
		err = cmdClick(ctx, cmdArgs)
	case "rightclick", "rclick":
		err = cmdRightClick(ctx, cmdArgs)
	case "dblclick":
		err = cmdDblClick(ctx, cmdArgs)
	case "fill":
		err = cmdFill(ctx, cmdArgs)
	case "select":
		err = cmdSelect(ctx, cmdArgs)
	case "type":
		err = cmdType(ctx, cmdArgs)
	case "press":
		err = cmdPress(ctx, cmdArgs)
	case "hover":
		err = cmdHover(ctx, cmdArgs)
	case "scroll":
		err = cmdScroll(ctx, cmdArgs)
	case "drag":
		err = cmdDrag(ctx, cmdArgs)
	case "upload":
		err = cmdUpload(ctx, cmdArgs)

	// Waits
	case "wait":
		err = cmdWait(ctx, cmdArgs)

	// Tabs
	case "pages":
		err = cmdPages(ctx)
	case "page":
		err = cmdPage(ctx, cmdArgs)
	case "newpage":
		err = cmdNewPage(ctx, cmdArgs)
	case "closepage":
		err = cmdClosePage(ctx)

	// JavaScript
	case "js":
		err = cmdJS(ctx, cmdArgs)

	// Debug
	case "console":
		err = cmdConsole(ctx)
	case "network", "net":
		err = cmdNetwork(ctx)

	// Dialogs
	case "dialog":
		err = cmdDialog(ctx, cmdArgs)

	// Testing
	case "test":
		err = cmdTest(ctx, cmdArgs)
	case "shots":
		err = cmdShots(ctx, cmdArgs)
	case "compare":
		err = cmdCompare(cmdArgs)

	// Utilities
	case "texts":
		err = cmdTexts(ctx, cmdArgs)
	case "freeport":
		err = cmdFreeport()

	// Internal
	case "_monitor":
		cmdMonitor(cmdArgs)
		return
	case "_vaultwatch":
		err = cmdVaultWatch(cmdArgs)

	default:
		fatal("unknown command: %s", cmd)
	}

	if err != nil {
		fatal("%v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "peek: "+format+"\n", args...)
	os.Exit(1)
}

func printUsage() {
	fmt.Print(`peek — browser remote operator

Usage: peek [--port PORT] [--headless] [--paced] [-w N] <command> [args...]

--paced: each press and keystroke waits half a second or more, never the same;
         presses and hovers move the real pointer there, typing goes key by key,
         scrolling goes in trackpad flicks

Chrome:
  open [url]            Launch Chrome, print port number
  close                 Kill Chrome instance on this port

Vaults (macOS: a profile that keeps its logins, in an encrypted disk image):
  unlock [name] [url] [--paced]
                        Ask for the password in the terminal, open a visible
                        Chrome on the profile, print its port; name defaults to
                        main; --paced: at a person's pace until it is locked
  lock [name]           Close its Chrome and lock it (closing the window does
                        too); name defaults to main
  vault new <name>      Create one; asks for its password
  restart [name]        Close the unlocked vault's Chrome and start it again,
                        without locking it; name defaults to main
  status                Every vault, locked or unlocked, and its Chrome's port

Navigation:
  navigate <url>       Go to URL
  reload               Reload current page
  back                 Go back in history
  forward              Go forward in history
  resize <w> <h>       Resize browser window

Inspection:
  snapshot [--verbose]  Print accessibility tree; [N] before a node is its --ref
  screenshot [path] [--full] [--png] [--quality N]
             [--css sel | --clip x,y,w,h] [--scale N]
                        Screenshot (default: /tmp/peek.jpg). --css or --clip
                        captures one region, --scale re-renders it bigger:
                        the way to read small text on a dense screen.
  styles [--css sel] [--id id] [text]
                        Computed box, colors and font of one element, and
                        whether it is hidden, off screen or its text is cut off
  url                   Print current URL
  html                  Print page HTML

Interaction:
  click [--css sel] [--id id] [--ref N] [--index N] [--role r] [text]   Click element;
                        --role keeps only that accessibility role (tab, link, checkbox…)
  rightclick [--css sel] [--id id] [--ref N] [--index N] [text] Right-click element (context menu)
  dblclick [--css sel] [--id id] [--ref N] [--index N] [text] Double-click element
  fill <label> <value>  Fill input field by label
  select <label> <val>  Select dropdown option
  type <text>           Type raw text into focused element
  type --stdin          Type what standard input holds, never printed (a password)
  press <key>           Press key (Enter, Tab, Escape, etc.)
  hover [--css sel] [--id id] [--ref N] [text]   Hover over element
  scroll [pixels]       Scroll down that much (negative is up), a screen by default
  drag <from> <to>      Drag element to another
  upload <sel> <file>   Upload file to input

Waits:
  wait <text>           Wait for text to appear
  wait --gone <text>    Wait for text to disappear
  wait --url <part>     Wait for the URL to contain it
  wait --url-re <re>    Wait for the URL to match a regular expression

Tabs:
  pages                 List open tabs (* marks the one in use)
  page <index>          Switch to that tab, and stay there
  newpage [url]         Open new tab
  closepage             Close current tab

JavaScript:
  js <code>             Evaluate JavaScript

Debug:
  console               Show console messages
  network               Show network requests

Dialogs (never block: answered, and a confirm gets a no):
  dialog                Show what the page asked (alert / confirm / prompt)
  dialog accept [text]  Answer yes from now on (optional input for prompts)
  dialog dismiss        Answer no from now on (the default)

Testing:
  test <path> [-w N]  Run .peek test files (recursive)
  shots <dir> [--sizes 1440x900,390x844] [--clock 2026-10-10T10:00] [--only parts,…]
                        One PNG per back-office screen (the menu, plus each
                        list's first row) of the signed-in headless browser,
                        frozen so two runs of the same code give the same
                        pixels
  compare <before> <after> <out>
                        Compare two shot sets: a diff and a side by side per
                        screen that changed, and report.md listing them

Utilities:
  texts --css <sel>     List texts of matching elements
  freeport              Print a free TCP port number

Session:
  PORT=$(peek open http://localhost:9092/admin)
  peek --port $PORT fill Email admin@demo.com
  peek --port $PORT click Login

Default port: 9222
`)
}
