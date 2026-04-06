package inputdev

import (
	"testing"
)

func TestKeycodeToRune_Unshifted(t *testing.T) {
	r := KeycodeToRune(30, false) // KEY_A
	if r != 'a' {
		t.Fatalf("expected 'a', got %q", r)
	}
}

func TestKeycodeToRune_Shifted(t *testing.T) {
	r := KeycodeToRune(30, true) // KEY_A shifted
	if r != 'A' {
		t.Fatalf("expected 'A', got %q", r)
	}
}

func TestKeycodeToRune_Special(t *testing.T) {
	r := KeycodeToRune(KeyEnter, false)
	if r != 0 {
		t.Fatalf("Enter should return rune 0, got %q", r)
	}
}

func TestClampMouse(t *testing.T) {
	cases := []struct{ v, max, want int }{
		{-5, 1280, 0},
		{0, 1280, 0},
		{640, 1280, 640},
		{1279, 1280, 1279},
		{1280, 1280, 1279},
		{9999, 1280, 1279},
	}
	for _, c := range cases {
		got := clampMouse(c.v, c.max)
		if got != c.want {
			t.Errorf("clampMouse(%d, %d) = %d, want %d", c.v, c.max, got, c.want)
		}
	}
}
