package protect

import "time"

const (
	StageWarming = "WARMING"
	StageMature  = "MATURE"
	StagePaused  = "PAUSED"
	StageCooling = "COOLING"

	KindCampaign = "CAMPAIGN"
	KindWarmup   = "WARMUP"
)

// Profile is the durable health + warmup state of one WhatsApp number.
type Profile struct {
	SessionID         string     `json:"session_id"`
	PhoneNumber       string     `json:"phone_number"`
	Stage             string     `json:"stage"`
	WarmupStartedAt   time.Time  `json:"warmup_started_at"`
	WarmupDay         int        `json:"warmup_day"`
	DailyCap          int        `json:"daily_cap"`
	TargetDaily       int        `json:"target_daily"`
	HealthScore       int        `json:"health_score"`
	ConsecutiveErrors int        `json:"consecutive_errors"`
	CircuitOpenUntil  *time.Time `json:"circuit_open_until,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	LastSentAt        *time.Time `json:"last_sent_at,omitempty"`
	SentToday         int        `json:"sent_today"`
	SentTodayDate     time.Time  `json:"sent_today_date"`
	BurstCount        int        `json:"burst_count"`
	WarmupSentToday   int        `json:"warmup_sent_today"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`

	// Computed for the API / UI, not necessarily persisted.
	HourlySent     int        `json:"hourly_sent"`
	RemainingToday int        `json:"remaining_today"`
	NextWindowAt   *time.Time `json:"next_window_at,omitempty"`
	StatusReason   string     `json:"status_reason"`
	Connected      bool       `json:"connected"`
	MatureSeed     bool       `json:"mature_seed"`
	Phase          string     `json:"phase"`
	PhaseLabel     string     `json:"phase_label"`
	CampaignBudget int        `json:"campaign_budget_today"`
	WarmupBudget   int        `json:"warmup_budget_today"`
	CampaignSent   int        `json:"campaign_sent_today"`
}

// Snapshot is the public view returned by GET /protect/numbers.
type Snapshot struct {
	Profile
	HourlyCap int `json:"hourly_cap"`
}

// Contact is a trusted number used by the automatic warmup worker.
type Contact struct {
	ID         string     `json:"id"`
	Phone      string     `json:"phone"`
	Label      string     `json:"label,omitempty"`
	Active     bool       `json:"active"`
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Event is one attempted send, used to compute hourly windows after restarts.
type Event struct {
	ID        string
	SessionID string
	Kind      string
	Phone     string
	Success   bool
	Error     string
	CreatedAt time.Time
}

// Decision is the gate result for a single send attempt.
type Decision struct {
	Allow  bool
	Wait   time.Duration
	Reason string
}

func (p *Profile) remaining() int {
	n := p.DailyCap - p.SentToday
	if n < 0 {
		return 0
	}
	return n
}
