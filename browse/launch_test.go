package browse

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A launched Chrome's profile names the process that launched it, and Chrome's browser process
// is that process's child: what `scl kill` compares to tell a Chrome whose program is gone.
func TestALaunchedChromeNamesItsLauncher(t *testing.T) {
	chromePage(t)

	owner, err := os.ReadFile(filepath.Join(testChrome.dir, OwnerFile))
	if err != nil {
		t.Fatalf("the profile has no owner mark: %v", err)
	}
	self := strconv.Itoa(os.Getpid())
	if string(owner) != self {
		t.Fatalf("the owner mark reads %q, want this process, %s", owner, self)
	}

	pid := strconv.Itoa(testChrome.cmd.Process.Pid)
	out, err := exec.Command("ps", "-o", "ppid=", "-p", pid).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	if parent := strings.TrimSpace(string(out)); parent != self {
		t.Fatalf("Chrome's browser process has parent %s, want its launcher %s", parent, self)
	}
}
