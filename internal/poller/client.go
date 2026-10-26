// Package poller implements the agent-side HTTP client that talks to
// gobackup-central: registration and the periodic heartbeat/command poll.
package poller

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type Client struct {
	CentralURL string
	Token      string
	HTTP       *http.Client
}

// NewClient builds an HTTPS-aware client. If caCertPEM is non-empty, the
// Central's certificate must chain to that CA (pinned trust — no public CA
// needed for an internal self-signed deployment); empty caCertPEM falls
// back to the system trust store (use when CentralURL has a real CA-signed
// certificate, e.g. Let's Encrypt).
func NewClient(centralURL, token string, caCertPEM []byte) (*Client, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}

	if len(caCertPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCertPEM) {
			return nil, fmt.Errorf("invalid CA certificate PEM")
		}
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		}
	}

	return &Client{CentralURL: centralURL, Token: token, HTTP: httpClient}, nil
}

// LoadCACertFile is a small helper for CLI flags that still take a file
// path (-central-ca) rather than the raw PEM bytes used when configuration
// comes from the database (the web UI "Settings" screen).
func LoadCACertFile(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	return os.ReadFile(path)
}

type RegisterResult struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

// Register calls POST /api/v1/agents/register and returns the issued token.
// It does not require a token itself (bootstrap step), but does reuse the
// same pinned-CA HTTP client so the very first contact with the Central is
// already verified, not just the later polls.
func Register(centralURL, name, hostname string, caCertPEM []byte) (RegisterResult, error) {
	client, err := NewClient(centralURL, "", caCertPEM)
	if err != nil {
		return RegisterResult{}, err
	}

	body, _ := json.Marshal(map[string]string{"name": name, "hostname": hostname})
	resp, err := client.HTTP.Post(centralURL+"/api/v1/agents/register", "application/json", bytes.NewReader(body))
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

type QueuedCommand struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

type CommandResult struct {
	CommandID string         `json:"command_id"`
	Status    string         `json:"status"` // "acked" or "failed"
	Result    map[string]any `json:"result,omitempty"`
}

type PollResponse struct {
	Commands []QueuedCommand `json:"commands"`
}

// JobInfo is one locally-defined job summary piggybacked onto every
// heartbeat, so the Central can show a combined "Tarefas de todos os
// servidores" table without a live remote round trip per agent.
type JobInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Cron    string `json:"cron"`
	Enabled bool   `json:"enabled"`
}

// RunningInfo is one job's current/just-finished execution progress,
// piggybacked the same way — lets the Central's progress bar for a remote
// job update without a live round trip.
type RunningInfo struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	Percent int    `json:"percent"`
	Message string `json:"message"`
}

// Poll sends one heartbeat with the agent's current status, its current
// list of job definitions, any running/just-finished execution progress,
// and any results from previously received commands; it returns newly
// queued commands.
func (c *Client) Poll(status string, results []CommandResult, jobs []JobInfo, running []RunningInfo) (PollResponse, error) {
	body, _ := json.Marshal(map[string]any{"status": status, "command_results": results, "jobs": jobs, "running": running})
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
