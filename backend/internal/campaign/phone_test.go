package campaign

import (
	"reflect"
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"11999998888":         "+5511999998888", // 11-digit mobile, no country code
		"(11) 99999-8888":     "+5511999998888", // formatted, no country code
		"+55 (11) 99999-8888": "+5511999998888", // formatted, with country code
		"5511999998888":       "+5511999998888", // 13-digit, with country code
		"1130031234":          "+551130031234",  // 10-digit landline, no country code
		"":                    "",               // empty
		"   ":                 "",               // whitespace only
		"abc-def":             "",               // no digits
		"123":                 "",               // too short to be a number
	}
	for in, want := range cases {
		if got := normalizePhone(in); got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeAndDedupe(t *testing.T) {
	in := []string{"11999998888", "(11) 99999-8888", "5511999998888", "", "abc", "1130031234"}
	got := normalizeAndDedupe(in)
	want := []string{"+5511999998888", "+551130031234"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeAndDedupe(%v) = %v, want %v", in, got, want)
	}
}
