package metrics

import "testing"

func TestBuildScopeSet(t *testing.T) {
	tests := []struct {
		name    string
		scope   []ScopeEntry
		wantErr bool
		wantLen int
	}{
		{name: "nil scope", scope: nil, wantErr: true},
		{name: "empty scope", scope: []ScopeEntry{}, wantErr: true},
		{name: "empty metricNames", scope: []ScopeEntry{{Service: "bot", MetricNames: nil}}, wantErr: true},
		{name: "empty service", scope: []ScopeEntry{{Service: "", MetricNames: []string{"a"}}}, wantErr: true},
		{name: "blank service", scope: []ScopeEntry{{Service: "  ", MetricNames: []string{"a"}}}, wantErr: true},
		{name: "blank metric name", scope: []ScopeEntry{{Service: "bot", MetricNames: []string{" "}}}, wantErr: true},
		{
			name: "valid single entry",
			scope: []ScopeEntry{
				{Service: "bot", MetricNames: []string{"a", "b"}},
			},
			wantLen: 2,
		},
		{
			name: "valid multiple services dedupes overlap",
			scope: []ScopeEntry{
				{Service: "bot", MetricNames: []string{"a", "a"}},
				{Service: "other", MetricNames: []string{"a"}},
			},
			wantLen: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set, err := buildScopeSet(test.scope)
			if (err != nil) != test.wantErr {
				t.Fatalf("buildScopeSet(%+v) error = %v, wantErr %v", test.scope, err, test.wantErr)
			}
			if err != nil {
				return
			}
			if len(set) != test.wantLen {
				t.Fatalf("len(set) = %d, want %d", len(set), test.wantLen)
			}
		})
	}
}
