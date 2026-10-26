// Package poller implements the agent-side HTTP client that talks to
// gobackup-central: registration and the periodic heartbeat/command poll.
package poller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	CentralURL string
	Token      string
	HTTP       *http.Client
}

func NewClient(centralURL, token string) *Client {
	return &Client{
		CentralURL: centralURL,
		Token:      token,
		HTTP:       &http.Client{Timeout: 10 * time.Second},
	}
}

type RegisterResult struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

// Register calls POST /api/v1/agents/register and returns the issued token.
// It does not require a token itself (bootstrap step).
func Register(centralURL, name, hostname string) (RegisterResult, error) {
	body, _ := json.Marshal(map[string]string{"name": name, "hostname": hostname})
	resp, err := http.Post(centralURL+"/api/v1/agents/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return RegisterResult{}, fmt.Errorf("register request: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return RegisterResult{}, fmt.Errorf("register failed (%d): %s", resp.StatusCode, string(data))
	}

	var res RegisterResult
	if err := json.Unmarshal(data, &res); err != nil {
		return RegisterResult{}, fmt.Errorf("register decode: %w", err)
	}
	return res, nil
}

type PollResponse struct {
	Commands []any `json:"commands"`
}

// Poll sends one heartbeat with the agent's current status and returns
// whatever commands the Central queued (empty for this MVP step).
func (c *Client) Poll(status string) (PollResponse, error) {
	body, _ := json.Marshal(map[string]string{"status": status})
	req, err := http.NewRequest(http.MethodPost, c.CentralURL+"/api/v1/agents/poll", bytes.NewReader(body))
	if err != nil {
		return PollResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return PollResponse{}, fmt.Errorf("poll request: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return PollResponse{}, fmt.Errorf("poll failed (%d): %s", resp.StatusCode, string(data))
	}

	var res PollResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return PollResponse{}, fmt.Errorf("poll decode: %w", err)
	}
	return res, nil
}
