package protect

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// memStore is an in-memory Store used by unit tests.
type memStore struct {
	mu       sync.Mutex
	profiles map[string]*Profile
	events   []Event
	contacts []Contact
	seq      int
}

func newMemStore() *memStore {
	return &memStore{profiles: make(map[string]*Profile)}
}

func (s *memStore) EnsureSchema(context.Context) error { return nil }

func (s *memStore) clone(p *Profile) *Profile {
	cp := *p
	if p.CircuitOpenUntil != nil {
		t := *p.CircuitOpenUntil
		cp.CircuitOpenUntil = &t
	}
	if p.LastSentAt != nil {
		t := *p.LastSentAt
		cp.LastSentAt = &t
	}
	return &cp
}

func (s *memStore) GetProfile(_ context.Context, sessionID string) (*Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[sessionID]
	if !ok {
		return nil, nil
	}
	return s.clone(p), nil
}

func (s *memStore) GetProfileByPhone(_ context.Context, phone string) (*Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	phone = digitsOnly(phone)
	var best *Profile
	for _, p := range s.profiles {
		if p.PhoneNumber == phone {
			if best == nil || p.UpdatedAt.After(best.UpdatedAt) {
				best = p
			}
		}
	}
	if best == nil {
		return nil, nil
	}
	return s.clone(best), nil
}

func (s *memStore) ListProfiles(context.Context) ([]Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, *s.clone(p))
	}
	return out, nil
}

func (s *memStore) UpsertProfile(_ context.Context, p *Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[p.SessionID] = s.clone(p)
	return nil
}

func (s *memStore) RebindSession(_ context.Context, oldID, newID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[oldID]
	if !ok {
		return nil
	}
	delete(s.profiles, oldID)
	p.SessionID = newID
	s.profiles[newID] = p
	return nil
}

func (s *memStore) InsertEvent(_ context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *memStore) CountSuccessSince(_ context.Context, sessionID string, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.SessionID == sessionID && e.Success && !e.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (s *memStore) CountKindToday(_ context.Context, sessionID, kind string, dayStart time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.SessionID == sessionID && e.Kind == kind && e.Success && !e.CreatedAt.Before(dayStart) {
			n++
		}
	}
	return n, nil
}

func (s *memStore) ListContacts(context.Context) ([]Contact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Contact(nil), s.contacts...)
	if out == nil {
		out = []Contact{}
	}
	return out, nil
}

func (s *memStore) AddContact(_ context.Context, phone, label string) (*Contact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	phone = digitsOnly(phone)
	if !looksLikePhone(phone) {
		return nil, ErrInvalidContact
	}
	for i := range s.contacts {
		if s.contacts[i].Phone == phone {
			s.contacts[i].Active = true
			if strings.TrimSpace(label) != "" {
				s.contacts[i].Label = label
			}
			cp := s.contacts[i]
			return &cp, nil
		}
	}
	s.seq++
	c := Contact{
		ID:        fmt.Sprintf("c-%d", s.seq),
		Phone:     phone,
		Label:     strings.TrimSpace(label),
		Active:    true,
		CreatedAt: time.Now(),
	}
	s.contacts = append(s.contacts, c)
	return &c, nil
}

func (s *memStore) DeleteContact(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.contacts {
		if c.ID == id {
			s.contacts = append(s.contacts[:i], s.contacts[i+1:]...)
			return nil
		}
	}
	return ErrContactNotFound
}

func (s *memStore) TouchContact(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for i := range s.contacts {
		if s.contacts[i].ID == id {
			s.contacts[i].LastSentAt = &now
			return nil
		}
	}
	return nil
}

type frozenClock struct {
	t   time.Time
	loc *time.Location
}

func (c *frozenClock) Now() time.Time           { return c.t.In(c.loc) }
func (c *frozenClock) Location() *time.Location { return c.loc }

func weekdayClock(year int, month time.Month, day, hour int) *frozenClock {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		loc = time.FixedZone("BRT", -3*60*60)
	}
	return &frozenClock{
		t:   time.Date(year, month, day, hour, 0, 0, 0, loc),
		loc: loc,
	}
}
