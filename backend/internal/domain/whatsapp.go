package domain

import "time"

// WhatsApp session status values. CONNECTING is a transient state held while a
// QR code is being scanned; it is kept in-memory by the session manager and is
// only persisted as CONNECTED once pairing succeeds (or DISCONNECTED on drop).
const (
	WhatsAppConnecting   = "CONNECTING"
	WhatsAppConnected    = "CONNECTED"
	WhatsAppDisconnected = "DISCONNECTED"
)

// WhatsAppSession is one WhatsApp number linked to the app through whatsmeow's
// multi-device pairing. The cryptographic device material itself lives in
// whatsmeow's own sqlstore tables; this record holds the app-level view (which
// number, current status) plus the JID that points back to the stored device.
type WhatsAppSession struct {
	ID          string    `json:"id"`
	JID         string    `json:"jid,omitempty"`
	PhoneNumber string    `json:"phone_number"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}
