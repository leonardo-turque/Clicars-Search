package domain

import "time"

// WhatsApp session status values mirrored from the hosted Zennitex WhatsApp API.
const (
	WhatsAppConnecting   = "CONNECTING"
	WhatsAppConnected    = "CONNECTED"
	WhatsAppDisconnected = "DISCONNECTED"
)

// WhatsAppSession is the app-level view of one remote WhatsApp instance
// (managed by https://whatsapp.zennitex.com.br).
type WhatsAppSession struct {
	ID          string    `json:"id"`
	JID         string    `json:"jid,omitempty"`
	PhoneNumber string    `json:"phone_number"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}
