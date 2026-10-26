package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/db"
)

// Dispatcher routes an alert event to every notification_channel a tenant
// has configured for it.
type Dispatcher struct {
	Store *db.Store
}

// Dispatch sends subject/message to every channel of tenantID whose Events
// list is empty (meaning "all events") or contains eventName.
func (d *Dispatcher) Dispatch(ctx context.Context, tenantID uuid.UUID, eventName, subject, message string) {
	channels, err := d.Store.ListNotificationChannels(ctx, tenantID)
	if err != nil {
		log.Println("notify: list channels:", err)
		return
	}

	for _, ch := range channels {
		if !matchesEvent(ch.Events, eventName) {
			continue
		}
		if err := send(ch, subject, message); err != nil {
			log.Printf("notify: send via %s channel %s failed: %v", ch.Type, ch.ID, err)
		} else {
			log.Printf("notify: sent %s alert via %s channel %s", eventName, ch.Type, ch.ID)
		}
	}
}

func matchesEvent(events []string, eventName string) bool {
	if len(events) == 0 {
		return true
	}
	for _, e := range events {
		if e == eventName {
			return true
		}
	}
	return false
}

func send(ch db.NotificationChannelRow, subject, message string) error {
	raw, err := json.Marshal(ch.Config)
	if err != nil {
		return fmt.Errorf("marshal channel config: %w", err)
	}

	switch ch.Type {
	case "email":
		var cfg EmailConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("decode email config: %w", err)
		}
		return SendEmail(cfg, subject, message)

	case "telegram":
		var cfg TelegramConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("decode telegram config: %w", err)
		}
		return SendTelegram(cfg, subject+"\n\n"+message)

	default:
		return fmt.Errorf("unsupported channel type: %s", ch.Type)
	}
}
