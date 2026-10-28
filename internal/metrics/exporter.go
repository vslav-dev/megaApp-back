package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

type ExporterConfig struct {
	Service    string
	NDJSONPath string
	AckPath    string
}

type Exporter struct {
	cfg    ExporterConfig
	client *FlatlineClient
	outbox *outbox
	logger *slog.Logger

	pending     []MinuteSnapshot
	unpersisted []MinuteSnapshot
	lastAcked   int64
}

type ackState struct {
	LastAckedMinuteBucket int64 `json:"lastAckedMinuteBucket"`
}

const exporterBatchSize = 25

func NewExporter(cfg ExporterConfig, client *FlatlineClient, logger *slog.Logger) (*Exporter, error) {
	box, err := prepareOutbox(cfg.NDJSONPath, outboxChunkLimitBytes)
	if err != nil {
		return nil, err
	}

	lastAcked, err := readAck(cfg.AckPath)
	if err != nil {
		return nil, err
	}
	pending, err := readPendingSnapshots(cfg.NDJSONPath, cfg.Service, lastAcked)
	if err != nil {
		return nil, err
	}

	return &Exporter{cfg: cfg, client: client, outbox: box, logger: logger, pending: pending, lastAcked: lastAcked}, nil
}

func (e *Exporter) FlushAndPush(ctx context.Context, bucket int64, points []MetricPoint) {
	if len(points) > 0 {
		metrics := make(map[string]float64, len(points))
		for _, point := range points {
			metrics[point.Name] = point.Value
		}
		e.unpersisted = append(e.unpersisted, MinuteSnapshot{MinuteBucket: bucket, Metrics: metrics})
	}

	e.persistUnpersisted()
	e.tryFlushPending(ctx)
}

func (e *Exporter) persistUnpersisted() {
	for len(e.unpersisted) > 0 {
		snapshot := e.unpersisted[0]
		if err := appendOutboxLine(e.outbox, e.cfg.Service, snapshot); err != nil {
			e.logger.Error("metrics_outbox_append_failed", "bucket", snapshot.MinuteBucket, "err", err)
			return
		}
		e.pending = append(e.pending, snapshot)
		e.unpersisted = e.unpersisted[1:]
	}
}

func (e *Exporter) tryFlushPending(ctx context.Context) {
	for len(e.pending) > 0 {
		batchSize := min(len(e.pending), exporterBatchSize)
		batch := e.pending[:batchSize]
		if err := e.client.PushSnapshots(ctx, e.cfg.Service, batch); err != nil {
			e.logger.Warn("metrics_push_failed", "pending", len(e.pending), "batch", batchSize, "err", err)
			return
		}

		lastAcked := batch[len(batch)-1].MinuteBucket
		if err := writeAck(e.cfg.AckPath, lastAcked); err != nil {
			e.logger.Error("metrics_ack_write_failed", "bucket", lastAcked, "err", err)
			return
		}

		e.lastAcked = lastAcked
		e.pending = e.pending[batchSize:]
	}
}

func readAck(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read metrics ack: %w", err)
	}

	var state ackState
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, fmt.Errorf("decode metrics ack: %w", err)
	}

	return state.LastAckedMinuteBucket, nil
}

func writeAck(path string, bucket int64) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create metrics ack dir: %w", err)
	}

	data, err := json.Marshal(ackState{LastAckedMinuteBucket: bucket})
	if err != nil {
		return fmt.Errorf("encode metrics ack: %w", err)
	}

	tempPath := path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open metrics ack temp: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return errors.Join(fmt.Errorf("write metrics ack temp: %w", err), file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync metrics ack temp: %w", err), file.Close())
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close metrics ack temp: %w", err)
	}

	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace metrics ack: %w", err)
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open metrics ack dir: %w", err)
	}
	if err := dirFile.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync metrics ack dir: %w", err), dirFile.Close())
	}
	if err := dirFile.Close(); err != nil {
		return fmt.Errorf("close metrics ack dir: %w", err)
	}

	return nil
}
