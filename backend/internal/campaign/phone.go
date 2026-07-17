package campaign

import "strings"

// normalizeAndDedupe turns the raw, loosely-formatted phone numbers scraped into
// the companies table into a clean, de-duplicated list of international numbers
// ready for WhatsApp validation. Numbers that can't be salvaged are dropped.
func normalizeAndDedupe(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		p := normalizePhone(r)
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// normalizePhone converts a free-form phone string into E.164-ish "+<digits>"
// form. It is Brazil-centric (the app targets pt-BR leads): a bare 10/11-digit
// local number gets the +55 country code. Numbers that already carry a country
// code are left as-is. WhatsApp's own IsOnWhatsApp check is the final arbiter of
// validity, so this only needs to get the formatting close.
func normalizePhone(raw string) string {
	var b strings.Builder
	for _, ch := range raw {
		if ch >= '0' && ch <= '9' {
			b.WriteByte(byte(ch))
		}
	}
	d := b.String()
	if d == "" {
		return ""
	}

	switch {
	case strings.HasPrefix(d, "55") && (len(d) == 12 || len(d) == 13):
		// Already a Brazilian number with the 55 country code (DDD + 8/9 digits).
	case len(d) == 10 || len(d) == 11:
		// Bare local number (DDD + 8/9 digits) → prepend the BR country code.
		d = "55" + d
	}

	// E.164 allows up to 15 digits; anything shorter than 8 isn't a real number.
	if len(d) < 8 || len(d) > 15 {
		return ""
	}
	return "+" + d
}
