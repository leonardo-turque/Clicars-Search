package protect

import "time"

// Clock abstracts time so the gate can be tested without sleeping for hours.
type Clock interface {
	Now() time.Time
	Location() *time.Location
}

type realClock struct {
	loc *time.Location
}

func newRealClock(name string) *realClock {
	if name == "" {
		name = "America/Sao_Paulo"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc = time.FixedZone("BRT", -3*60*60)
	}
	return &realClock{loc: loc}
}

func (c *realClock) Now() time.Time           { return time.Now().In(c.loc) }
func (c *realClock) Location() *time.Location { return c.loc }

// Hours describes the sending window in the configured timezone.
type Hours struct {
	StartHour int // inclusive, 0-23
	EndHour   int // exclusive, 0-24
	LunchFrom int // inclusive; 0 disables lunch pause
	LunchTo   int // exclusive
}

func (h Hours) normalize() Hours {
	if h.StartHour < 0 || h.StartHour > 23 {
		h.StartHour = 8
	}
	if h.EndHour <= h.StartHour || h.EndHour > 24 {
		h.EndHour = 20
	}
	if h.LunchFrom < 0 || h.LunchTo <= h.LunchFrom || h.LunchTo > 24 {
		h.LunchFrom, h.LunchTo = 0, 0
	}
	return h
}

func (h Hours) businessSpan() int {
	span := h.EndHour - h.StartHour
	if h.LunchTo > h.LunchFrom {
		span -= h.LunchTo - h.LunchFrom
	}
	if span < 1 {
		return 1
	}
	return span
}

// inWindow reports whether t (already in the clock location) is inside the
// sending window, and if not, when the next window opens.
func (h Hours) inWindow(t time.Time) (ok bool, next time.Time) {
	h = h.normalize()
	loc := t.Location()
	hour := t.Hour()

	openToday := time.Date(t.Year(), t.Month(), t.Day(), h.StartHour, 0, 0, 0, loc)
	closeToday := time.Date(t.Year(), t.Month(), t.Day(), h.EndHour, 0, 0, 0, loc)

	if hour < h.StartHour {
		return false, openToday
	}
	if !t.Before(closeToday) {
		return false, nextOpen(t.Add(24*time.Hour), h)
	}
	if h.LunchFrom > 0 && hour >= h.LunchFrom && hour < h.LunchTo {
		return false, time.Date(t.Year(), t.Month(), t.Day(), h.LunchTo, 0, 0, 0, loc)
	}
	return true, time.Time{}
}

func nextOpen(t time.Time, h Hours) time.Time {
	h = h.normalize()
	loc := t.Location()
	day := time.Date(t.Year(), t.Month(), t.Day(), h.StartHour, 0, 0, 0, loc)
	if t.After(day) || t.Equal(day) {
		// already past today's open; caller should have advanced the date.
	}
	return day
}

func startOfDay(t time.Time) time.Time {
	loc := t.Location()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

func daysSince(from, to time.Time) int {
	a := startOfDay(from)
	b := startOfDay(to)
	d := int(b.Sub(a).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}
