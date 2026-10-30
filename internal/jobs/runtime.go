package jobs

import (
	"context"
	"log/slog"
	"time"

	logplatform "megaapp-back/internal/platform/log"

	"github.com/robfig/cron/v3"
)

type Runtime struct {
	logger *slog.Logger
	cron   *cron.Cron
}

func NewRuntime(logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Runtime{
		logger: logger,
		cron: cron.New(
			cron.WithLocation(time.UTC),
			cron.WithParser(cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow|cron.Descriptor)),
		),
	}
}

func (r *Runtime) Register(name string, schedule string, run func(context.Context) error) error {
	_, err := r.cron.AddFunc(schedule, func() {
		startedAt := time.Now().UTC()
		defer func() {
			if recovered := recover(); recovered != nil {
				r.logger.Error("job panic recovered", "job", name, "panic", recovered)
			}
		}()
		if err := run(context.Background()); err != nil {
			r.logger.Error("job failed", "job", name, "duration", logplatform.FormatDuration(time.Since(startedAt)), "error", err)
			return
		}
		r.logger.Info("job completed", "job", name, "duration", logplatform.FormatDuration(time.Since(startedAt)))
	})
	return err
}

func (r *Runtime) Start() {
	r.cron.Start()
}

func (r *Runtime) Close() error {
	ctx := r.cron.Stop()
	<-ctx.Done()
	return nil
}
