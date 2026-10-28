package settings

import (
	"encoding/json"

	"megaapp-back/internal/ws"
)

type WSRealtimePublisher struct {
	hub *ws.Hub
}

func NewWSRealtimePublisher(hub *ws.Hub) *WSRealtimePublisher {
	return &WSRealtimePublisher{hub: hub}
}

// updatedAtMillis is the write's commit timestamp (repo.MergeFields), included so recipients can
// discard a broadcast that arrives out of order relative to a newer one already applied — two
// quick PUTs from the same user race their own goroutines to the socket, not just the DB write.
func (p *WSRealtimePublisher) PublishChanged(userID int64, namespace string, fields map[string]json.RawMessage, excludeClientID string, updatedAtMillis int64) {
	p.hub.BroadcastToUser(userID, map[string]any{
		"type": "SETTINGS_UPDATED",
		"payload": map[string]any{
			"namespace": namespace,
			"fields":    fields,
			"updatedAt": updatedAtMillis,
		},
	}, excludeClientID)
}
