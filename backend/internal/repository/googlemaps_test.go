package repository

import (
	"testing"

	"github.com/zennitex/clicars-search/internal/domain"
)

// TestNormalize checks that normalize produces consistent keys for deduplication.
func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  São Paulo  ", "sãopaulo"},
		{"Padaria & Café", "padariacafé"},
		{"RESTAURANTE 123", "restaurante123"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDedup verifies that identical name+location pairs collapse and empty
// companies are filtered out.
func TestDedup(t *testing.T) {
	input := []domain.Company{
		{Name: "Padaria A", Location: "São Paulo"},
		{Name: "Padaria A", Location: "São Paulo"}, // exact duplicate
		{Name: "padaria a", Location: "são paulo"}, // case-folded duplicate
		{Name: "Padaria B", Location: "Rio"},
		{},                                         // empty — must be filtered
	}
	got := dedup(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique companies after dedup, got %d: %+v", len(got), got)
	}
}

// TestDedupPreservesOrder checks the first occurrence is kept when duplicates exist.
func TestDedupPreservesOrder(t *testing.T) {
	input := []domain.Company{
		{Name: "Alpha", Location: "SP", Phone: "111"},
		{Name: "Alpha", Location: "SP", Phone: "222"}, // duplicate — should be dropped
	}
	got := dedup(input)
	if len(got) != 1 {
		t.Fatalf("expected 1 company, got %d", len(got))
	}
	if got[0].Phone != "111" {
		t.Errorf("expected first occurrence to be kept (phone=111), got phone=%s", got[0].Phone)
	}
}

// TestCanonicalMapsURL strips query params and fragments.
func TestCanonicalMapsURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"https://www.google.com/maps/place/Foo/@-23.5,-46.6,17z/data=!4m8",
			"https://www.google.com/maps/place/Foo/@-23.5,-46.6,17z/data=!4m8",
		},
		{
			"https://www.google.com/maps/place/Bar?q=baz#anchor",
			"https://www.google.com/maps/place/Bar",
		},
		{
			"not a url %%",
			"not a url %%",
		},
	}
	for _, tc := range cases {
		if got := canonicalMapsURL(tc.in); got != tc.want {
			t.Errorf("canonicalMapsURL(%q)\n  got  %q\n  want %q", tc.in, got, tc.want)
		}
	}
}

// TestBuildGrid verifies the grid always produces exactly n cells and that the
// first cell is the bare location.
func TestBuildGrid(t *testing.T) {
	for _, n := range []int{1, 5, 20, 50} {
		cells := buildGrid("São Paulo", n)
		if len(cells) != n {
			t.Errorf("buildGrid(n=%d): got %d cells", n, len(cells))
		}
		if cells[0] != "São Paulo" {
			t.Errorf("buildGrid: first cell should be bare location, got %q", cells[0])
		}
	}
}

// TestBuildGridNoDuplicates checks that grid cells within the first 20 are unique.
func TestBuildGridNoDuplicates(t *testing.T) {
	cells := buildGrid("Curitiba", 20)
	seen := make(map[string]int, len(cells))
	for i, c := range cells {
		if prev, dup := seen[c]; dup {
			t.Errorf("buildGrid: duplicate cell %q at index %d (first seen at %d)", c, i, prev)
		}
		seen[c] = i
	}
}
