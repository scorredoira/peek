package main

import (
	"testing"

	"github.com/go-rod/rod/lib/input"
)

func TestResolveComboReadsChordsAndThePlusKey(t *testing.T) {
	cases := []struct {
		combo string
		want  []input.Key
	}{
		{"Enter", []input.Key{input.Enter}},
		{"Control+k", []input.Key{input.ControlLeft, input.Key('k')}},
		{"Shift+Meta+Enter", []input.Key{input.ShiftLeft, input.MetaLeft, input.Enter}},
		{"+", []input.Key{input.Key('+')}},
		{"Shift++", []input.Key{input.ShiftLeft, input.Key('+')}},
	}

	for _, c := range cases {
		got, err := resolveCombo(c.combo)
		if err != nil {
			t.Fatalf("%q: %v", c.combo, err)
		}

		if len(got) != len(c.want) {
			t.Fatalf("%q: got %v, want %v", c.combo, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q: got %v, want %v", c.combo, got, c.want)
			}
		}
	}
}

func TestResolveComboRefusesWhatIsNotAChord(t *testing.T) {
	for _, combo := range []string{"Hyper+k", "Control+"} {
		if _, err := resolveCombo(combo); err == nil {
			t.Fatalf("%q: want an error, got none", combo)
		}
	}
}
