// Package notify sends alerts (email, Telegram) about backup job failures
// and agent offline events, routed through the notification_channels a
// tenant configured on the Central.
package notify

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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
	// Encryption picks how the connection to SMTPHost:SMTPPort is secured:
	//   ""/"auto" (default) — plain connect, then upgrade with STARTTLS if
	//     the server advertises it (works for the common 587/25 case).
	//   "ssl"  — implicit TLS from the first byte (the common 465 case;
	//     STARTTLS doesn't exist on that port, the server speaks TLS
	//     immediately).
	//   "none" — never encrypt, even if STARTTLS is offered. Only for a
	//     trusted internal relay the admin explicitly chose not to
	//     encrypt to; login/password are still sent in that case ONLY
	//     because the admin opted in on this exact channel, overriding
	//     the net/smtp standard library's usual (correct) refusal to send
	//     credentials over a connection it can't confirm is encrypted.
	Encryption string `json:"encryption"`
}

// TelegramConfig is the shape expected in notification_channels.config for
// type=telegram.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// SendEmail delivers a plaintext email over SMTP, choosing the encryption
// mode from cfg.Encryption (see EmailConfig). Unlike plain smtp.SendMail,
// this gives explicit control over implicit TLS (port 465) and lets an
// admin who knowingly points this at an unencrypted internal relay opt out
// of the stdlib's otherwise-correct refusal to send credentials in the
// clear.
func SendEmail(cfg EmailConfig, subject, body string) error {
	addr := net.JoinHostPort(cfg.SMTPHost, fmt.Sprintf("%d", cfg.SMTPPort))
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\r\n", cfg.From, cfg.To, subject, body)

	dialer := &net.Dialer{Timeout: 15 * time.Second}

	var conn net.Conn
	var err error
	if cfg.Encryption == "ssl" {
		// Implicit TLS (classic port 465): the server speaks TLS from the
		// very first byte — there is no STARTTLS handshake to negotiate.
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: cfg.SMTPHost})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	c, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer c.Close()

	secure := cfg.Encryption == "ssl"
	if cfg.Encryption != "ssl" && cfg.Encryption != "none" {
		// "auto" (default, including pre-existing channels saved before
		// this field existed): upgrade in place if the server offers it —
		// covers the common port 587/25 STARTTLS case.
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: cfg.SMTPHost}); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
			secure = true
		}
	}

	if cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("servidor SMTP não anuncia suporte a autenticação (AUTH) — confira se a porta/criptografia estão certas")
		}
		var auth smtp.Auth
		if secure {
			auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
		} else if cfg.Encryption == "none" {
			// Explicit admin opt-out of encryption on this channel — send
			// PLAIN credentials anyway instead of letting the stdlib's
			// smtp.PlainAuth refuse with "unencrypted connection".
			auth = plainAuthInsecure{cfg.Username, cfg.Password}
		} else {
			return errors.New("conexão não criptografada: o servidor não ofereceu STARTTLS. " +
				"Use a porta 465 com Criptografia=SSL/TLS implícito, ou marque " +
				"Criptografia=Nenhuma se este for um relay interno confiável sem TLS")
		}
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	if err := c.Mail(cfg.From); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := c.Rcpt(cfg.To); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		w.Close()
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}
	return c.Quit()
}

// plainAuthInsecure implements smtp.Auth with the same wire format as
// smtp.PlainAuth (RFC 4616), minus its (normally correct) refusal to run
// over a connection it can't confirm is encrypted — used only when the
// admin explicitly set Encryption="none" on a channel.
type plainAuthInsecure struct {
	username, password string
}

func (a plainAuthInsecure) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	resp := []byte("\x00" + a.username + "\x00" + a.password)
	return "PLAIN", resp, nil
}

func (a plainAuthInsecure) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
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
