package metrics

import (
	"context"
	"encoding/json"

	"megaapp-back/internal/ws"
)

func NewSubscribeHandler(service *Service, realtime *Realtime) ws.MessageHandler {
	return func(client *ws.Client, message map[string]any) error {
		ctx := context.Background()

		isAdmin, err := service.IsAdmin(ctx, client.UserID())
		if err != nil {
			return err
		}
		if !isAdmin {
			return nil
		}

		scope, err := decodeSubscribeScope(message)
		if err != nil {
			return err
		}

		realtime.Subscribe(client, scope)
		return nil
	}
}

func NewUnsubscribeHandler(realtime *Realtime) ws.MessageHandler {
	return func(client *ws.Client, _ map[string]any) error {
		realtime.Unsubscribe(client)
		return nil
	}
}

func decodeSubscribeScope(message map[string]any) (map[scopeKey]struct{}, error) {
	payload, ok := message["payload"]
	if !ok {
		return nil, ErrInvalidScope
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	var body struct {
		Scope []ScopeEntry `json:"scope"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		return nil, err
	}
	return buildScopeSet(body.Scope)
}
