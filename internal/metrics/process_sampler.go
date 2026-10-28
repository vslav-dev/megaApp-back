package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	clockplatform "megaapp-back/internal/platform/clock"
)

const processSampleInterval = 5 * time.Second

// ProcessSampler measures this process's own CPU/RSS usage — same method
// already proven in production by Flatline's HardwareCollector
// (flatline/internal/metrics/hardware_sampler.go): delta of /proc/self/stat
// ticks over delta of /proc/stat ticks every 5s, VmRSS from
// /proc/self/status. Runs on its own timer, independent of the once-a-minute
// business metrics cron — the cron pulls whatever aggregate closed via
// TakeCompleted. See plans/29-process-self-metrics.design-doc.md.
type ProcessSampler struct {
	clock  clockplatform.Clock
	logger *slog.Logger

	currentBucket int64
	aggregate     processAggregate
	prevSample    *processResourceSample

	mu              sync.Mutex
	completed       map[string]float64
	completedBucket int64

	cancel context.CancelFunc
	done   chan struct{}
}

type processResourceSample struct {
	totalCPUTicks   float64
	processCPUTicks float64
	rssBytes        float64
}

type processAggregate struct {
	cpuRatio  sampledMetric
	rssBytes  float64
	hasLatest bool
}

type sampledMetric struct {
	sum   float64
	count int
	max   float64
}

func NewProcessSampler(clock clockplatform.Clock, logger *slog.Logger) (*ProcessSampler, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("process metrics require linux")
	}
	return &ProcessSampler{clock: clock, logger: logger}, nil
}

func (s *ProcessSampler) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)
		s.sample()

		ticker := time.NewTicker(processSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sample()
			}
		}
	}()
}

// Close is safe to call on a nil *ProcessSampler — process metrics are
// disabled on non-linux dev machines (see NewProcessSampler), and cleanup
// paths in app.go call Close unconditionally alongside the other backgrounds.
func (s *ProcessSampler) Close() error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
	return nil
}

func (s *ProcessSampler) sample() {
	sample, err := readProcessResourceSample()
	if err != nil {
		s.logger.Warn("process_metrics_sample_failed", "err", err)
		return
	}

	bucket := s.clock.Now().UTC().Truncate(time.Minute).Unix()
	s.advanceBucket(bucket)

	if s.prevSample != nil {
		s.aggregate.addDelta(*s.prevSample, sample)
	}
	s.aggregate.setLatest(sample)
	s.prevSample = &sample
}

func (s *ProcessSampler) advanceBucket(bucket int64) {
	if s.currentBucket == 0 {
		s.currentBucket = bucket
		return
	}
	if bucket == s.currentBucket {
		return
	}

	s.publish(s.currentBucket, s.aggregate.metrics())
	s.currentBucket = bucket
	s.aggregate = processAggregate{}
}

func (s *ProcessSampler) publish(bucket int64, metrics map[string]float64) {
	if len(metrics) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = metrics
	s.completedBucket = bucket
}

// TakeCompleted returns the metric points of the most recently closed minute
// bucket under the given service, or nil if nothing has closed since the
// last call.
func (s *ProcessSampler) TakeCompleted(service string) []MetricPoint {
	s.mu.Lock()
	metrics := s.completed
	bucket := s.completedBucket
	s.completed = nil
	s.mu.Unlock()

	if len(metrics) == 0 {
		return nil
	}

	points := make([]MetricPoint, 0, len(metrics))
	for name, value := range metrics {
		points = append(points, MetricPoint{Service: service, Name: name, Granularity: GranularityMinute, Bucket: bucket, Value: value})
	}
	return points
}

func (a *processAggregate) addDelta(prev, current processResourceSample) {
	totalDelta := current.totalCPUTicks - prev.totalCPUTicks
	if totalDelta <= 0 {
		return
	}
	processDelta := current.processCPUTicks - prev.processCPUTicks
	if processDelta < 0 {
		return
	}
	a.cpuRatio.add(processDelta / totalDelta)
}

func (a *processAggregate) setLatest(sample processResourceSample) {
	a.rssBytes = sample.rssBytes
	a.hasLatest = true
}

func (a *processAggregate) metrics() map[string]float64 {
	metrics := make(map[string]float64)
	a.cpuRatio.write(metrics, "process_cpu_ratio")
	if a.hasLatest {
		metrics["process_rss_bytes"] = a.rssBytes
	}
	if len(metrics) == 0 {
		return nil
	}
	return metrics
}

func (m *sampledMetric) add(value float64) {
	if m.count == 0 || value > m.max {
		m.max = value
	}
	m.sum += value
	m.count++
}

func (m sampledMetric) write(dst map[string]float64, prefix string) {
	if m.count == 0 {
		return
	}
	dst[prefix+"_avg"] = m.sum / float64(m.count)
	dst[prefix+"_max"] = m.max
}

func readProcessResourceSample() (processResourceSample, error) {
	totalTicks, err := readTotalCPUTicks()
	if err != nil {
		return processResourceSample{}, err
	}
	processTicks, err := readProcessCPUTicks()
	if err != nil {
		return processResourceSample{}, err
	}
	rssBytes, err := readProcessRSSBytes()
	if err != nil {
		return processResourceSample{}, err
	}
	return processResourceSample{totalCPUTicks: totalTicks, processCPUTicks: processTicks, rssBytes: rssBytes}, nil
}

func readTotalCPUTicks() (float64, error) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, fmt.Errorf("read /proc/stat: %w", err)
	}
	return parseTotalCPUTicks(string(raw))
}

func parseTotalCPUTicks(raw string) (float64, error) {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "cpu" {
			continue
		}

		var total float64
		for _, field := range fields[1:] {
			value, err := strconv.ParseFloat(field, 64)
			if err != nil {
				return 0, fmt.Errorf("parse cpu field %q: %w", field, err)
			}
			total += value
		}
		return total, nil
	}
	return 0, fmt.Errorf("cpu line not found")
}

func readProcessCPUTicks() (float64, error) {
	raw, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, fmt.Errorf("read /proc/self/stat: %w", err)
	}
	return parseProcessCPUTicks(string(raw))
}

func parseProcessCPUTicks(raw string) (float64, error) {
	closeIndex := strings.LastIndex(raw, ")")
	if closeIndex == -1 {
		return 0, fmt.Errorf("process stat missing command")
	}

	fields := strings.Fields(raw[closeIndex+1:])
	if len(fields) < 13 {
		return 0, fmt.Errorf("process stat has %d fields", len(fields))
	}

	utime, err := strconv.ParseFloat(fields[11], 64)
	if err != nil {
		return 0, fmt.Errorf("parse process utime: %w", err)
	}
	stime, err := strconv.ParseFloat(fields[12], 64)
	if err != nil {
		return 0, fmt.Errorf("parse process stime: %w", err)
	}
	return utime + stime, nil
}

func readProcessRSSBytes() (float64, error) {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, fmt.Errorf("read /proc/self/status: %w", err)
	}
	return parseProcessRSSBytes(string(raw))
}

func parseProcessRSSBytes(raw string) (float64, error) {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "VmRSS:" {
			continue
		}
		valueKB, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return 0, fmt.Errorf("parse process rss: %w", err)
		}
		return valueKB * 1024, nil
	}
	return 0, fmt.Errorf("VmRSS not found")
}
