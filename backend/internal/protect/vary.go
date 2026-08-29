package protect

import (
	"math/rand"
	"strings"
	"time"
	"unicode"
)

var greetingsByHour = []struct {
	until int
	text  string
}{
	{12, "Bom dia"},
	{18, "Boa tarde"},
	{24, "Boa noite"},
}

var warmupPhrases = []string{
	"Oi, tudo bem?",
	"Bom dia! Tudo certo por aí?",
	"Oi! Passando para confirmar que a conversa está ok.",
	"Tudo bem? Só conferindo se está recebendo minhas mensagens.",
	"Oi, boa tarde! Tudo certo?",
	"Fala! Tudo bem com você?",
	"Oi, passando para manter o contato.",
	"Tudo certo? Qualquer coisa é só responder.",
}

// Render applies light, human-looking variation so identical campaign bodies
// do not go out as a perfect clone 200 times. {{saudacao}} is replaced by
// Bom dia / Boa tarde / Boa noite according to the local hour.
func Render(body string, now time.Time) string {
	body = strings.TrimSpace(body)
	greet := greeting(now)
	body = strings.ReplaceAll(body, "{{saudacao}}", greet)
	body = strings.ReplaceAll(body, "{{Saudacao}}", greet)

	// Tiny trailing variation: 0–1 extra line break, never changes meaning.
	if rand.Intn(4) == 0 && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body
}

func greeting(now time.Time) string {
	h := now.Hour()
	for _, g := range greetingsByHour {
		if h < g.until {
			return g.text
		}
	}
	return "Olá"
}

func pickWarmupPhrase(now time.Time, last string, day int) string {
	greet := greeting(now)
	var pool []string
	switch PhaseForDay(day) {
	case PhaseFoundation:
		pool = []string{
			greet + "! Tudo bem?",
			"Oi, tudo certo por aí?",
			"Fala! Só passando para manter o contato.",
			greet + ", recebeu?",
			"Oi! Estou por aqui se precisar.",
		}
	case PhasePresence:
		pool = []string{
			greet + "! Como vai o dia?",
			"Oi, tudo bem com você?",
			"Passando para saber se está tudo certo.",
			"Fala! Qualquer coisa é só chamar.",
			greet + ", tranquilo por aí?",
		}
	default:
		pool = append([]string{}, warmupPhrases...)
		pool = append(pool,
			greet+"! Tudo bem?",
			greet+", passando para confirmar o contato.",
		)
	}
	for i := 0; i < 8; i++ {
		p := pool[rand.Intn(len(pool))]
		if p != last {
			return p
		}
	}
	return greet + "!"
}

func digitsOnly(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteByte(byte(r))
		}
	}
	return b.String()
}

func looksLikePhone(s string) bool {
	d := digitsOnly(s)
	n := 0
	for _, r := range d {
		if unicode.IsDigit(r) {
			n++
		}
	}
	return n >= 10 && n <= 15
}
