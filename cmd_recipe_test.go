package main

import (
	"strings"
	"testing"
)

// A recipe saved in a vault reads back whole, is listed by its first line, and a second save
// replaces it.
func TestRecipesAreSavedListedAndReplaced(t *testing.T) {
	mount := t.TempDir()

	list, err := listRecipes(mount)
	if err != nil || len(list) != 0 {
		t.Fatalf("a vault with no recipes: %v, %v", list, err)
	}

	if err := saveRecipe(mount, "ionos-mailbox-password", "# Change a mailbox's password\n\n1. Open the panel"); err != nil {
		t.Fatal(err)
	}
	if err := saveRecipe(mount, "apple", "\nSign in to App Store Connect\n"); err != nil {
		t.Fatal(err)
	}

	got, err := readRecipe(mount, "ionos-mailbox-password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "# Change a mailbox's password\n\n1. Open the panel\n" {
		t.Fatalf("read back %q", got)
	}

	list, err = listRecipes(mount)
	if err != nil {
		t.Fatal(err)
	}
	want := []recipeEntry{
		{name: "apple", title: "Sign in to App Store Connect"},
		{name: "ionos-mailbox-password", title: "Change a mailbox's password"},
	}
	if len(list) != len(want) || list[0] != want[0] || list[1] != want[1] {
		t.Fatalf("listed %v, want %v", list, want)
	}

	if err := saveRecipe(mount, "apple", "Sign in with the passkey\n"); err != nil {
		t.Fatal(err)
	}
	got, err = readRecipe(mount, "apple")
	if err != nil || got != "Sign in with the passkey\n" {
		t.Fatalf("after a second save: %q, %v", got, err)
	}
}

func TestRecipeErrors(t *testing.T) {
	mount := t.TempDir()

	if _, err := readRecipe(mount, "nothing"); err == nil || !strings.Contains(err.Error(), "no recipe nothing") {
		t.Fatalf("a recipe that is not there: %v", err)
	}
	if err := saveRecipe(mount, "empty", " \n"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("an empty recipe: %v", err)
	}
	if _, err := readRecipe(mount, "empty"); err == nil {
		t.Fatal("an empty recipe was saved")
	}

	for _, ref := range []string{"../x", "main/../x", "Ionos", "a b", "x/", "ma-in/x", ""} {
		if _, _, err := parseRecipeRef(ref); err == nil {
			t.Fatalf("%q was accepted", ref)
		}
	}
	vault, name, err := parseRecipeRef("ionos")
	if err != nil || vault != "main" || name != "ionos" {
		t.Fatalf("ionos: %s, %s, %v", vault, name, err)
	}
	vault, name, err = parseRecipeRef("apple/sign-in")
	if err != nil || vault != "apple" || name != "sign-in" {
		t.Fatalf("apple/sign-in: %s, %s, %v", vault, name, err)
	}
}
