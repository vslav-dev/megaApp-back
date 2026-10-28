package metrics

import (
	"context"
	"sync"
	"time"

	clockplatform "megaapp-back/internal/platform/clock"
)

const MainServiceName = "megaapp"

const (
	GranularityMinute = "minute"
	GranularityHour   = "hour"
	GranularityDay    = "day"
)

type MetricPoint struct {
	Service     string  `json:"service"`
	Name        string  `json:"name"`
	Granularity string  `json:"granularity"`
	Bucket      int64   `json:"bucket"`
	Value       float64 `json:"value"`
}

type AdminLister interface {
	ListAdminUserIDs(ctx context.Context) ([]int64, error)
}

type Service struct {
	serviceName string
	clock       clockplatform.Clock
	adminLister AdminLister

	mu     sync.Mutex
	counts map[string]int64
}

func NewService(serviceName string, clock clockplatform.Clock, adminLister AdminLister) *Service {
	return &Service{
		serviceName: serviceName,
		clock:       clock,
		adminLister: adminLister,
		counts:      make(map[string]int64),
	}
}

func (s *Service) Increment(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[name]++
}

// Flush is a pure in-memory operation — counters never touch a local DB
// anymore, the closed bucket's points are handed to an Exporter by the caller.
func (s *Service) Flush() []MetricPoint {
	s.mu.Lock()
	pending := s.counts
	s.counts = make(map[string]int64)
	s.mu.Unlock()

	if len(pending) == 0 {
		return nil
	}

	bucket := previousMinuteBucket(s.clock.Now())
	points := make([]MetricPoint, 0, len(pending))
	for name, delta := range pending {
		points = append(points, MetricPoint{Service: s.serviceName, Name: name, Granularity: GranularityMinute, Bucket: bucket, Value: float64(delta)})
	}
	return points
}

func (s *Service) IsAdmin(ctx context.Context, userID int64) (bool, error) {
	adminUserIDs, err := s.adminLister.ListAdminUserIDs(ctx)
	if err != nil {
		return false, err
	}
	for _, adminUserID := range adminUserIDs {
		if adminUserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) AdminUserIDs(ctx context.Context) ([]int64, error) {
	return s.adminLister.ListAdminUserIDs(ctx)
}

func previousMinuteBucket(now time.Time) int64 {
	return now.Truncate(time.Minute).Add(-time.Minute).Unix()
}
