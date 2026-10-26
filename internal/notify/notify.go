// Package notify sends alerts (email, Telegram) about backup job failures
// and agent offline events, routed through the notification_channels a
// tenant configured on the Central.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"time"
)

// EmailConfig is the shape expected in notification_channels.config for
// type=email.
type EmailConfig struct {
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// TelegramConfig is the shape expected in notification_channels.config for
// type=telegram.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// SendEmail delivers a plaintext email over SMTP. It uses PLAIN auth when a
// username/password is configured, and no auth otherwise (fine for local
// test relays / internal mail servers).
func SendEmail(cfg EmailConfig, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\r\n", cfg.From, cfg.To, subject, body)

	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
	}

	if err := smtp.SendMail(addr, auth, cfg.From, []string{cfg.To}, []byte(msg)); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}

// SendTelegram posts a message to a chat via the Telegram Bot API.
func SendTelegram(cfg TelegramConfig, message string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	payload, _ := json.Marshal(map[string]string{
		"chat_id": cfg.ChatID,
		"text":    message,
	})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("telegram request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}
	return nil
}
