package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The vault keeps what is written in it across a lock, opens only with its password, and is
// unmounted once locked.
func TestVaultImageLocksAndUnlocks(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("vaults are macOS only")
	}
	dir := t.TempDir()
	image := filepath.Join(dir, "test.sparsebundle")
	mount := filepath.Join(dir, "mnt")

	if err := createVaultImage(image, "test", "right", "100M"); err != nil {
		t.Fatal(err)
	}

	if err := attachVaultImage(image, mount, "wrong"); err == nil || err.Error() != "wrong password" {
		t.Fatalf("a wrong password: got %v, want wrong password", err)
	}
	mounted, err := isMounted(mount)
	if err != nil {
		t.Fatal(err)
	}
	if mounted {
		t.Fatal("mounted with a wrong password")
	}

	if err := attachVaultImage(image, mount, "right"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := detachVaultImage(mount); err != nil {
			t.Error(err)
		}
	})
	if err := os.WriteFile(filepath.Join(mount, "cookie"), []byte("session"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := detachVaultImage(mount); err != nil {
		t.Fatal(err)
	}
	mounted, err = isMounted(mount)
	if err != nil {
		t.Fatal(err)
	}
	if mounted {
		t.Fatal("still mounted after the lock")
	}
	if _, err := os.Stat(filepath.Join(mount, "cookie")); err == nil {
		t.Fatal("the file is readable while locked")
	}

	if err := attachVaultImage(image, mount, "right"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(mount, "cookie"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "session" {
		t.Fatalf("got %q after the lock, want session", b)
	}
}

// The vault's profile takes peek's colour and keeps what the person set in it.
func TestVaultProfileIsPaintedKeepingItsPreferences(t *testing.T) {
	dir := t.TempDir()
	prefs := filepath.Join(dir, "Default", "Preferences")
	if err := os.MkdirAll(filepath.Dir(prefs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefs, []byte(`{"homepage":"https://prisnap.com","browser":{"show_home_button":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := paintVaultProfile(dir, peekColour); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(prefs)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"homepage":"https://prisnap.com"`, `"show_home_button":true`, `"user_color2":-30720`} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %s in %s", want, got)
		}
	}

	fresh := t.TempDir()
	if err := paintVaultProfile(fresh, vaultColour(defaultVault)); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(fresh, "Default", "Preferences")); err != nil || !strings.Contains(string(b), `"user_color2":-14774722`) {
		t.Fatalf("a new profile: %s, %v", b, err)
	}
}
