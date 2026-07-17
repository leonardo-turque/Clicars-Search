package domain

import "time"

// Company is a business discovered and enriched by a Search.
type Company struct {
	ID        string    `json:"id"`
	SearchID  string    `json:"search_id"`
	Name      string    `json:"name"`
	Location  string    `json:"location"`
	Phone     string    `json:"phone"`
	Website   string    `json:"website"`
	CreatedAt time.Time `json:"created_at"`
}
