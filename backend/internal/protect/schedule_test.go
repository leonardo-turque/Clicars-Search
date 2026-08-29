package protect

import "testing"

func TestPhaseForDay(t *testing.T) {
	if PhaseForDay(1) != PhaseFoundation || PhaseForDay(7) != PhasePresence {
		t.Fatal("unexpected early phases")
	}
	if PhaseForDay(10) != PhaseMix || PhaseForDay(20) != PhaseRamp {
		t.Fatal("unexpected late phases")
	}
	if PhaseForDay(22) != PhaseMature {
		t.Fatal("day 22 should be mature")
	}
}

func TestDailyCapForDay_RampsToTarget(t *testing.T) {
	if got := DailyCapForDay(1, 200); got != 24 {
		t.Fatalf("day 1: got %d", got)
	}
	if got := DailyCapForDay(10, 200); got != 100 {
		t.Fatalf("day 10: got %d", got)
	}
	if got := DailyCapForDay(18, 200); got != 200 {
		t.Fatalf("day 18: got %d", got)
	}
	if got := DailyCapForDay(40, 200); got != 200 {
		t.Fatalf("mature day: got %d", got)
	}
	if got := DailyCapForDay(18, 150); got != 150 {
		t.Fatalf("cap should not exceed target, got %d", got)
	}
}

func TestApplyCalendar_WeekendReduction(t *testing.T) {
	if got := ApplyCalendar(200, 3, 0.55); got != 200 { // Wednesday
		t.Fatalf("weekday should keep cap, got %d", got)
	}
	sat := ApplyCalendar(200, 6, 0.55)
	if sat < 80 || sat > 140 {
		t.Fatalf("saturday cap out of range: %d", sat)
	}
	sun := ApplyCalendar(200, 0, 0.55)
	if sun < 4 || sun > 20 {
		t.Fatalf("sunday cap out of range: %d", sun)
	}
}

func TestHourlyCapFor_SpreadsTwoHundred(t *testing.T) {
	h := HourlyCapFor(200, 11) // 8–20 minus lunch
	if h < 18 || h > 25 {
		t.Fatalf("hourly cap for 200/day should sit near 20, got %d", h)
	}
}
