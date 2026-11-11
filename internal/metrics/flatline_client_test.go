package metrics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"megaapp-back/internal/metrics/wire"
)

func TestNewFlatlineClientDisablesKeepAlives(t *testing.T) {
	client := NewFlatlineClient("http://flatline", time.Second)
	transport, ok := client.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", client.client.Transport)
	}
	if !transport.DisableKeepAlives {
		t.Fatal("DisableKeepAlives = false, want true")
	}
}

func TestFlatlineClientPushSnapshotsSendsExpectedBody(t *testing.T) {
	var receivedRequest pushRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/metrics/snapshots" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedRequest); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pushResponse{Result: true})
	}))
	defer server.Close()

	client := NewFlatlineClient(server.URL, time.Second)
	snapshots := []MinuteSnapshot{{MinuteBucket: 120, Metrics: map[string]float64{"food_diary_entry_created": 2}}}

	if err := client.PushSnapshots(context.Background(), "megaapp", snapshots); err != nil {
		t.Fatalf("PushSnapshots() error = %v", err)
	}

	if receivedRequest.Service != "megaapp" {
		t.Fatalf("Service = %q, want megaapp", receivedRequest.Service)
	}
	if len(receivedRequest.Snapshots) != 1 || receivedRequest.Snapshots[0].MinuteBucket != 120 {
		t.Fatalf("Snapshots = %+v, want one snapshot at bucket 120", receivedRequest.Snapshots)
	}
	if receivedRequest.Snapshots[0].Granularity != GranularityMinute {
		t.Fatalf("Granularity = %q, want %q", receivedRequest.Snapshots[0].Granularity, GranularityMinute)
	}
}

func TestFlatlineClientPushSnapshotsReturnsErrorOnRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pushResponse{Result: false})
	}))
	defer server.Close()

	client := NewFlatlineClient(server.URL, time.Second)
	err := client.PushSnapshots(context.Background(), "megaapp", []MinuteSnapshot{{MinuteBucket: 60, Metrics: map[string]float64{"a": 1}}})
	if err == nil {
		t.Fatal("PushSnapshots() error = nil, want error")
	}
}

func TestFlatlineClientPushSnapshotsReturnsErrorOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer server.Close()

	client := NewFlatlineClient(server.URL, time.Second)
	err := client.PushSnapshots(context.Background(), "megaapp", []MinuteSnapshot{{MinuteBucket: 60, Metrics: map[string]float64{"a": 1}}})
	if err == nil {
		t.Fatal("PushSnapshots() error = nil, want error")
	}
}

func TestFlatlineClientSinceReturnsPoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/metrics/since" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("cursor") != "100" {
			t.Fatalf("cursor = %q, want 100", r.URL.Query().Get("cursor"))
		}
		if r.URL.Query().Get("minuteSince") != "10" || r.URL.Query().Get("hourSince") != "20" || r.URL.Query().Get("daySince") != "30" {
			t.Fatalf("floors = %q/%q/%q, want 10/20/30", r.URL.Query().Get("minuteSince"), r.URL.Query().Get("hourSince"), r.URL.Query().Get("daySince"))
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(wire.Encode([]wire.Series{
			{Service: "megaapp", MetricName: "a", Granularity: wire.GranularityMinute, Points: []wire.Point{{Bucket: 120, Value: 1}}},
		}))
	}))
	defer server.Close()

	client := NewFlatlineClient(server.URL, time.Second)
	points, err := client.Since(context.Background(), 100, 10, 20, 30)
	if err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	if len(points) != 1 || points[0].Name != "a" || points[0].Granularity != GranularityMinute {
		t.Fatalf("points = %+v, want one minute point named a", points)
	}
}

func TestFlatlineClientSinceReturnsErrorOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewFlatlineClient(server.URL, time.Second)
	if _, err := client.Since(context.Background(), 0, 0, 0, 0); err == nil {
		t.Fatal("Since() error = nil, want error")
	}
}

func TestFlatlineClientHistorySendsFloorsAndScopeAndReturnsRawResponse(t *testing.T) {
	wireBody := wire.Encode([]wire.Series{
		{Service: "bot-a", MetricName: "a", Granularity: wire.GranularityMinute, Points: []wire.Point{{Bucket: 180, Value: 1}}},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/metrics/history" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s, want POST metrics history", r.Method, r.URL.Path)
		}
		var body historyRequestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body error = %v", err)
		}
		if body.MinuteSince != 60 || body.HourSince != 120 || body.DaySince != 180 {
			t.Fatalf("floors = %+v, want 60/120/180", body)
		}
		if len(body.Scope) != 1 || body.Scope[0].Service != "bot-a" || len(body.Scope[0].MetricNames) != 1 || body.Scope[0].MetricNames[0] != "a" {
			t.Fatalf("scope = %+v, want [{bot-a [a]}]", body.Scope)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(wireBody)
	}))
	defer server.Close()

	client := NewFlatlineClient(server.URL, time.Second)
	resp, err := client.History(context.Background(), 60, 120, 180, []ScopeEntry{{Service: "bot-a", MetricNames: []string{"a"}}})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	defer resp.Body.Close()

	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(gotBody) != string(wireBody) {
		t.Fatalf("body = %v, want unread passthrough of Flatline's raw wire bytes %v", gotBody, wireBody)
	}
	if resp.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", resp.Header.Get("Content-Type"))
	}
}

func TestFlatlineClientHistoryReturnsErrorOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewFlatlineClient(server.URL, time.Second)
	if _, err := client.History(context.Background(), 60, 120, 180, []ScopeEntry{{Service: "bot-a", MetricNames: []string{"a"}}}); err == nil {
		t.Fatal("History() error = nil, want error")
	}
}
