package domain

import "time"

// Campaign lifecycle. PENDING is the brief window between creation and the
// worker picking up the first queued message; RUNNING while messages are going
// out; COMPLETED once every queued lead has been attempted (sent, failed, or
// skipped).
const (
	CampaignPending   = "PENDING"
	CampaignRunning   = "RUNNING"
	CampaignCompleted = "COMPLETED"
)

// Per-recipient message statuses in the durable campaign_messages queue.
const (
	MessagePending = "PENDING"
	MessageSending = "SENDING"
	MessageSent    = "SENT"
	MessageFailed  = "FAILED"
	MessageSkipped = "SKIPPED"
)

// Campaign is one bulk WhatsApp dispatch: a single message_body sent to every
// phone discovered by a Search, through one connected WhatsApp number, paced by
// the anti-ban engine. Total/Sent/Failed track live progress (Sent+Failed is the
// number of leads already attempted out of Total).
type Campaign struct {
	ID                string    `json:"id"`
	SearchID          string    `json:"search_id"`
	WhatsAppSessionID string    `json:"whatsapp_session_id"`
	MessageBody       string    `json:"message_body"`
	Status            string    `json:"status"`
	Total             int       `json:"total"`
	Sent              int       `json:"sent"`
	Failed            int       `json:"failed"`
	CreatedAt         time.Time `json:"created_at"`
}

// CampaignSummary enriches Campaign with denormalised search and session data
// for the list view, avoiding N+1 queries.
type CampaignSummary struct {
	Campaign
	SearchNiche    string `json:"search_niche"`
	SearchLocation string `json:"search_location"`
	PhoneNumber    string `json:"phone_number"`
}

// CampaignMessage is one queued recipient for a campaign. The worker claims
// PENDING rows, marks them SENDING while in flight, then terminal SENT /
// FAILED / SKIPPED. scheduled_at controls pacing across restarts.
type CampaignMessage struct {
	ID          string
	CampaignID  string
	Phone       string
	Status      string
	Attempts    int
	LastError   string
	ScheduledAt time.Time
	SentAt      *time.Time
	CreatedAt   time.Time

	// Denormalised from the parent campaign for the worker (not persisted on
	// the message row itself).
	WhatsAppSessionID string
	MessageBody       string
}
