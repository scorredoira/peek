package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A directory of tests that peek cannot read all of is an error, never a smaller suite: a
// sub-directory it could not open would drop its tests and still report green.
func TestPeekTestFailsWhenItCannotReadADirectory(t *testing.T) {
	dir := t.TempDir()
	hidden := filepath.Join(dir, "hidden")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "a.peek"), []byte("open x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hidden, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(hidden, 0o755) })

	err := cmdTest(&cmdContext{workers: 1}, []string{dir})
	if err == nil || !strings.Contains(err.Error(), "walk "+dir) {
		t.Fatalf("peek test over an unreadable directory: %v, want the walk's error", err)
	}
}
