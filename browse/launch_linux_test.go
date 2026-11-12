package browse

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A Chrome dies with the program that launched it: a test run killed (a timeout, a SIGKILL)
// leaves no Chrome behind holding memory.
func TestAChromeDiesWithItsLauncher(t *testing.T) {
	if os.Getenv("PEEK_LAUNCHER") == "1" {
		chrome, err := Launch(LaunchOptions{})
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		fmt.Println("CHROME", chrome.cmd.Process.Pid)
		time.Sleep(time.Minute)
		return
	}
	if noChrome != "" {
		t.Skipf("no Chrome on this machine: %s", noChrome)
	}

	launcher := exec.Command(os.Args[0], "-test.run=^TestAChromeDiesWithItsLauncher$")
	launcher.Env = append(os.Environ(), "PEEK_LAUNCHER=1")
	out, err := launcher.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := launcher.Start(); err != nil {
		t.Fatal(err)
	}
	pid := 0
	lines := bufio.NewScanner(out)
	for lines.Scan() {
		line := lines.Text()
		if strings.HasPrefix(line, "CHROME ") {
			pid, _ = strconv.Atoi(strings.TrimPrefix(line, "CHROME "))
			break
		}
		if strings.HasPrefix(line, "ERR ") {
			t.Fatal(line)
		}
	}
	if pid == 0 {
		t.Fatal("the launcher never said its Chrome")
	}

	_ = launcher.Process.Kill()
	_ = launcher.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for {
		// a process that is gone, or a zombie nobody waits for, is no Chrome holding memory
		state, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil || strings.Contains(string(state), ") Z ") {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("Chrome %d is still running 5s after its launcher was killed", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
