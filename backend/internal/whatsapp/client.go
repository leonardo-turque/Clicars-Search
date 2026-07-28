package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIClient talks to the hosted Zennitex WhatsApp API (whatsapp.zennitex.com.br).
// BaseURL should include the /api prefix used by the nginx front (e.g.
// https://whatsapp.zennitex.com.br/api).
type APIClient struct {
	baseURL   string
	adminKey  string
	http      *http.Client
}

func NewAPIClient(baseURL, adminKey string) *APIClient {
	return &APIClient{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		adminKey: adminKey,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

type remoteInstance struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Token       string    `json:"token"`
	PhoneNumber string    `json:"phone_number"`
	DeviceJID   string    `json:"device_jid"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type remoteQR struct {
	InstanceID string `json:"instance_id"`
	QRCode     string `json:"qr_code"`
	ExpiresAt  int64  `json:"expires_at"`
}

type sendRequest struct {
	To   string `json:"to"`
	Type string `json:"type"`
	Text string `json:"text"`
}

type sendResponse struct {
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
}

type apiError struct {
	Error string `json:"error"`
}

func (c *APIClient) ListInstances(ctx context.Context) ([]remoteInstance, error) {
	var out []remoteInstance
	if err := c.do(ctx, http.MethodGet, "/instances", c.adminKey, "", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []remoteInstance{}
	}
	return out, nil
}

func (c *APIClient) GetInstance(ctx context.Context, id string) (*remoteInstance, error) {
	var out remoteInstance
	if err := c.do(ctx, http.MethodGet, "/instances/"+id, c.adminKey, "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) CreateInstance(ctx context.Context, name string) (*remoteInstance, error) {
	var out remoteInstance
	body := map[string]string{"name": name}
	if err := c.do(ctx, http.MethodPost, "/instances", c.adminKey, "", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) DeleteInstance(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/instances/"+id, c.adminKey, "", nil, nil)
}

func (c *APIClient) GetQR(ctx context.Context, id string) (*remoteQR, error) {
	var out remoteQR
	if err := c.do(ctx, http.MethodGet, "/instances/"+id+"/qr", c.adminKey, "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) SendText(ctx context.Context, token, to, text string) (*sendResponse, error) {
	var out sendResponse
	req := sendRequest{To: to, Type: "text", Text: text}
	if err := c.do(ctx, http.MethodPost, "/messages/send", "", token, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// do performs an HTTP call. Pass adminKey for X-Admin-Key auth, or bearer for
// Authorization: Bearer (instance send token). Exactly one should be set.
func (c *APIClient) do(ctx context.Context, method, path, adminKey, bearer string, body, dest any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if adminKey != "" {
		req.Header.Set("X-Admin-Key", adminKey)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode == http.StatusConflict {
		return ErrAlreadyConnected
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var ae apiError
		_ = json.Unmarshal(raw, &ae)
		msg := strings.TrimSpace(ae.Error)
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("whatsapp api %s %s: %s", method, path, msg)
	}
	if dest == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
