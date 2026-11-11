package wire

import "testing"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	series := []Series{
		{
			Service:     "spread-capture-bot-v3",
			MetricName:  "heartbeat",
			Granularity: GranularityMinute,
			Points:      []Point{{Bucket: 100, Value: 1}, {Bucket: 160, Value: 0}},
		},
		{
			Service:     "tg-transcriber-bot",
			MetricName:  "openrouter_cost_usd",
			Granularity: GranularityHour,
			Points:      []Point{{Bucket: 3600, Value: 0.42}},
		},
		{
			Service:     "empty-service",
			MetricName:  "no_points",
			Granularity: GranularityDay,
			Points:      nil,
		},
	}

	encoded := Encode(series)
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(decoded) != len(series) {
		t.Fatalf("Decode() series count = %d, want %d", len(decoded), len(series))
	}
	for i, want := range series {
		got := decoded[i]
		if got.Service != want.Service || got.MetricName != want.MetricName || got.Granularity != want.Granularity {
			t.Fatalf("series %d = %+v, want %+v", i, got, want)
		}
		if len(got.Points) != len(want.Points) {
			t.Fatalf("series %d points = %v, want %v", i, got.Points, want.Points)
		}
		for j, wantPoint := range want.Points {
			if got.Points[j] != wantPoint {
				t.Fatalf("series %d point %d = %+v, want %+v", i, j, got.Points[j], wantPoint)
			}
		}
	}
}

func TestEncodeEmptySeries(t *testing.T) {
	encoded := Encode(nil)
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(decoded) != 0 {
		t.Fatalf("Decode() = %v, want empty", decoded)
	}
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	encoded := Encode(nil)
	encoded[0] = FormatVersion + 1
	if _, err := Decode(encoded); err == nil {
		t.Fatal("Decode() error = nil, want error for unsupported version")
	}
}

func TestDecodeRejectsTruncatedPayload(t *testing.T) {
	encoded := Encode([]Series{{Service: "a", MetricName: "b", Points: []Point{{Bucket: 1, Value: 2}}}})
	if _, err := Decode(encoded[:len(encoded)-4]); err == nil {
		t.Fatal("Decode() error = nil, want error for truncated payload")
	}
}
