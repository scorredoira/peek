package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The preferences that turn the password manager off are written, or peek says it could
// not: a Chrome started without them offers to save every password a test types.
func TestWriteProfilePrefsFailsWhenItCannotWrite(t *testing.T) {
	parent := t.TempDir()
	dataDir := filepath.Join(parent, "profile")

	if err := writeProfilePrefs(dataDir); err != nil {
		t.Fatalf("write into a writable dir: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(dataDir, "Default", "Preferences"))
	if err != nil || string(written) != profilePrefs {
		t.Fatalf("Preferences reads %q, %v", written, err)
	}

	locked := filepath.Join(parent, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	if err := writeProfilePrefs(filepath.Join(locked, "profile")); err == nil {
		t.Fatal("writing into a dir nobody may write answered no error")
	}
}
