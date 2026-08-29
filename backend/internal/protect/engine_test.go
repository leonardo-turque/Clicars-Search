package protect

import (
	"context"
	"testing"
	"time"
)

func testEngine(t *testing.T, clock Clock) (*Engine, *memStore) {
	t.Helper()
	store := newMemStore()
	cfg := Config{
		Timezone:          "America/Sao_Paulo",
		BusinessStartHour: 8,
		BusinessEndHour:   20,
		LunchFrom:         12,
		LunchTo:           13,
		DailyTarget:       200,
		MinDelay:          time.Millisecond,
		MaxDelay:          time.Millisecond,
		RestEvery:         1000,
		HumanPauseChance:  0,
		PrimaryPhones:     []string{PrimaryPhone},
		WeekendFactor:     0.55,
	}
	return NewEngine(store, cfg, nil, clock), store
}

func TestSeedPrimary_StartsAtDayOne(t *testing.T) {
	clock := weekdayClock(2026, time.August, 12, 10)
	eng, store := testEngine(t, clock)

	if err := eng.SeedPrimary(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := store.GetProfileByPhone(context.Background(), PrimaryPhone)
	if err != nil || p == nil {
		t.Fatalf("expected seeded profile, err=%v", err)
	}
	if p.Stage != StageWarming {
		t.Fatalf("expected WARMING, got %s", p.Stage)
	}
	if p.WarmupDay != 1 {
		t.Fatalf("expected day 1, got %d", p.WarmupDay)
	}
	if p.DailyCap != 24 {
		t.Fatalf("expected day-1 cap 24, got %d", p.DailyCap)
	}
	if p.MatureSeed {
		t.Fatal("primary must not skip the ramp")
	}
}

func TestSeedPrimary_UndoesFalseMature(t *testing.T) {
	clock := weekdayClock(2026, time.August, 12, 10)
	eng, store := testEngine(t, clock)
	ctx := context.Background()
	fake := eng.newProfile(seedPrefix+PrimaryPhone, PrimaryPhone, clock.Now())
	eng.promoteMature(fake, clock.Now())
	if err := store.UpsertProfile(ctx, fake); err != nil {
		t.Fatal(err)
	}
	if err := eng.SeedPrimary(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := store.GetProfileByPhone(ctx, PrimaryPhone)
	if p.Stage != StageWarming || p.WarmupDay != 1 {
		t.Fatalf("false mature must be reset, got stage=%s day=%d", p.Stage, p.WarmupDay)
	}
}

func TestEvaluate_BlocksCampaignsOnDay1(t *testing.T) {
	clock := weekdayClock(2026, time.August, 12, 10)
	eng, _ := testEngine(t, clock)
	ctx := context.Background()
	if err := eng.SeedPrimary(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := eng.store.GetProfileByPhone(ctx, PrimaryPhone)

	dec, err := eng.Evaluate(ctx, p.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allow {
		t.Fatalf("campaigns must wait during foundation phase, got %+v", dec)
	}

	warm, err := eng.evaluate(ctx, p.SessionID, KindWarmup)
	if err != nil {
		t.Fatal(err)
	}
	if !warm.Allow {
		t.Fatalf("warmup pings should run on day 1, got %+v", warm)
	}
}

func TestEvaluate_BlocksOutsideHours(t *testing.T) {
	clock := weekdayClock(2026, time.August, 12, 22)
	eng, store := testEngine(t, clock)
	ctx := context.Background()
	p := eng.newProfile("sess-1", "5511999990000", clock.Now())
	if err := store.UpsertProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	dec, err := eng.Evaluate(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allow {
		t.Fatal("should not allow sends at 22:00")
	}
	if dec.Wait < time.Hour {
		t.Fatalf("expected a long wait until morning, got %s", dec.Wait)
	}
}

func TestEvaluate_DailyCap(t *testing.T) {
	clock := weekdayClock(2026, time.August, 12, 10)
	eng, store := testEngine(t, clock)
	ctx := context.Background()
	p := eng.newProfile("sess-1", "5511999990000", clock.Now())
	p.SentToday = p.DailyCap
	if err := store.UpsertProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	dec, err := eng.Evaluate(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allow {
		t.Fatal("should block when daily cap is exhausted")
	}
	if dec.Reason == "" {
		t.Fatal("expected a reason")
	}
}

func TestRecord_BanOpensCircuit(t *testing.T) {
	clock := weekdayClock(2026, time.August, 12, 10)
	eng, store := testEngine(t, clock)
	ctx := context.Background()
	p := eng.newProfile("sess-1", "5511999990000", clock.Now())
	if err := store.UpsertProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	eng.Record(ctx, "sess-1", false, "number banned")
	got, _ := store.GetProfile(ctx, "sess-1")
	if got.Stage != StageCooling && got.Stage != StagePaused {
		t.Fatalf("expected cooling/paused after ban, got %s", got.Stage)
	}
	if got.CircuitOpenUntil == nil {
		t.Fatal("expected circuit to open")
	}
	dec, _ := eng.Evaluate(ctx, "sess-1")
	if dec.Allow {
		t.Fatal("circuit should block sends")
	}
}

func TestRecord_SuccessCountsTowardDaily(t *testing.T) {
	clock := weekdayClock(2026, time.August, 12, 10)
	eng, store := testEngine(t, clock)
	ctx := context.Background()
	if err := eng.SeedPrimary(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := store.GetProfileByPhone(ctx, PrimaryPhone)
	for i := 0; i < 5; i++ {
		eng.record(ctx, p.SessionID, KindWarmup, "5511999990000", true, "")
	}
	got, _ := store.GetProfile(ctx, p.SessionID)
	if got.SentToday != 5 {
		t.Fatalf("expected 5 sent today, got %d", got.SentToday)
	}
	if got.remaining() != 19 {
		t.Fatalf("expected 19 remaining on day 1, sent=%d cap=%d", got.SentToday, got.DailyCap)
	}
}

func TestEnsureSession_RebindsSeedToLiveID(t *testing.T) {
	clock := weekdayClock(2026, time.August, 12, 10)
	eng, store := testEngine(t, clock)
	ctx := context.Background()
	if err := eng.SeedPrimary(ctx); err != nil {
		t.Fatal(err)
	}
	if err := eng.ensureSession(ctx, "live-uuid", PrimaryPhone, clock.Now()); err != nil {
		t.Fatal(err)
	}
	seed, _ := store.GetProfile(ctx, seedPrefix+PrimaryPhone)
	if seed != nil {
		t.Fatal("seed session id should have been rebound")
	}
	live, _ := store.GetProfile(ctx, "live-uuid")
	if live == nil || live.Stage != StageWarming || live.DailyCap != 24 {
		t.Fatalf("expected rebound warming profile, got %+v", live)
	}
}

func TestNewNumberStartsWarming(t *testing.T) {
	clock := weekdayClock(2026, time.August, 12, 10)
	eng, _ := testEngine(t, clock)
	p := eng.newProfile("new", "5511988887777", clock.Now())
	if p.Stage != StageWarming {
		t.Fatalf("new numbers must warm up, got %s", p.Stage)
	}
	if p.DailyCap != 24 {
		t.Fatalf("day 1 cap should be 24, got %d", p.DailyCap)
	}
}

func TestCampaignBudget_FoundationIsZero(t *testing.T) {
	if CampaignBudget(1, 8) != 0 {
		t.Fatal("day 1 campaigns must be 0")
	}
	if CampaignBudget(5, 35) == 0 {
		t.Fatal("phase 2 should open a small campaign slice")
	}
	if WarmupBudget(1, 8) != 8 {
		t.Fatal("day 1 budget is all warmup")
	}
}
