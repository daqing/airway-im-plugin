// Package notify queues host-domain notifications onto the IM delivery
// pipeline. A host (the KongChat backend, or any platform embedding the
// plugin) can push arbitrary events — friend requests, mail alerts, anything
// — to specific users over the same authenticated WebSocket gateway the chat
// itself uses, with the pipeline's usual at-least-once delivery and
// event-id dedupe.
//
// Events are advisory: they reach recipients that are online (or reconnect
// while the gateway still holds them in its bounded queue). Offline users
// are NOT replayed, so hosts must pair every notification with a pull
// endpoint clients can load on demand.
package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/daqing/airway/lib/repo"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/utils"
)

const (
	// MaxUserUUIDs caps the recipients of a single event.
	MaxUserUUIDs = 100
	// MaxEventName caps the event name length.
	MaxEventName = 64
	// MaxDataBytes caps the encoded JSON data payload.
	MaxDataBytes = 4096
)

type frameTargets struct {
	UserUUIDs []string `json:"user_uuids"`
}

type frame struct {
	EventID        string          `json:"event_id"`
	Event          string          `json:"event"`
	ConversationID string          `json:"conversation_id"`
	Data           json.RawMessage `json:"data"`
	Targets        frameTargets    `json:"targets"`
}

// Emit queues one notification. userUUIDs are deduplicated (order kept),
// event is a free-form name (conventionally prefixed with "host.", e.g.
// "host.friend_request"), and data any JSON value; nil data becomes {}.
// Returns the generated event id. Requires the framework database to be
// initialized (any point after boot).
func Emit(userUUIDs []string, event string, data any) (string, error) {
	cleaned := make([]string, 0, len(userUUIDs))
	seen := map[string]bool{}
	for _, uuid := range userUUIDs {
		uuid = strings.TrimSpace(uuid)
		if uuid == "" || seen[uuid] {
			continue
		}
		seen[uuid] = true
		cleaned = append(cleaned, uuid)
	}
	if len(cleaned) == 0 {
		return "", errors.New("notify: at least one user uuid is required")
	}
	if len(cleaned) > MaxUserUUIDs {
		return "", fmt.Errorf("notify: at most %d recipients per event", MaxUserUUIDs)
	}

	event = strings.TrimSpace(event)
	if event == "" || len(event) > MaxEventName {
		return "", fmt.Errorf("notify: event name must be 1-%d characters", MaxEventName)
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("notify: encode data: %w", err)
	}
	if len(encoded) > MaxDataBytes {
		return "", fmt.Errorf("notify: data must be at most %d encoded bytes", MaxDataBytes)
	}
	if string(encoded) == "null" {
		encoded = []byte("{}")
	}

	eventID, err := utils.NewULID()
	if err != nil {
		return "", err
	}

	payload, err := json.Marshal(frame{
		EventID:        eventID,
		Event:          event,
		ConversationID: "",
		Data:           encoded,
		Targets:        frameTargets{UserUUIDs: cleaned},
	})
	if err != nil {
		return "", fmt.Errorf("notify: encode frame: %w", err)
	}

	if _, err := repo.CurrentDB().Conn().Exec(
		`INSERT INTO outbox_events (id, topic, aggregate_id, payload, created_at, attempts)
		 VALUES (?, ?, ?, ?, ?, 0)`,
		eventID, event, event, string(payload), time.Now().UTC()); err != nil {
		return "", err
	}
	return eventID, nil
}
