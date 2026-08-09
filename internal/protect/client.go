// Package protect talks to the local UniFi Protect Integration API
// (https://<console>/proxy/protect/integration/v1/...), authenticated with
// the same X-API-KEY used for the UniFi Network API.
//
// Note: as of this writing the Integration API exposes camera/NVR metadata
// and a real-time event *subscription* over WebSocket, but no REST endpoint
// for historical event search (confirmed: GET .../v1/events -> 404). That
// means we can only learn real smart-detect event types for clips recorded
// after this service started listening — see internal/correlate.
package protect

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	host       string
	apiKey     string
	httpClient *http.Client
}

func NewClient(host, apiKey string, insecureSkipVerify bool) *Client {
	return &Client{
		host:   host,
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureSkipVerify},
			},
		},
	}
}

type Camera struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// Cameras lists all cameras known to the Protect controller.
func (c *Client) Cameras() ([]Camera, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL()+"/cameras", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-KEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching cameras: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching cameras: unexpected status %s", resp.Status)
	}

	var cams []Camera
	if err := json.NewDecoder(resp.Body).Decode(&cams); err != nil {
		return nil, fmt.Errorf("decoding cameras response: %w", err)
	}
	return cams, nil
}

func (c *Client) baseURL() string {
	return fmt.Sprintf("https://%s/proxy/protect/integration/v1", c.host)
}

// WebSocketURL is the confirmed real-time event subscription endpoint.
func (c *Client) WebSocketURL() string {
	return fmt.Sprintf("wss://%s/proxy/protect/integration/v1/subscribe/events", c.host)
}
