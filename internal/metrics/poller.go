package metrics

import (
	"context"
	"log/slog"
	"sort"
	"time"

	clockplatform "megaapp-back/internal/platform/clock"
)

type realtimeBroadcaster interface {
	BroadcastDetail(DetailUpdate)
	BroadcastLatest(adminUserIDs []int64, snapshot LatestSnapshot)
}

type Poller struct {
	client      *FlatlineClient
	realtime    realtimeBroadcaster
	adminLister AdminLister
	interval    time.Duration
	maxCatchUp  time.Duration
	clock       clockplatform.Clock
	logger      *slog.Logger

	cursor     int64
	latest     map[string]map[string]float64
	lastBucket map[string]int64
	seenBucket map[string]int64

	cancel context.CancelFunc
	done   chan struct{}
}

func NewPoller(client *FlatlineClient, realtime realtimeBroadcaster, adminLister AdminLister, interval time.Duration, initialLookback time.Duration, maxCatchUp time.Duration, clk clockplatform.Clock, logger *slog.Logger) *Poller {
	return &Poller{
		client:      client,
		realtime:    realtime,
		adminLister: adminLister,
		interval:    interval,
		maxCatchUp:  maxCatchUp,
		clock:       clk,
		logger:      logger,
		cursor:      initialPollerCursor(clk.Now(), initialLookback),
		latest:      make(map[string]map[string]float64),
		lastBucket:  make(map[string]int64),
		seenBucket:  make(map[string]int64),
	}
}

func initialPollerCursor(now time.Time, lookback time.Duration) int64 {
	return now.Truncate(time.Minute).Add(-lookback).Unix()
}

func (p *Poller) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})

	go func() {
		defer close(p.done)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.tick(ctx)
			}
		}
	}()
}

func (p *Poller) Close() error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		<-p.done
	}
	return nil
}

func (p *Poller) tick(ctx context.Context) {
	// Bounds how far back a stale cursor (e.g. after a Flatline outage) can reach in a
	// single tick, so a long gap can't push one oversized METRICS_UPDATE over the shared
	// WebSocket connection — see plans/15, "Ход выполнения (2026-07-31)". Anything older
	// than this window is left to the client's own REST /api/metrics/history catch-up.
	catchUpFloor := p.clock.Now().Add(-p.maxCatchUp).Unix()

	points, err := p.client.Since(ctx, p.cursor, catchUpFloor, catchUpFloor, catchUpFloor)
	if err != nil {
		p.logger.Warn("metrics_poll_failed", "err", err)
		return
	}
	if len(points) == 0 {
		return
	}

	// Flatline's Since() compares against bucket+step (not bucket alone) so a
	// late-closing candle stays visible across the cursor boundary — see
	// METRICS-GRANULARITY._implementation-plan.md, section 3. Side effect:
	// the most recently closed bucket of any series satisfies that condition
	// on every single tick until a newer bucket appears, so Since() keeps
	// re-returning it unchanged. Track per-series freshness here and only
	// broadcast points actually new since the last tick, instead of
	// re-pushing identical data to every subscriber every poll interval.
	newPoints := make([]MetricPoint, 0, len(points))
	for _, point := range points {
		if point.Bucket > p.cursor {
			p.cursor = point.Bucket
		}

		seenKey := point.Service + "\x00" + point.Name + "\x00" + point.Granularity
		if point.Bucket > p.seenBucket[seenKey] {
			p.seenBucket[seenKey] = point.Bucket
			newPoints = append(newPoints, point)
		}

		serviceMetrics, ok := p.latest[point.Service]
		if !ok {
			serviceMetrics = make(map[string]float64)
			p.latest[point.Service] = serviceMetrics
		}
		serviceMetrics[point.Name] = point.Value

		if point.Bucket > p.lastBucket[point.Service] {
			p.lastBucket[point.Service] = point.Bucket
		}
	}

	if len(newPoints) > 0 {
		p.realtime.BroadcastDetail(DetailUpdate{Points: newPoints})
	}

	adminUserIDs, err := p.adminLister.ListAdminUserIDs(ctx)
	if err != nil {
		p.logger.Warn("metrics_admin_lookup_failed", "err", err)
		return
	}
	p.realtime.BroadcastLatest(adminUserIDs, p.buildLatestSnapshot())
}

func (p *Poller) buildLatestSnapshot() LatestSnapshot {
	services := make([]ServiceLatest, 0, len(p.latest))
	for service, metrics := range p.latest {
		services = append(services, ServiceLatest{
			Service:    service,
			LastBucket: p.lastBucket[service],
			Metrics:    metrics,
		})
	}
	sort.Slice(services, func(i, j int) bool {
		return services[i].Service < services[j].Service
	})
	return LatestSnapshot{Services: services}
}
