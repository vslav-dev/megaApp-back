package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

type fakeRealtime struct {
	detailCalls []DetailUpdate
	latestCalls []LatestSnapshot
	adminIDs    [][]int64
}

func (f *fakeRealtime) BroadcastDetail(update DetailUpdate) {
	f.detailCalls = append(f.detailCalls, update)
}

func (f *fakeRealtime) BroadcastLatest(adminUserIDs []int64, snapshot LatestSnapshot) {
	f.adminIDs = append(f.adminIDs, adminUserIDs)
	f.latestCalls = append(f.latestCalls, snapshot)
}

func TestInitialPollerCursorAppliesLookback(t *testing.T) {
	now := time.Date(2026, 6, 21, 14, 33, 7, 0, time.UTC)
	got := initialPollerCursor(now, 2*time.Minute)
	want := time.Date(2026, 6, 21, 14, 31, 0, 0, time.UTC).Unix()
	if got != want {
		t.Fatalf("initialPollerCursor() = %d, want %d", got, want)
	}
}

func TestPollerTickAdvancesCursorAndBroadcasts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") != "0" {
			t.Fatalf("cursor = %q, want 0", r.URL.Query().Get("cursor"))
		}
		_, _ = w.Write(encodePointsToWire([]MetricPoint{
			{Service: "megaapp", Name: "food_diary_entry_created", Bucket: 120, Value: 2},
			{Service: "spread-capture-bot-v3", Name: "heartbeat", Bucket: 180, Value: 1},
		}))
	}))
	defer server.Close()

	realtime := &fakeRealtime{}
	admins := fakeAdminLister{adminUserIDs: []int64{7}}
	poller := NewPoller(NewFlatlineClient(server.URL, time.Second), realtime, admins, time.Second, time.Minute, time.Hour, fixedMetricsClock{now: time.Unix(0, 0)}, discardLogger())
	poller.cursor = 0

	poller.tick(context.Background())

	if poller.cursor != 180 {
		t.Fatalf("cursor = %d, want 180", poller.cursor)
	}
	if len(realtime.detailCalls) != 1 || len(realtime.detailCalls[0].Points) != 2 {
		t.Fatalf("detailCalls = %+v, want one call with two points", realtime.detailCalls)
	}
	if len(realtime.latestCalls) != 1 {
		t.Fatalf("latestCalls = %+v, want one call", realtime.latestCalls)
	}

	snapshot := realtime.latestCalls[0]
	if len(snapshot.Services) != 2 {
		t.Fatalf("services = %+v, want two services", snapshot.Services)
	}
	if snapshot.Services[0].Service != "megaapp" || snapshot.Services[0].Metrics["food_diary_entry_created"] != 2 {
		t.Fatalf("megaapp service = %+v, want food_diary_entry_created=2", snapshot.Services[0])
	}
	if snapshot.Services[1].Service != "spread-capture-bot-v3" || snapshot.Services[1].LastBucket != 180 {
		t.Fatalf("bot service = %+v, want lastBucket=180", snapshot.Services[1])
	}
	if len(realtime.adminIDs) != 1 || realtime.adminIDs[0][0] != 7 {
		t.Fatalf("adminIDs = %+v, want [[7]]", realtime.adminIDs)
	}
}

func TestPollerTickBoundsCatchUpFloorRegardlessOfCursorAge(t *testing.T) {
	now := time.Unix(10_000, 0)
	maxCatchUp := time.Hour
	wantFloor := now.Add(-maxCatchUp).Unix()

	var gotMinuteSince, gotHourSince, gotDaySince string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMinuteSince = r.URL.Query().Get("minuteSince")
		gotHourSince = r.URL.Query().Get("hourSince")
		gotDaySince = r.URL.Query().Get("daySince")
		_, _ = w.Write(encodePointsToWire(nil))
	}))
	defer server.Close()

	poller := NewPoller(NewFlatlineClient(server.URL, time.Second), &fakeRealtime{}, fakeAdminLister{}, time.Second, time.Minute, maxCatchUp, fixedMetricsClock{now: now}, discardLogger())
	poller.cursor = 0 // simulate a cursor stuck far in the past after an outage

	poller.tick(context.Background())

	wantFloorStr := strconv.FormatInt(wantFloor, 10)
	for name, got := range map[string]string{"minuteSince": gotMinuteSince, "hourSince": gotHourSince, "daySince": gotDaySince} {
		if got != wantFloorStr {
			t.Fatalf("%s = %q, want %q", name, got, wantFloorStr)
		}
	}
}

func TestPollerTickSkipsBroadcastWhenNoNewPoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encodePointsToWire(nil))
	}))
	defer server.Close()

	realtime := &fakeRealtime{}
	poller := NewPoller(NewFlatlineClient(server.URL, time.Second), realtime, fakeAdminLister{}, time.Second, time.Minute, time.Hour, fixedMetricsClock{now: time.Unix(0, 0)}, discardLogger())

	poller.tick(context.Background())

	if len(realtime.detailCalls) != 0 || len(realtime.latestCalls) != 0 {
		t.Fatalf("expected no broadcasts, got detail=%d latest=%d", len(realtime.detailCalls), len(realtime.latestCalls))
	}
}

func TestPollerTickSkipsRebroadcastOfUnchangedPoint(t *testing.T) {
	// Mirrors Flatline's Since() behavior for a granularity's most recently
	// closed bucket: it keeps satisfying `bucket+step > cursor` and gets
	// returned again on every poll until a newer bucket appears, even though
	// the Poller's own cursor never moves past it. See poller.go comment.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encodePointsToWire([]MetricPoint{
			{Service: "hardware:1.2.3.4", Name: "load1", Granularity: GranularityMinute, Bucket: 120, Value: 0.5},
		}))
	}))
	defer server.Close()

	realtime := &fakeRealtime{}
	poller := NewPoller(NewFlatlineClient(server.URL, time.Second), realtime, fakeAdminLister{}, time.Second, time.Minute, time.Hour, fixedMetricsClock{now: time.Unix(0, 0)}, discardLogger())
	poller.cursor = 0

	poller.tick(context.Background())
	poller.tick(context.Background())
	poller.tick(context.Background())

	if len(realtime.detailCalls) != 1 {
		t.Fatalf("detailCalls = %+v, want exactly one (first tick only)", realtime.detailCalls)
	}
	if len(realtime.latestCalls) != 3 {
		t.Fatalf("latestCalls = %d, want 3 (health channel still ticks every poll)", len(realtime.latestCalls))
	}
}

func TestPollerStartAndCloseStopsCleanly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encodePointsToWire(nil))
	}))
	defer server.Close()

	poller := NewPoller(NewFlatlineClient(server.URL, time.Second), &fakeRealtime{}, fakeAdminLister{}, 10*time.Millisecond, time.Minute, time.Hour, fixedMetricsClock{now: time.Unix(0, 0)}, discardLogger())
	poller.Start()
	time.Sleep(20 * time.Millisecond)
	if err := poller.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
