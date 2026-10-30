package jobs

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeRunsScheduledJob(t *testing.T) {
	runtime := NewRuntime(slog.New(slog.DiscardHandler))
	defer func() { _ = runtime.Close() }()

	triggered := make(chan struct{}, 1)
	var runs atomic.Int64
	if err := runtime.Register("quotes", "@every 1s", func(context.Context) error {
		runs.Add(1)
		select {
		case triggered <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	runtime.Start()

	select {
	case <-triggered:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled job did not run")
	}

	if runs.Load() == 0 {
		t.Fatal("runs = 0, want > 0")
	}
}

func TestRuntimeRejectsInvalidSchedule(t *testing.T) {
	runtime := NewRuntime(slog.New(slog.DiscardHandler))
	defer func() { _ = runtime.Close() }()

	if err := runtime.Register("quotes", "bad schedule", func(context.Context) error { return nil }); err == nil {
		t.Fatal("Register() error = nil, want error")
	}
}
