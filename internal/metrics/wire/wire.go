// Package wire implements the compact binary layout shared with Flatline's
// /api/metrics/history and /api/metrics/since, and reused here for
// METRICS_UPDATE/METRICS_LATEST over WS — see
// plans/33-metrics-flow-tstorage-migration.implementation-plan.md §2.4.
//
// This byte layout mirrors flatline/internal/metrics/wire/wire.go exactly.
// The two packages live in separate repos/modules and can't share Go code —
// kept in sync by hand when the format changes, same as any other part of
// the cross-repo contract documented in that plan.
//
// Layout (all integers little-endian):
//
//	byte    formatVersion
//	uint32  seriesCount
//	uint32  totalPoints
//	-- seriesCount series headers --
//	  uint16 serviceLen;    byte[serviceLen]    service (utf8)
//	  uint16 metricNameLen; byte[metricNameLen] metricName (utf8)
//	  byte   granularity
//	  uint32 pointCount
//	-- padding to the next 8-byte boundary --
//	-- totalPoints int64 bucket timestamps, grouped by series in header order --
//	-- totalPoints float64 values, grouped by series in header order --
//
// Bucket and value regions start 8-byte aligned, and every series' slice
// within them stays 8-byte aligned (pointCount*8 is always a multiple of 8),
// so a browser can read them directly as BigInt64Array/Float64Array views
// over the same ArrayBuffer without copying.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

type Granularity byte

const (
	GranularityMinute Granularity = 0
	GranularityHour   Granularity = 1
	GranularityDay    Granularity = 2
)

const FormatVersion byte = 1

type Point struct {
	Bucket int64
	Value  float64
}

// Series holds all points for one (service, metricName, granularity) tuple.
// Points must be ordered by Bucket ascending.
type Series struct {
	Service     string
	MetricName  string
	Granularity Granularity
	Points      []Point
}

var ErrMalformed = errors.New("malformed wire payload")

func Encode(series []Series) []byte {
	totalPoints := 0
	headerLen := 1 + 4 + 4
	for _, s := range series {
		headerLen += 2 + len(s.Service) + 2 + len(s.MetricName) + 1 + 4
		totalPoints += len(s.Points)
	}
	dataStart := align8(headerLen)
	bucketsStart := dataStart
	valuesStart := bucketsStart + totalPoints*8
	buf := make([]byte, valuesStart+totalPoints*8)

	buf[0] = FormatVersion
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(series)))
	binary.LittleEndian.PutUint32(buf[5:9], uint32(totalPoints))

	offset := 9
	bucketOffset := bucketsStart
	valueOffset := valuesStart
	for _, s := range series {
		offset = putString(buf, offset, s.Service)
		offset = putString(buf, offset, s.MetricName)
		buf[offset] = byte(s.Granularity)
		offset++
		binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(len(s.Points)))
		offset += 4

		for _, p := range s.Points {
			binary.LittleEndian.PutUint64(buf[bucketOffset:bucketOffset+8], uint64(p.Bucket))
			bucketOffset += 8
			binary.LittleEndian.PutUint64(buf[valueOffset:valueOffset+8], math.Float64bits(p.Value))
			valueOffset += 8
		}
	}

	return buf
}

func Decode(data []byte) ([]Series, error) {
	if len(data) < 9 {
		return nil, ErrMalformed
	}
	if data[0] != FormatVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrMalformed, data[0])
	}
	seriesCount := binary.LittleEndian.Uint32(data[1:5])
	totalPoints := binary.LittleEndian.Uint32(data[5:9])

	series := make([]Series, seriesCount)
	offset := 9
	pointCounts := make([]uint32, seriesCount)
	for i := range series {
		service, next, err := getString(data, offset)
		if err != nil {
			return nil, err
		}
		offset = next
		metricName, next, err := getString(data, offset)
		if err != nil {
			return nil, err
		}
		offset = next
		if offset+5 > len(data) {
			return nil, ErrMalformed
		}
		series[i].Service = service
		series[i].MetricName = metricName
		series[i].Granularity = Granularity(data[offset])
		offset++
		pointCounts[i] = binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
	}

	dataStart := align8(offset)
	bucketsStart := dataStart
	valuesStart := bucketsStart + int(totalPoints)*8
	if valuesStart+int(totalPoints)*8 > len(data) {
		return nil, ErrMalformed
	}

	bucketOffset := bucketsStart
	valueOffset := valuesStart
	for i := range series {
		points := make([]Point, pointCounts[i])
		for j := range points {
			points[j].Bucket = int64(binary.LittleEndian.Uint64(data[bucketOffset : bucketOffset+8]))
			bucketOffset += 8
			points[j].Value = math.Float64frombits(binary.LittleEndian.Uint64(data[valueOffset : valueOffset+8]))
			valueOffset += 8
		}
		series[i].Points = points
	}

	return series, nil
}

func putString(buf []byte, offset int, s string) int {
	binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(s)))
	offset += 2
	copy(buf[offset:offset+len(s)], s)
	return offset + len(s)
}

func getString(data []byte, offset int) (string, int, error) {
	if offset+2 > len(data) {
		return "", 0, ErrMalformed
	}
	length := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
	offset += 2
	if offset+length > len(data) {
		return "", 0, ErrMalformed
	}
	return string(data[offset : offset+length]), offset + length, nil
}

func align8(offset int) int {
	if remainder := offset % 8; remainder != 0 {
		return offset + (8 - remainder)
	}
	return offset
}
