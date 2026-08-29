package protect

import (
	"strings"
	"time"
)

// Signal is how a send error should affect the number's health.
type Signal int

const (
	SignalOK Signal = iota
	SignalSoft     // transient network / timeout — retry later, small health hit
	SignalRate     // provider throttling — open a short circuit
	SignalBan      // account restriction / logout — long cool-down
)

func classifyError(msg string) Signal {
	s := strings.ToLower(strings.TrimSpace(msg))
	if s == "" {
		return SignalOK
	}
	switch {
	case containsAny(s,
		"banned", "banido", "blocked", "bloqueado", "forbidden", "403",
		"logged out", "desconect", "not authorized", "unauthorized", "401",
		"conflict", "number banned", "account locked", "restricted"):
		return SignalBan
	case containsAny(s,
		"429", "rate", "too many", "throttl", "slow down", "flood",
		"spam", "temporarily unavailable"):
		return SignalRate
	default:
		return SignalSoft
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func circuitHold(sig Signal, consecutive int) time.Duration {
	switch sig {
	case SignalBan:
		return 24 * time.Hour
	case SignalRate:
		if consecutive >= 5 {
			return 4 * time.Hour
		}
		if consecutive >= 3 {
			return time.Hour
		}
		return 20 * time.Minute
	default:
		if consecutive >= 8 {
			return 2 * time.Hour
		}
		if consecutive >= 5 {
			return 30 * time.Minute
		}
		if consecutive >= 3 {
			return 10 * time.Minute
		}
		return 0
	}
}
