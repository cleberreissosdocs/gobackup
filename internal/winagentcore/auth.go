// Package winagentcore (auth.go): local password protection for the tray
// app's loopback-only config page. There is no multi-user model here (it's
// a single-machine desktop tool) — just one local password, defaulting to
// "admin" on a brand new install so it's usable immediately, with a
// settable replacement persisted in config.json alongside the Central
// registration.
package winagentcore

import "golang.org/x/crypto/bcrypt"

// DefaultPassword is accepted whenever no password has ever been set
// (fresh install, PasswordHash empty) — matches the documented default
// admin/admin used by every other GoBackup surface.
const DefaultPassword = "admin"

// CheckPassword verifies password against the stored hash, falling back to
// DefaultPassword when the config has never had one set.
func (c Config) CheckPassword(password string) bool {
	if c.PasswordHash == "" {
		return password == DefaultPassword
	}
	return bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)) == nil
}

// SetPassword hashes and persists a new local password, leaving every
// other config field (Central registration, etc.) untouched.
func SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.PasswordHash = string(hash)
	return SaveConfig(cfg)
}
