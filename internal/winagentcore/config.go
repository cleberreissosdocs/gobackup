// Package winagentcore (config.go): local connection settings for
// gobackup-winagent. There is no Postgres/web UI on this platform, so this
// is a small JSON file — the Windows equivalent of the Linux agent's
// `agent_settings` table.
package winagentcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cleber/gobackup/internal/poller"
)

type Config struct {
	CentralURL   string `json:"central_url"`
	Token        string `json:"token"`
	AgentID      string `json:"agent_id"`
	Name         string `json:"name"`
	Hostname     string `json:"hostname"`
	CACertPEM    string `json:"ca_cert_pem"`
	PasswordHash string `json:"password_hash,omitempty"`
}

func ConfigPath() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		// Not running on Windows (or ProgramData unset) — fall back to the
		// working directory so local testing still works.
		base = "."
	}
	return filepath.Join(base, "GoBackup", "config.json")
}

func LoadConfig() (Config, error) {
	var c Config
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return c, nil
}

func SaveConfig(c Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// 0600-equivalent: Windows ACLs aren't controlled by this bit, but it's
	// harmless and matches what the Linux side would do for a secrets file.
	return os.WriteFile(path, data, 0o600)
}

// IsConfigured reports whether this agent has ever registered with a
// Central — used by both the CLI's `serve`/`status` and the tray app to
// decide what to show before anything is set up.
func (c Config) IsConfigured() bool {
	return c.CentralURL != "" && c.Token != ""
}

// RegisterAndSave registers this machine with a Central and persists the
// resulting connection locally — the single operation both the CLI's
// `register` command and the tray app's configuration screen perform.
func RegisterAndSave(centralURL, caPath, name string) (Config, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "windows-agent"
	}
	agentName := name
	if agentName == "" {
		agentName = hostname
	}

	var caPEM []byte
	if caPath != "" {
		caPEM, err = poller.LoadCACertFile(caPath)
		if err != nil {
			return Config{}, fmt.Errorf("ler certificado CA: %w", err)
		}
	}

	res, err := poller.Register(centralURL, agentName, hostname, caPEM)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		CentralURL: centralURL,
		Token:      res.Token,
		AgentID:    res.AgentID,
		Name:       agentName,
		Hostname:   hostname,
		CACertPEM:  string(caPEM),
	}
	if err := SaveConfig(cfg); err != nil {
		return Config{}, fmt.Errorf("salvar configuração: %w", err)
	}
	return cfg, nil
}

// Unregister clears the local registration only — it does NOT remove the
// agent from the Central's own dashboard (use "Apagar" there for that).
func Unregister() error {
	return SaveConfig(Config{})
}
