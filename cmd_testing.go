package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

// Background processes started by the "start" command, cleaned up after all tests.
var (
	startedMu    sync.Mutex
	startedProcs []*os.Process
	startedPorts = map[int]bool{}
)

type peekTest struct {
	name  string
	file  string
	lines []peekLine
}

type peekLine struct {
	num  int
	text string
}

type testResult struct {
	test       *peekTest
	passed     bool
	err        error
	line       int
	dur        time.Duration
	pageErrors []string
}

var testPortNext int32 = 10222

func cmdTest(ctx *cmdContext, args []string) error {
	workers := ctx.workers
	headless := ctx.headless
	var paths []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workers", "-w":
			if i+1 >= len(args) {
				return fmt.Errorf("--workers requires a value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("invalid workers: %s", args[i])
			}
			workers = n
		case "--headless":
			headless = true
		default:
			paths = append(paths, args[i])
		}
	}

	if len(paths) == 0 {
		return fmt.Errorf("usage: peek test <file.peek|dir> [-w N] [--headless]")
	}

	// Collect test files.
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("cannot access %s: %w", p, err)
		}
		if info.IsDir() {
			filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.IsDir() && strings.HasSuffix(path, ".peek") {
					files = append(files, path)
				}
				return nil
			})
		} else {
			files = append(files, p)
		}
	}

	if len(files) == 0 {
		return fmt.Errorf("no .peek test files found")
	}

	// Parse all test files.
	tests := make([]*peekTest, len(files))
	for i, f := range files {
		t, err := parseTestFile(f)
		if err != nil {
			return fmt.Errorf("parse %s: %w", f, err)
		}
		tests[i] = t
	}

	// Run tests with worker pool, printing results progressively.
	totalStart := time.Now()
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var passed, failed int32
	var printMu sync.Mutex

	// Suppress command output during tests.
	origStdout := os.Stdout
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("failed to open devnull: %w", err)
	}
	os.Stdout = devNull

	for _, t := range tests {
		wg.Add(1)
		go func(t *peekTest) {
			defer wg.Done()
			sem <- struct{}{}
			r := runTest(headless, t)
			<-sem

			printMu.Lock()
			if r.passed {
				atomic.AddInt32(&passed, 1)
				name := r.test.file
				if r.test.name != "" {
					name += " — " + r.test.name
				}
				fmt.Fprintf(origStdout, "PASS  %s (%s)\n", name, formatDur(r.dur))
			} else {
				atomic.AddInt32(&failed, 1)
				loc := r.test.file
				if r.line > 0 {
					loc = fmt.Sprintf("%s:%d", r.test.file, r.line)
				}
				fmt.Fprintf(origStdout, "FAIL  %s — %v\n", loc, r.err)
				for _, e := range r.pageErrors {
					fmt.Fprintf(origStdout, "        page: %s\n", e)
				}
			}
			printMu.Unlock()
		}(t)
	}

	wg.Wait()
	devNull.Close()
	os.Stdout = origStdout

	// Kill background processes started by "start" commands.
	startedMu.Lock()
	for _, p := range startedProcs {
		p.Kill()
		p.Wait()
	}
	startedProcs = nil
	startedMu.Unlock()

	p := atomic.LoadInt32(&passed)
	f := atomic.LoadInt32(&failed)
	fmt.Printf("\n%d tests, %d passed, %d failed (%s)\n", p+f, p, f, formatDur(time.Since(totalStart)))

	if f > 0 {
		os.Exit(1)
	}
	return nil
}

func parseTestFile(path string) (*peekTest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	t := &peekTest{
		file: path,
	}

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if t.name == "" {
				t.name = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			}
			continue
		}
		t.lines = append(t.lines, peekLine{num: lineNum, text: line})
	}

	return t, scanner.Err()
}

func runTest(headless bool, t *peekTest) testResult {
	start := time.Now()

	// Allocate a unique port for this test.
	port := allocTestPort()
	ctx := &cmdContext{port: port, headless: headless}

	// Variable store for exec captures.
	vars := map[string]string{}

	chromeStarted := false
	defer func() {
		if chromeStarted && ctx.pid != 0 {
			if p, err := os.FindProcess(ctx.pid); err == nil {
				p.Kill()
				p.Wait()
			}
		}
	}()

	for _, line := range t.lines {
		// Expand ${VAR} references before parsing.
		expanded := expandVars(line.text, vars)
		cmd, args := parseLine(expanded)

		var err error
		switch cmd {
		case "open":
			if !chromeStarted {
				err = cmdOpen(ctx, args, true)
				if err == nil {
					chromeStarted = true
					if len(args) > 0 {
						_, page, cerr := connect(ctx)
						if cerr == nil {
							page.Timeout(10 * time.Second).WaitLoad()
						}
					}
				}
			} else if len(args) > 0 {
				err = cmdNavigate(ctx, args)
			}

		case "navigate", "nav":
			err = cmdNavigate(ctx, args)
		case "reload":
			err = cmdReload(ctx)
		case "back":
			err = cmdBack(ctx)
		case "forward":
			err = cmdForward(ctx)
		case "resize":
			err = cmdResize(ctx, args)

		case "click":
			err = cmdClick(ctx, args)
		case "rightclick", "rclick":
			err = cmdRightClick(ctx, args)
		case "dblclick":
			err = cmdDblClick(ctx, args)
		case "fill":
			err = cmdFill(ctx, args)
		case "select":
			err = cmdSelect(ctx, args)
		case "type":
			err = cmdType(ctx, args)
		case "press":
			err = cmdPress(ctx, args)
		case "hover":
			err = cmdHover(ctx, args)
		case "drag":
			err = cmdDrag(ctx, args)
		case "upload":
			err = cmdUpload(ctx, args)

		case "wait":
			err = cmdWait(ctx, args)

		case "screenshot", "ss":
			err = cmdScreenshot(ctx, args)
		case "snapshot", "snap":
			err = cmdSnapshot(ctx, args)

		case "js":
			err = cmdJS(ctx, args)

		case "dialog":
			err = cmdDialog(ctx, args)

		case "assert":
			err = execAssert(ctx, args)

		case "exec":
			err = execShell(expanded, vars)

		case "freeport":
			err = execFreeport(args, vars)

		case "start":
			err = execStart(expanded)

		case "pause":
			if len(args) == 0 {
				err = fmt.Errorf("usage: pause <duration> (e.g. 5s, 500ms)")
			} else {
				d, perr := time.ParseDuration(args[0])
				if perr != nil {
					err = fmt.Errorf("invalid duration: %s", args[0])
				} else {
					time.Sleep(d)
				}
			}

		default:
			err = fmt.Errorf("unknown command: %s", cmd)
		}

		if err != nil {
			thrown, _ := pageErrors(ctx, chromeStarted)
			return testResult{
				test:       t,
				passed:     false,
				err:        err,
				line:       line.num,
				dur:        time.Since(start),
				pageErrors: thrown,
			}
		}
	}

	// A test that ran every step without a Go error has proved that its selectors still
	// resolve, and nothing else. The page may have thrown on the way in, lost a bundle to
	// a 404 and answered every data call with a 500 — peek captured all of it, and not
	// reading it back was the difference between a suite that proves the UI works and one
	// that proves it is still clickable.
	thrown, err := pageErrors(ctx, chromeStarted)
	if err != nil {
		return testResult{
			test:   t,
			passed: false,
			err:    err,
			dur:    time.Since(start),
		}
	}

	if len(thrown) > 0 {
		return testResult{
			test:       t,
			passed:     false,
			err:        fmt.Errorf("the page reported %d error(s)", len(thrown)),
			dur:        time.Since(start),
			pageErrors: thrown,
		}
	}

	return testResult{
		test:   t,
		passed: true,
		dur:    time.Since(start),
	}
}

// pageErrors reads back what the page said about itself while the test ran.
//
// Failing to READ them is not the same as there being none: answering "clean" because the
// browser could not be asked is the exact false green this check exists to remove, so the
// failure travels and the test fails on it.
func pageErrors(ctx *cmdContext, chromeStarted bool) ([]string, error) {
	if !chromeStarted {
		return nil, nil
	}

	_, page, err := connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot ask the page what went wrong: %w", err)
	}

	res, err := page.Eval(`() => {
		if (!window.__peekErrors) { return [] }
		return window.__peekErrors.map(m => m.text)
	}`)
	if err != nil {
		return nil, fmt.Errorf("cannot read the page's errors: %w", err)
	}

	raw, ok := res.Value.Val().([]interface{})
	if !ok {
		return nil, fmt.Errorf("cannot read the page's errors")
	}

	var errors []string
	for _, e := range raw {
		errors = append(errors, fmt.Sprint(e))
	}

	return errors, nil
}

// execAssert runs an assertion with automatic retry until timeout.
func execAssert(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: assert <url|text|gone|title|js|clean> <value>")
	}

	timeout := defaultTimeout

	// Parse --timeout flag.
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

	if args[0] == "clean" {
		return assertClean(ctx)
	}

	if len(args) < 2 {
		return fmt.Errorf("usage: assert <url|text|gone|title|js|clean> <value>")
	}

	kind := args[0]
	value := strings.Join(args[1:], " ")

	switch kind {
	case "url":
		return assertURL(ctx, value, timeout)
	case "text":
		return assertText(ctx, value, timeout)
	case "gone":
		return assertGone(ctx, value, timeout)
	case "title":
		return assertTitle(ctx, value, timeout)
	case "js":
		return assertJS(ctx, value, timeout)
	default:
		return fmt.Errorf("unknown assert: %s", kind)
	}
}

// assertClean fails when the page has reported anything wrong up to this point.
//
// A whole test is checked for this at the end anyway; this is for saying WHERE. Put it
// after the step you suspect and the failure names that line instead of the last one.
func assertClean(ctx *cmdContext) error {
	thrown, err := pageErrors(ctx, true)
	if err != nil {
		return err
	}
	if len(thrown) == 0 {
		return nil
	}

	return fmt.Errorf("assert clean: the page reported %d error(s): %s",
		len(thrown), strings.Join(thrown, " | "))
}

func assertURL(ctx *cmdContext, pattern string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, page, err := connect(ctx)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		info, err := page.Info()
		if err == nil && strings.Contains(info.URL, pattern) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("assert url: URL does not contain %q", pattern)
}

func assertText(ctx *cmdContext, text string, timeout time.Duration) error {
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
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("assert text: %q not found", text)
}

func assertGone(ctx *cmdContext, text string, timeout time.Duration) error {
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
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("assert gone: %q still present", text)
}

func assertTitle(ctx *cmdContext, text string, timeout time.Duration) error {
	lower := strings.ToLower(text)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, page, err := connect(ctx)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		info, err := page.Info()
		if err == nil && strings.Contains(strings.ToLower(info.Title), lower) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("assert title: title does not contain %q", text)
}

func assertJS(ctx *cmdContext, expr string, timeout time.Duration) error {
	code := fmt.Sprintf("() => { return !!(%s) }", expr)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, page, err := connect(ctx)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		result, err := page.Eval(code)
		if err == nil && result != nil && result.Value.Bool() {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("assert js: %q is not truthy", expr)
}

// parseLine splits a test file line into command and arguments,
// respecting double-quoted strings.
func parseLine(line string) (string, []string) {
	var tokens []string
	var current strings.Builder
	inQuotes := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '"':
			inQuotes = !inQuotes
		case ch == ' ' && !inQuotes:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	if len(tokens) == 0 {
		return "", nil
	}
	return tokens[0], tokens[1:]
}

func allocTestPort() int {
	for {
		port := int(atomic.AddInt32(&testPortNext, 1)) - 1
		if !isPortOpen(port) {
			return port
		}
	}
}

func formatDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// execShell runs a shell command and captures stdout into vars["result"].
// Receives the full raw line (e.g. "exec --as TOKEN mysql -e \"SELECT ...\"").
// Optional --as flag: exec --as TOKEN mysql ...  → vars["TOKEN"] = stdout
func execShell(rawLine string, vars map[string]string) error {
	// Strip the "exec" prefix.
	shell := strings.TrimSpace(strings.TrimPrefix(rawLine, "exec"))
	if shell == "" {
		return fmt.Errorf("usage: exec <command> [args...]")
	}

	varName := "result"

	// Parse optional --as flag from the beginning.
	if strings.HasPrefix(shell, "--as ") {
		rest := strings.TrimPrefix(shell, "--as ")
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) < 2 {
			return fmt.Errorf("exec --as: missing command")
		}
		varName = parts[0]
		shell = parts[1]
	}

	cmd := exec.Command("sh", "-c", shell)
	out, err := cmd.CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		if output != "" {
			return fmt.Errorf("exec failed: %w\n%s", err, output)
		}
		return fmt.Errorf("exec failed: %w", err)
	}

	vars[varName] = strings.TrimSpace(string(out))
	return nil
}

// execStart ensures a server is running on the given port.
// Syntax: start :PORT command...
// If the port is already responding to HTTP, it's a no-op.
// Otherwise, starts the command in background and waits for readiness.
func execStart(rawLine string) error {
	rest := strings.TrimSpace(strings.TrimPrefix(rawLine, "start"))
	if rest == "" || !strings.HasPrefix(rest, ":") {
		return fmt.Errorf("usage: start :PORT command...")
	}

	spaceIdx := strings.Index(rest, " ")
	if spaceIdx < 0 {
		return fmt.Errorf("usage: start :PORT command...")
	}

	port, err := strconv.Atoi(rest[1:spaceIdx])
	if err != nil {
		return fmt.Errorf("start: invalid port: %s", rest[1:spaceIdx])
	}

	shell := strings.TrimSpace(rest[spaceIdx+1:])
	if shell == "" {
		return fmt.Errorf("usage: start :PORT command...")
	}

	// Tests share one server on purpose: several files run in parallel and the first one
	// to get here starts it. What must NOT be reused is a server this run did not start —
	// left over from a previous run, and very likely the previous BUILD. Reusing that one
	// silently is how a whole suite passes green against code that was never run.
	startedMu.Lock()

	if startedPorts[port] {
		startedMu.Unlock()
		return waitForPort(port)
	}

	if isPortOpen(port) {
		startedMu.Unlock()
		return fmt.Errorf("start: something is already listening on port %d and this run did "+
			"not start it — kill it first, or the tests run against whatever that is", port)
	}

	cmd := exec.Command("sh", "-c", shell)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		startedMu.Unlock()
		return fmt.Errorf("start: failed to run %q: %w", shell, err)
	}

	startedProcs = append(startedProcs, cmd.Process)
	startedPorts[port] = true
	startedMu.Unlock()

	if err := waitForPort(port); err != nil {
		cmd.Process.Kill()
		return err
	}

	return nil
}

// waitForPort waits until the port answers HTTP.
func waitForPort(port int) error {
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://localhost:%d/", port))
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}

	return fmt.Errorf("start: port %d not ready after 5s", port)
}

// expandVars replaces ${VAR} references in a line.
// Priority: exec-captured vars first, then environment variables.
func expandVars(line string, vars map[string]string) string {
	for name, value := range vars {
		line = strings.ReplaceAll(line, "${"+name+"}", value)
	}
	return os.ExpandEnv(line)
}
