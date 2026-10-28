package metrics

import (
	"sync"

	"megaapp-back/internal/metrics/wire"
	"megaapp-back/internal/ws"
)

// WS binary frames are [1 byte frame type][wire-encoded payload] — the type
// byte replaces the "type" JSON field the old SendJSON envelope used, so
// METRICS_UPDATE/METRICS_LATEST don't need a JSON wrapper around binary data
// (which would force it through base64 and defeat the point of a binary
// format). See plans/33-metrics-flow-tstorage-migration.implementation-plan.md §2.3.
const (
	wsFrameMetricsUpdate byte = 0
	wsFrameMetricsLatest byte = 1
)

type ServiceLatest struct {
	Service    string             `json:"service"`
	LastBucket int64              `json:"lastBucket"`
	Metrics    map[string]float64 `json:"metrics"`
}

type LatestSnapshot struct {
	Services []ServiceLatest `json:"services"`
}

type DetailUpdate struct {
	Points []MetricPoint `json:"points"`
}

type Realtime struct {
	hub *ws.Hub

	mu          sync.Mutex
	subscribers map[*ws.Client]map[scopeKey]struct{}
}

func NewRealtime(hub *ws.Hub) *Realtime {
	return &Realtime{hub: hub, subscribers: make(map[*ws.Client]map[scopeKey]struct{})}
}

func (r *Realtime) BroadcastLatest(adminUserIDs []int64, snapshot LatestSnapshot) {
	frame := append([]byte{wsFrameMetricsLatest}, encodeLatestSnapshot(snapshot)...)
	for _, userID := range adminUserIDs {
		r.hub.BroadcastBinaryToUser(userID, frame)
	}
}

// BroadcastDetail filters update.Points to each subscriber's own scope
// before sending — a client whose scope matches none of the points gets
// nothing on this tick, not an empty message.
func (r *Realtime) BroadcastDetail(update DetailUpdate) {
	for client, scope := range r.subscriberSnapshot() {
		points := filterPointsByScope(update.Points, scope)
		if len(points) == 0 {
			continue
		}
		frame := append([]byte{wsFrameMetricsUpdate}, encodePointsToWire(points)...)
		if err := client.SendBinary(frame); err != nil {
			r.Unsubscribe(client)
		}
	}
}

// encodePointsToWire pivots a flat, row-oriented point list into the wire
// format's per-series columnar shape — mirrors Flatline's own
// encodePointsToWire (internal/metrics/http.go), which does the same pivot
// on the read side of /api/metrics/since.
func encodePointsToWire(points []MetricPoint) []byte {
	type key struct {
		service     string
		metricName  string
		granularity string
	}
	order := make([]key, 0)
	bySeries := make(map[key][]wire.Point)
	for _, point := range points {
		k := key{service: point.Service, metricName: point.Name, granularity: point.Granularity}
		if _, exists := bySeries[k]; !exists {
			order = append(order, k)
		}
		bySeries[k] = append(bySeries[k], wire.Point{Bucket: point.Bucket, Value: point.Value})
	}

	series := make([]wire.Series, 0, len(order))
	for _, k := range order {
		series = append(series, wire.Series{
			Service:     k.service,
			MetricName:  k.metricName,
			Granularity: granularityToWire(k.granularity),
			Points:      bySeries[k],
		})
	}
	return wire.Encode(series)
}

// encodeLatestSnapshot packs one single-point series per (service,
// metricName) — the frontend's health-dot consumer only ever reads the
// latest value per metric, not a granularity-specific time series, so
// Granularity here is a fixed placeholder (minute) rather than meaningful
// per-metric data.
func encodeLatestSnapshot(snapshot LatestSnapshot) []byte {
	series := make([]wire.Series, 0, len(snapshot.Services))
	for _, service := range snapshot.Services {
		for name, value := range service.Metrics {
			series = append(series, wire.Series{
				Service:     service.Service,
				MetricName:  name,
				Granularity: wire.GranularityMinute,
				Points:      []wire.Point{{Bucket: service.LastBucket, Value: value}},
			})
		}
	}
	return wire.Encode(series)
}

func filterPointsByScope(points []MetricPoint, scope map[scopeKey]struct{}) []MetricPoint {
	filtered := make([]MetricPoint, 0, len(points))
	for _, point := range points {
		if _, inScope := scope[scopeKey{service: point.Service, metricName: point.Name}]; inScope {
			filtered = append(filtered, point)
		}
	}
	return filtered
}

// Subscribe replaces the client's scope entirely — it does not merge with
// whatever the client subscribed to before. A resubscribe always reflects
// what's on screen right now (e.g. after switching dashboard panels), never
// the union of everything a client has ever looked at this session.
func (r *Realtime) Subscribe(client *ws.Client, scope map[scopeKey]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subscribers[client] = scope
}

func (r *Realtime) Unsubscribe(client *ws.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subscribers, client)
}

func (r *Realtime) subscriberSnapshot() map[*ws.Client]map[scopeKey]struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()

	snapshot := make(map[*ws.Client]map[scopeKey]struct{}, len(r.subscribers))
	for client, scope := range r.subscribers {
		snapshot[client] = scope
	}
	return snapshot
}
