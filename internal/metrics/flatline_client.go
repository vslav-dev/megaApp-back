package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"megaapp-back/internal/metrics/wire"
)

type MinuteSnapshot struct {
	MinuteBucket int64
	Metrics      map[string]float64
}

type FlatlineClient struct {
	baseURL string
	client  *http.Client
}

func NewFlatlineClient(baseURL string, timeout time.Duration) *FlatlineClient {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	transport.DisableKeepAlives = true
	return &FlatlineClient{baseURL: baseURL, client: &http.Client{Timeout: timeout, Transport: transport}}
}

type pushRequest struct {
	Service   string             `json:"service"`
	Snapshots []outboundSnapshot `json:"snapshots"`
}

type outboundSnapshot struct {
	Granularity  string             `json:"granularity"`
	MinuteBucket int64              `json:"bucket"`
	Metrics      map[string]float64 `json:"metrics"`
}

type pushResponse struct {
	Result bool `json:"result"`
}

func (c *FlatlineClient) PushSnapshots(ctx context.Context, service string, snapshots []MinuteSnapshot) error {
	body := pushRequest{Service: service, Snapshots: make([]outboundSnapshot, 0, len(snapshots))}
	for _, snapshot := range snapshots {
		body.Snapshots = append(body.Snapshots, outboundSnapshot{Granularity: GranularityMinute, MinuteBucket: snapshot.MinuteBucket, Metrics: snapshot.Metrics})
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal flatline push body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/metrics/snapshots", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build flatline push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("send flatline push request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("flatline push status %d: %s", resp.StatusCode, string(respBody))
	}

	var response pushResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("decode flatline push response: %w", err)
	}
	if !response.Result {
		return errors.New("flatline push rejected")
	}

	return nil
}

// Since fetches points newer than cursor, optionally additionally bounded by
// age per granularity (minuteFloor/hourFloor/dayFloor — pass 0 for "no extra
// bound"). Bounding here, not after the fact in Go, keeps Flatline from
// having to scan and ship its entire retained history (weeks of minute rows
// across every service) for every call — see Poller.tick, which always bounds
// this to cap how much a stale cursor can catch up in one call.
//
// The response is Flatline's binary wire format (see internal/metrics/wire)
// — Poller needs the actual point values (dedup, latest/lastBucket tracking),
// so unlike History this is a real decode step, just a cheaper one than JSON.
func (c *FlatlineClient) Since(ctx context.Context, cursor, minuteFloor, hourFloor, dayFloor int64) ([]MetricPoint, error) {
	url := c.baseURL + "/api/metrics/since" +
		"?cursor=" + strconv.FormatInt(cursor, 10) +
		"&minuteSince=" + strconv.FormatInt(minuteFloor, 10) +
		"&hourSince=" + strconv.FormatInt(hourFloor, 10) +
		"&daySince=" + strconv.FormatInt(dayFloor, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build flatline since request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send flatline since request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("flatline since status %d: %s", resp.StatusCode, string(respBody))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read flatline since response: %w", err)
	}
	series, err := wire.Decode(body)
	if err != nil {
		return nil, fmt.Errorf("decode flatline since response: %w", err)
	}

	points := make([]MetricPoint, 0, len(series))
	for _, s := range series {
		granularity := granularityFromWire(s.Granularity)
		for _, p := range s.Points {
			points = append(points, MetricPoint{Service: s.Service, Name: s.MetricName, Granularity: granularity, Bucket: p.Bucket, Value: p.Value})
		}
	}
	return points, nil
}

func granularityFromWire(g wire.Granularity) string {
	switch g {
	case wire.GranularityHour:
		return GranularityHour
	case wire.GranularityDay:
		return GranularityDay
	default:
		return GranularityMinute
	}
}

func granularityToWire(g string) wire.Granularity {
	switch g {
	case GranularityHour:
		return wire.GranularityHour
	case GranularityDay:
		return wire.GranularityDay
	default:
		return wire.GranularityMinute
	}
}

type historyRequestBody struct {
	MinuteSince int64        `json:"minuteSince"`
	HourSince   int64        `json:"hourSince"`
	DaySince    int64        `json:"daySince"`
	Scope       []ScopeEntry `json:"scope"`
}

// History is a POST, not a GET — scope (per-service metric-name lists)
// doesn't fit cleanly on a query string. Scope is forwarded to Flatline
// unchanged, not transformed — see
// plans/32-metrics-history-scope-filter.implementation-plan.md §3.1.
//
// Returns Flatline's raw response on success for the caller to stream
// through unread (see HistoryHandler.History) — the binary wire payload
// never needs decoding in megaapp-back, only in the browser. On success the
// caller owns resp.Body and must close it; on error the body is already
// drained and closed here.
func (c *FlatlineClient) History(ctx context.Context, minuteFloor, hourFloor, dayFloor int64, scope []ScopeEntry) (*http.Response, error) {
	payload, err := json.Marshal(historyRequestBody{MinuteSince: minuteFloor, HourSince: hourFloor, DaySince: dayFloor, Scope: scope})
	if err != nil {
		return nil, fmt.Errorf("marshal flatline history body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/metrics/history", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build flatline history request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send flatline history request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("flatline history status %d: %s", resp.StatusCode, string(respBody))
	}

	return resp, nil
}
