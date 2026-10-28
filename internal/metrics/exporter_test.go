package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestExporterFlushAndPushAcksOnSuccess(t *testing.T) {
	dir := t.TempDir()
	var receivedBuckets []int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req pushRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, snapshot := range req.Snapshots {
			receivedBuckets = append(receivedBuckets, snapshot.MinuteBucket)
		}
		_ = json.NewEncoder(w).Encode(pushResponse{Result: true})
	}))
	defer server.Close()

	exporter, err := NewExporter(ExporterConfig{
		Service:    "megaapp",
		NDJSONPath: filepath.Join(dir, "metrics-outbox.ndjson"),
		AckPath:    filepath.Join(dir, "metrics-outbox.ack.json"),
	}, NewFlatlineClient(server.URL, time.Second), discardLogger())
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}

	exporter.FlushAndPush(context.Background(), 60, []MetricPoint{{Name: "a", Value: 1}})

	if len(receivedBuckets) != 1 || receivedBuckets[0] != 60 {
		t.Fatalf("receivedBuckets = %v, want [60]", receivedBuckets)
	}
	if len(exporter.pending) != 0 {
		t.Fatalf("len(pending) = %d, want 0 after successful push", len(exporter.pending))
	}

	ackedBucket, err := readAck(filepath.Join(dir, "metrics-outbox.ack.json"))
	if err != nil {
		t.Fatalf("readAck() error = %v", err)
	}
	if ackedBucket != 60 {
		t.Fatalf("ackedBucket = %d, want 60", ackedBucket)
	}
}

func TestExporterFlushAndPushKeepsPendingOnFailureAndRetriesNextTick(t *testing.T) {
	dir := t.TempDir()
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var req pushRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Snapshots) != 2 {
			t.Errorf("second attempt snapshots = %d, want 2 (both pending buckets)", len(req.Snapshots))
		}
		_ = json.NewEncoder(w).Encode(pushResponse{Result: true})
	}))
	defer server.Close()

	exporter, err := NewExporter(ExporterConfig{
		Service:    "megaapp",
		NDJSONPath: filepath.Join(dir, "metrics-outbox.ndjson"),
		AckPath:    filepath.Join(dir, "metrics-outbox.ack.json"),
	}, NewFlatlineClient(server.URL, time.Second), discardLogger())
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}

	exporter.FlushAndPush(context.Background(), 60, []MetricPoint{{Name: "a", Value: 1}})
	if len(exporter.pending) != 1 {
		t.Fatalf("len(pending) after failed push = %d, want 1", len(exporter.pending))
	}

	exporter.FlushAndPush(context.Background(), 120, []MetricPoint{{Name: "a", Value: 1}})
	if len(exporter.pending) != 0 {
		t.Fatalf("len(pending) after successful retry = %d, want 0", len(exporter.pending))
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestExporterDoesNotSendOrAckBeforeOutboxWriteSucceeds(t *testing.T) {
	dir := t.TempDir()
	var receivedBuckets []int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req pushRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, snapshot := range req.Snapshots {
			receivedBuckets = append(receivedBuckets, snapshot.MinuteBucket)
		}
		_ = json.NewEncoder(w).Encode(pushResponse{Result: true})
	}))
	defer server.Close()

	exporter, err := NewExporter(ExporterConfig{
		Service:    "megaapp",
		NDJSONPath: filepath.Join(dir, "metrics-outbox.ndjson"),
		AckPath:    filepath.Join(dir, "metrics-outbox.ack.json"),
	}, NewFlatlineClient(server.URL, time.Second), discardLogger())
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}

	blockedPath := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blockedPath, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	activePath := exporter.outbox.activePath
	exporter.outbox.activePath = blockedPath
	exporter.FlushAndPush(context.Background(), 60, []MetricPoint{{Name: "a", Value: 1}})

	if len(receivedBuckets) != 0 || len(exporter.pending) != 0 || len(exporter.unpersisted) != 1 {
		t.Fatalf("after write failure: received=%v pending=%d unpersisted=%d", receivedBuckets, len(exporter.pending), len(exporter.unpersisted))
	}
	if _, err := os.Stat(filepath.Join(dir, "metrics-outbox.ack.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ack stat error = %v, want not exist", err)
	}

	exporter.outbox.activePath = activePath
	exporter.FlushAndPush(context.Background(), 0, nil)
	if len(receivedBuckets) != 1 || receivedBuckets[0] != 60 || len(exporter.unpersisted) != 0 {
		t.Fatalf("after retry: received=%v unpersisted=%d", receivedBuckets, len(exporter.unpersisted))
	}
}

func TestExporterIgnoresEmptyPoints(t *testing.T) {
	dir := t.TempDir()
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	exporter, err := NewExporter(ExporterConfig{
		Service:    "megaapp",
		NDJSONPath: filepath.Join(dir, "metrics-outbox.ndjson"),
		AckPath:    filepath.Join(dir, "metrics-outbox.ack.json"),
	}, NewFlatlineClient(server.URL, time.Second), discardLogger())
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}

	exporter.FlushAndPush(context.Background(), 60, nil)
	if called {
		t.Fatal("server was called, want no-op on empty points")
	}
}

func TestNewExporterRestoresLastAckedFromDisk(t *testing.T) {
	dir := t.TempDir()
	ackPath := filepath.Join(dir, "metrics-outbox.ack.json")
	if err := writeAck(ackPath, 60); err != nil {
		t.Fatalf("writeAck() error = %v", err)
	}
	outboxPath := filepath.Join(dir, "metrics-outbox.ndjson")
	writeTestOutbox(t, outboxPath,
		outboxLine{Service: "megaapp", MinuteBucket: 120, Metrics: map[string]float64{"value": 1}},
		outboxLine{Service: "another-service"},
	)
	writeTestOutbox(t, numberedOutboxPath(outboxPath, 1),
		outboxLine{Service: "megaapp", MinuteBucket: 240, Metrics: map[string]float64{"value": 4}},
		outboxLine{Service: "megaapp", MinuteBucket: 120, Metrics: map[string]float64{"value": 2}},
		outboxLine{Service: "megaapp", MinuteBucket: 60, Metrics: map[string]float64{"value": 1}},
	)

	exporter, err := NewExporter(ExporterConfig{
		Service:    "megaapp",
		NDJSONPath: outboxPath,
		AckPath:    ackPath,
	}, NewFlatlineClient("http://127.0.0.1:0", time.Second), discardLogger())
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}

	if exporter.lastAcked != 60 {
		t.Fatalf("lastAcked = %d, want 60", exporter.lastAcked)
	}
	if len(exporter.pending) != 2 {
		t.Fatalf("len(pending) = %d, want 2", len(exporter.pending))
	}
	if exporter.pending[0].MinuteBucket != 120 || exporter.pending[0].Metrics["value"] != 2 {
		t.Fatalf("pending[0] = %+v, want deduplicated bucket 120 from latest chunk", exporter.pending[0])
	}
	if exporter.pending[1].MinuteBucket != 240 {
		t.Fatalf("pending[1] = %+v, want bucket 240", exporter.pending[1])
	}
}

func TestNewExporterRejectsMalformedOutboxData(t *testing.T) {
	dir := t.TempDir()
	outboxPath := filepath.Join(dir, "metrics-outbox.ndjson")
	if err := os.WriteFile(outboxPath, []byte("{not-json}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := NewExporter(ExporterConfig{
		Service:    "megaapp",
		NDJSONPath: outboxPath,
		AckPath:    filepath.Join(dir, "metrics-outbox.ack.json"),
	}, NewFlatlineClient("http://127.0.0.1:0", time.Second), discardLogger())
	if err == nil {
		t.Fatal("NewExporter() error = nil, want malformed outbox error")
	}
}

func TestExporterReplaysInBoundedBatchesAndAcksSuccessfulPrefix(t *testing.T) {
	dir := t.TempDir()
	outboxPath := filepath.Join(dir, "metrics-outbox.ndjson")
	lines := make([]outboxLine, 60)
	for i := range lines {
		lines[i] = outboxLine{
			Service:      "megaapp",
			MinuteBucket: int64(i+1) * 60,
			Metrics:      map[string]float64{"value": float64(i)},
		}
	}
	writeTestOutbox(t, outboxPath, lines...)

	var requestSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req pushRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Decode() error = %v", err)
		}
		requestSizes = append(requestSizes, len(req.Snapshots))
		if len(requestSizes) == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(pushResponse{Result: true})
	}))
	defer server.Close()

	ackPath := filepath.Join(dir, "metrics-outbox.ack.json")
	exporter, err := NewExporter(ExporterConfig{
		Service:    "megaapp",
		NDJSONPath: outboxPath,
		AckPath:    ackPath,
	}, NewFlatlineClient(server.URL, time.Second), discardLogger())
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}

	exporter.FlushAndPush(context.Background(), 0, nil)

	if len(requestSizes) != 2 || requestSizes[0] != 25 || requestSizes[1] != 25 {
		t.Fatalf("requestSizes = %v, want [25 25]", requestSizes)
	}
	if len(exporter.pending) != 35 {
		t.Fatalf("len(pending) = %d, want 35", len(exporter.pending))
	}
	acked, err := readAck(ackPath)
	if err != nil {
		t.Fatalf("readAck() error = %v", err)
	}
	if acked != 25*60 {
		t.Fatalf("acked = %d, want %d", acked, 25*60)
	}
}

func TestReadAckReturnsZeroWhenFileMissing(t *testing.T) {
	bucket, err := readAck(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("readAck() error = %v", err)
	}
	if bucket != 0 {
		t.Fatalf("bucket = %d, want 0", bucket)
	}
}

func TestWriteAckThenReadAckRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ack.json")
	if err := writeAck(path, 42); err != nil {
		t.Fatalf("writeAck() error = %v", err)
	}

	bucket, err := readAck(path)
	if err != nil {
		t.Fatalf("readAck() error = %v", err)
	}
	if bucket != 42 {
		t.Fatalf("bucket = %d, want 42", bucket)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file state error = %v, want not exists", err)
	}
}

func writeTestOutbox(t *testing.T, path string, lines ...outboxLine) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	encoder := json.NewEncoder(file)
	for _, line := range lines {
		if err := encoder.Encode(line); err != nil {
			_ = file.Close()
			t.Fatalf("Encode() error = %v", err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
