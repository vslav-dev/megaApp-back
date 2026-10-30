package metrics

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendOutboxLineWritesValidJSONLine(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "metrics-outbox.ndjson")
	box, err := prepareOutbox(basePath, outboxChunkLimitBytes)
	if err != nil {
		t.Fatalf("prepareOutbox() error = %v", err)
	}

	if err := appendOutboxLine(box, "megaapp", MinuteSnapshot{MinuteBucket: 60, Metrics: map[string]float64{"a": 1}}); err != nil {
		t.Fatalf("appendOutboxLine() error = %v", err)
	}

	lines := readOutboxLines(t, box.activePath)
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(lines))
	}
	if lines[0].Service != "megaapp" || lines[0].MinuteBucket != 60 || lines[0].Metrics["a"] != 1 {
		t.Fatalf("line = %+v, want service=megaapp bucket=60 a=1", lines[0])
	}
}

func TestAppendOutboxLineRotatesChunkWhenLimitExceeded(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "metrics-outbox.ndjson")
	box, err := prepareOutbox(basePath, 10) // tiny limit forces rotation on the very first line
	if err != nil {
		t.Fatalf("prepareOutbox() error = %v", err)
	}

	if err := appendOutboxLine(box, "megaapp", MinuteSnapshot{MinuteBucket: 60, Metrics: map[string]float64{"a": 1}}); err != nil {
		t.Fatalf("appendOutboxLine() error = %v", err)
	}
	if err := appendOutboxLine(box, "megaapp", MinuteSnapshot{MinuteBucket: 120, Metrics: map[string]float64{"a": 1}}); err != nil {
		t.Fatalf("appendOutboxLine() second error = %v", err)
	}

	if box.activePath == basePath {
		t.Fatalf("activePath = %q, want a numbered chunk, not the base path", box.activePath)
	}

	entries, err := os.ReadDir(filepath.Dir(basePath))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("len(entries) = %d, want at least 2 chunk files", len(entries))
	}
}

func TestPrepareOutboxResumesLastChunkAcrossRestart(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "metrics-outbox.ndjson")

	box, err := prepareOutbox(basePath, outboxChunkLimitBytes)
	if err != nil {
		t.Fatalf("prepareOutbox() error = %v", err)
	}
	if err := appendOutboxLine(box, "megaapp", MinuteSnapshot{MinuteBucket: 60, Metrics: map[string]float64{"a": 1}}); err != nil {
		t.Fatalf("appendOutboxLine() error = %v", err)
	}

	reopened, err := prepareOutbox(basePath, outboxChunkLimitBytes)
	if err != nil {
		t.Fatalf("prepareOutbox() reopen error = %v", err)
	}
	if reopened.activePath != box.activePath {
		t.Fatalf("activePath = %q, want %q (resume same chunk)", reopened.activePath, box.activePath)
	}
	if reopened.activeSize != box.activeSize {
		t.Fatalf("activeSize = %d, want %d", reopened.activeSize, box.activeSize)
	}
}

func readOutboxLines(t *testing.T, path string) []outboxLine {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = file.Close() }()

	lines := make([]outboxLine, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line outboxLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error = %v", err)
	}
	return lines
}
