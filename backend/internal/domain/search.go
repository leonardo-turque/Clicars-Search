package domain

import "time"

// Search status values.
const (
	SearchStatusPending   = "PENDING"
	SearchStatusRunning   = "RUNNING"
	SearchStatusCompleted = "COMPLETED"
	SearchStatusFailed    = "FAILED"
)

// Search records a single execution of the company-discovery flow.
type Search struct {
	ID          string     `json:"id"`
	Niche       string     `json:"niche"`
	Location    string     `json:"location"`
	Quantity    int        `json:"quantity"`
	Status      string     `json:"status"`
	Progress    int        `json:"progress"`
	ErrorMsg    string     `json:"error_msg,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}
