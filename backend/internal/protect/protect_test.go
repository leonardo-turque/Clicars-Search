package protect

import (
	"testing"
	"time"
)

func TestClassifyError_BanAndRate(t *testing.T) {
	if classifyError("number banned by provider") != SignalBan {
		t.Fatal("expected ban signal")
	}
	if classifyError("429 too many requests") != SignalRate {
		t.Fatal("expected rate signal")
	}
	if classifyError("connection reset") != SignalSoft {
		t.Fatal("expected soft signal")
	}
}

func TestCircuitHold_Escalates(t *testing.T) {
	if circuitHold(SignalBan, 1) != 24*time.Hour {
		t.Fatal("ban should cool for 24h")
	}
	if circuitHold(SignalSoft, 1) != 0 {
		t.Fatal("single soft error should not open a circuit")
	}
	if circuitHold(SignalSoft, 3) != 10*time.Minute {
		t.Fatal("three soft errors should pause 10m")
	}
}

func TestHours_LunchAndOvernight(t *testing.T) {
	loc := time.FixedZone("BRT", -3*60*60)
	h := Hours{StartHour: 8, EndHour: 20, LunchFrom: 12, LunchTo: 13}

	morning := time.Date(2026, 8, 12, 10, 0, 0, 0, loc) // Wednesday
	ok, _ := h.inWindow(morning)
	if !ok {
		t.Fatal("10:00 should be inside the window")
	}

	lunch := time.Date(2026, 8, 12, 12, 15, 0, 0, loc)
	ok, next := h.inWindow(lunch)
	if ok {
		t.Fatal("lunch should be paused")
	}
	if next.Hour() != 13 {
		t.Fatalf("expected resume at 13:00, got %v", next)
	}

	night := time.Date(2026, 8, 12, 21, 0, 0, 0, loc)
	ok, next = h.inWindow(night)
	if ok {
		t.Fatal("21:00 should be closed")
	}
	if next.Day() != 13 || next.Hour() != 8 {
		t.Fatalf("expected next open Thursday 08:00, got %v", next)
	}
}

func TestRender_Saudacao(t *testing.T) {
	loc := time.FixedZone("BRT", -3*60*60)
	morning := time.Date(2026, 8, 12, 9, 0, 0, 0, loc)
	got := Render("{{saudacao}}! Temos novidades.", morning)
	if got[:7] != "Bom dia" {
		t.Fatalf("expected Bom dia, got %q", got)
	}
}
