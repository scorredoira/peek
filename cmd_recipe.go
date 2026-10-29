package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// A recipe is how a task is done on a site with no API (change a mailbox's password at IONOS):
// the addresses, what to press, what the page answers when it worked. It lives inside the
// vault, next to the logins it needs, so any agent driving the vault finds it, and it reads only
// while the vault is unlocked. A password never goes in one.

// recipeNameRe is a recipe's name: lower-case letters, digits and '-' (ionos-mailbox-password).
var recipeNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const recipesDirName = "recipes"

// cmdRecipes is peek recipes [vault]: every recipe of the vault, each with its first line.
func cmdRecipes(args []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("peek recipes works on macOS only")
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: peek recipes [vault]")
	}
	vault := defaultVault
	if len(args) == 1 {
		vault = args[0]
	}
	if !vaultNameRe.MatchString(vault) {
		return fmt.Errorf("a vault's name is letters and digits only: %q", vault)
	}
	mount, err := unlockedVault(vault)
	if err != nil {
		return err
	}
	list, err := listRecipes(mount)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Printf("no recipes in %s (peek recipe <name> --save < file)\n", vault)
		return nil
	}
	for _, r := range list {
		fmt.Printf("%s\t%s\n", r.name, r.title)
	}
	return nil
}

// cmdRecipe is peek recipe [vault/]<name> [--save]: prints the recipe, or with --save writes it
// from standard input, replacing the one there was.
func cmdRecipe(args []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("peek recipe works on macOS only")
	}
	save := false
	ref := ""
	for _, a := range args {
		if a == "--save" {
			save = true
		} else if ref == "" {
			ref = a
		} else {
			return fmt.Errorf("usage: peek recipe [vault/]<name> [--save]")
		}
	}
	if ref == "" {
		return fmt.Errorf("usage: peek recipe [vault/]<name> [--save]")
	}
	vault, name, err := parseRecipeRef(ref)
	if err != nil {
		return err
	}
	mount, err := unlockedVault(vault)
	if err != nil {
		return err
	}
	if save {
		text, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read the recipe from standard input: %w", err)
		}
		if err := saveRecipe(mount, name, string(text)); err != nil {
			return err
		}
		fmt.Printf("saved %s/%s\n", vault, name)
		return nil
	}
	text, err := readRecipe(mount, name)
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}

// parseRecipeRef splits "vault/name", or "name" in the main vault.
func parseRecipeRef(ref string) (string, string, error) {
	vault := defaultVault
	name := ref
	if v, n, ok := strings.Cut(ref, "/"); ok {
		vault = v
		name = n
	}
	if !vaultNameRe.MatchString(vault) {
		return "", "", fmt.Errorf("a vault's name is letters and digits only: %q", vault)
	}
	if !recipeNameRe.MatchString(name) {
		return "", "", fmt.Errorf("a recipe's name is lower-case letters, digits and '-': %q", name)
	}
	return vault, name, nil
}

// unlockedVault is the mount of the vault, or an error saying how to unlock it.
func unlockedVault(name string) (string, error) {
	mount := vaultMount(name)
	mounted, err := isMounted(mount)
	if err != nil {
		return "", err
	}
	if !mounted {
		return "", fmt.Errorf("the vault %s is locked: the person runs peek unlock %s", name, name)
	}
	return mount, nil
}

type recipeEntry struct {
	name  string
	title string
}

// listRecipes is the recipes in the vault mounted at mount, by name, each with its first line
// that is not empty (a leading '#' left out).
func listRecipes(mount string) ([]recipeEntry, error) {
	dir := filepath.Join(mount, recipesDirName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var list []recipeEntry
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || e.IsDir() || !recipeNameRe.MatchString(name) {
			continue
		}
		title, err := recipeTitle(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		list = append(list, recipeEntry{name: name, title: title})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
	return list, nil
}

func recipeTitle(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimLeft(sc.Text(), "#"))
		if line != "" {
			return line, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return "", nil
}

func readRecipe(mount, name string) (string, error) {
	path := filepath.Join(mount, recipesDirName, name+".md")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("no recipe %s (peek recipes)", name)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(b), nil
}

func saveRecipe(mount, name, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("the recipe %s is empty: write it on standard input", name)
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	dir := filepath.Join(mount, recipesDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, name+".md")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}
