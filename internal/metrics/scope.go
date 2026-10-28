package metrics

import (
	"errors"
	"strings"
)

// ScopeEntry names the metrics of one service a request wants — REST history
// and WS METRICS_SUBSCRIBE share this shape. MetricNames is always non-empty:
// there is no "whole service" wildcard, see
// plans/32-metrics-history-scope-filter.implementation-plan.md §3.1.
type ScopeEntry struct {
	Service     string   `json:"service"`
	MetricNames []string `json:"metricNames"`
}

// scopeKey mirrors Flatline's metricScopeKey — a (service, metric) pair
// independent of granularity. Copied, not imported: these two services don't
// share Go code, only the wire contract, matching the "copy architecture, not
// code" convention already used for producers — see
// flatline/plans/00-FLATLINE-ARCHITECTURE.design-doc.md §3.3a.
type scopeKey struct {
	service    string
	metricName string
}

var ErrInvalidScope = errors.New("invalid metrics scope")

func buildScopeSet(scope []ScopeEntry) (map[scopeKey]struct{}, error) {
	if len(scope) == 0 {
		return nil, ErrInvalidScope
	}

	keys := make(map[scopeKey]struct{})
	for _, entry := range scope {
		service := strings.TrimSpace(entry.Service)
		if service == "" || len(entry.MetricNames) == 0 {
			return nil, ErrInvalidScope
		}
		for _, name := range entry.MetricNames {
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, ErrInvalidScope
			}
			keys[scopeKey{service: service, metricName: name}] = struct{}{}
		}
	}
	return keys, nil
}
