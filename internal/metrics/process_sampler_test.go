package metrics

import (
	"log/slog"
	"math"
	"testing"
	"time"
)

func TestProcessSamplerPublishesClosedBucket(t *testing.T) {
	clock := &stubProcessClock{now: time.Date(2026, 6, 26, 12, 34, 10, 0, time.UTC)}
	sampler := &ProcessSampler{clock: clock, logger: slog.New(slog.NewTextHandler(discardWriter{}, nil))}

	sampler.applySample(processResourceSample{totalCPUTicks: 100, processCPUTicks: 10, rssBytes: 2_000})
	clock.now = time.Date(2026, 6, 26, 12, 34, 15, 0, time.UTC)
	sampler.applySample(processResourceSample{totalCPUTicks: 120, processCPUTicks: 12, rssBytes: 3_000})
	clock.now = time.Date(2026, 6, 26, 12, 35, 5, 0, time.UTC)
	sampler.applySample(processResourceSample{totalCPUTicks: 140, processCPUTicks: 14, rssBytes: 4_000})

	points := sampler.TakeCompleted("megaapp")
	values := make(map[string]float64)
	for _, point := range points {
		if point.Service != "megaapp" {
			t.Fatalf("point service = %q, want %q", point.Service, "megaapp")
		}
		if point.Bucket != time.Date(2026, 6, 26, 12, 34, 0, 0, time.UTC).Unix() {
			t.Fatalf("point bucket = %d, want closed-minute bucket", point.Bucket)
		}
		values[point.Name] = point.Value
	}

	assertFloatEqualProcess(t, values["process_cpu_ratio_avg"], 0.1)
	assertFloatEqualProcess(t, values["process_cpu_ratio_max"], 0.1)
	assertFloatEqualProcess(t, values["process_rss_bytes"], 3_000)
}

func TestProcessSamplerTakeCompletedDrainsOnce(t *testing.T) {
	clock := &stubProcessClock{now: time.Date(2026, 6, 26, 12, 34, 10, 0, time.UTC)}
	sampler := &ProcessSampler{clock: clock, logger: slog.New(slog.NewTextHandler(discardWriter{}, nil))}

	sampler.applySample(processResourceSample{totalCPUTicks: 100, processCPUTicks: 10, rssBytes: 2_000})
	clock.now = time.Date(2026, 6, 26, 12, 35, 0, 0, time.UTC)
	sampler.applySample(processResourceSample{totalCPUTicks: 120, processCPUTicks: 12, rssBytes: 3_000})

	if points := sampler.TakeCompleted("megaapp"); len(points) == 0 {
		t.Fatalf("expected points on first TakeCompleted call")
	}
	if points := sampler.TakeCompleted("megaapp"); len(points) != 0 {
		t.Fatalf("expected no points on second TakeCompleted call, got %d", len(points))
	}
}

func TestProcessAggregateAddDeltaIgnoresNonPositiveTotalDelta(t *testing.T) {
	var aggregate processAggregate
	aggregate.addDelta(
		processResourceSample{totalCPUTicks: 100, processCPUTicks: 10},
		processResourceSample{totalCPUTicks: 100, processCPUTicks: 12},
	)
	if aggregate.cpuRatio.count != 0 {
		t.Fatalf("expected no sample recorded for zero total delta")
	}
}

func TestParseTotalCPUTicks(t *testing.T) {
	total, err := parseTotalCPUTicks("cpu  100 20 30 50 10 5 5 2 0 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n")
	if err != nil {
		t.Fatalf("parseTotalCPUTicks() error = %v", err)
	}
	assertFloatEqualProcess(t, total, 222)
}

func TestParseProcessCPUTicks(t *testing.T) {
	value, err := parseProcessCPUTicks("123 (megaapp back) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17")
	if err != nil {
		t.Fatalf("parseProcessCPUTicks() error = %v", err)
	}
	assertFloatEqualProcess(t, value, 23)
}

func TestParseProcessRSSBytes(t *testing.T) {
	value, err := parseProcessRSSBytes("Name:\tmegaapp-back\nVmRSS:\t   2048 kB\n")
	if err != nil {
		t.Fatalf("parseProcessRSSBytes() error = %v", err)
	}
	assertFloatEqualProcess(t, value, 2_097_152)
}

// applySample bypasses /proc reads so bucket/aggregate logic can be tested
// with fixed inputs, mirroring what sample() does after readProcessResourceSample.
func (s *ProcessSampler) applySample(sample processResourceSample) {
	bucket := s.clock.Now().UTC().Truncate(time.Minute).Unix()
	s.advanceBucket(bucket)

	if s.prevSample != nil {
		s.aggregate.addDelta(*s.prevSample, sample)
	}
	s.aggregate.setLatest(sample)
	s.prevSample = &sample
}

type stubProcessClock struct {
	now time.Time
}

func (c *stubProcessClock) Now() time.Time {
	return c.now
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func assertFloatEqualProcess(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("got %v, want %v", got, want)
	}
}
