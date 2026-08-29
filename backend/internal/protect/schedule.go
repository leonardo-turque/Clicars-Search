package protect

import "math"

// WarmupDays is the length of the automatic ramp before a number is treated as mature.
const WarmupDays = 21

// DefaultTargetDaily is the operational ceiling for a single WhatsApp number.
const DefaultTargetDaily = 200

// PrimaryPhone is the Clicars number that must complete every warmup phase.
const PrimaryPhone = "554184376916"

const (
	PhaseFoundation = "FOUNDATION" // days 1–3: only trusted chats
	PhasePresence   = "PRESENCE"   // days 4–7: light presence + tiny campaign slice
	PhaseMix        = "MIX"        // days 8–14: warmup + campaigns share the day
	PhaseRamp       = "RAMP"       // days 15–21: mostly campaigns, still paced
	PhaseMature     = "MATURE"     // day 22+
)

// PhaseForDay returns the warmup phase for a 1-based day.
func PhaseForDay(day int) string {
	switch {
	case day <= 3:
		return PhaseFoundation
	case day <= 7:
		return PhasePresence
	case day <= 14:
		return PhaseMix
	case day <= 21:
		return PhaseRamp
	default:
		return PhaseMature
	}
}

// PhaseLabel is the Portuguese name shown in the dashboard.
func PhaseLabel(phase string) string {
	switch phase {
	case PhaseFoundation:
		return "Fase 1 · Identidade (dias 1–3)"
	case PhasePresence:
		return "Fase 2 · Presença (dias 4–7)"
	case PhaseMix:
		return "Fase 3 · Mistura (dias 8–14)"
	case PhaseRamp:
		return "Fase 4 · Subida (dias 15–21)"
	default:
		return "Fase 5 · Maduro"
	}
}

// CampaignBudget is how many campaign (not warmup) sends are allowed today.
// Days 1–3 are warmup-only: campaigns wait until phase 2.
func CampaignBudget(day, dailyCap int) int {
	if dailyCap < 1 {
		return 0
	}
	switch PhaseForDay(day) {
	case PhaseFoundation:
		return 0
	case PhasePresence:
		n := dailyCap / 5
		if n < 1 {
			n = 1
		}
		return n
	case PhaseMix:
		return dailyCap / 2
	case PhaseRamp:
		return (dailyCap * 3) / 4
	default:
		return dailyCap
	}
}

// WarmupBudget is how many trusted-contact pings the worker may send today.
func WarmupBudget(day, dailyCap int) int {
	if dailyCap < 1 {
		return 0
	}
	switch PhaseForDay(day) {
	case PhaseFoundation:
		return dailyCap
	case PhasePresence:
		n := dailyCap - CampaignBudget(day, dailyCap)
		if n < 4 {
			n = 4
		}
		return n
	case PhaseMix:
		n := dailyCap / 3
		if n < 4 {
			n = 4
		}
		return n
	case PhaseRamp:
		n := dailyCap / 8
		if n < 2 {
			n = 2
		}
		return n
	default:
		return 2
	}
}

// warmupCurve is a 21-day send ramp designed to look like organic growth
// rather than a sudden blast. Index 0 = day 1.
// Foundation days run at an accelerated 24/day (owner request, 2026-08-26):
// a sustained level reads organic; a one-day spike then drop does not.
var warmupCurve = []int{
	24, 24, 24, 25, 35, 45, 55, 70, 85, 100,
	115, 130, 145, 160, 175, 185, 195, 200, 200, 200, 200,
}

// DailyCapForDay returns the send ceiling for a given 1-based warmup day.
// Days past WarmupDays stay at target. The result is never above target.
func DailyCapForDay(day, target int) int {
	if target <= 0 {
		target = DefaultTargetDaily
	}
	if day < 1 {
		day = 1
	}
	if day > len(warmupCurve) {
		return target
	}
	cap := warmupCurve[day-1]
	if cap > target {
		return target
	}
	return cap
}

// ApplyCalendar scales a daily cap for weekends. Saturday is reduced; Sunday
// is a light keep-alive only. Mature numbers still send on Saturday at a
// gentler pace so B2B traffic does not look like a 7-day bot.
func ApplyCalendar(cap int, weekday int, weekendFactor float64) int {
	if cap < 1 {
		return 0
	}
	if weekendFactor <= 0 {
		weekendFactor = 0.55
	}
	switch weekday {
	case 6: // Saturday (time.Saturday == 6)
		n := int(math.Round(float64(cap) * weekendFactor))
		if n < 8 {
			n = 8
		}
		return n
	case 0: // Sunday
		n := int(math.Round(float64(cap) * weekendFactor * 0.35))
		if n < 4 {
			n = 4
		}
		if n > 20 {
			n = 20
		}
		return n
	default:
		return cap
	}
}

// HourlyCapFor spreads the daily ceiling across businessHours, with a small
// burst margin so a slightly faster hour does not stall the whole day.
func HourlyCapFor(dailyCap, businessHours int) int {
	if dailyCap < 1 {
		return 1
	}
	if businessHours < 1 {
		businessHours = 12
	}
	per := int(math.Ceil(float64(dailyCap) / float64(businessHours)))
	// Allow a modest burst (≈25%) so jitter/rests can catch up later.
	burst := per + (per / 4)
	if burst < 3 {
		burst = 3
	}
	if burst > 25 {
		burst = 25
	}
	return burst
}
