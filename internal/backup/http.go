package backup

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"

	"megaapp-back/internal/httpx/legacy"

	"github.com/go-chi/chi/v5"
)

type DebugHandler struct {
	service *Service
	logger  *slog.Logger
}

func NewDebugHandler(service *Service, logger *slog.Logger) *DebugHandler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &DebugHandler{service: service, logger: logger}
}

func RegisterDebugRoutes(router chi.Router, handler *DebugHandler) {
	if handler == nil {
		return
	}
	router.Post("/api/debug/run-backup-job", handler.RunBackupJob)
}

func (h *DebugHandler) RunBackupJob(w http.ResponseWriter, r *http.Request) {
	jobID := rand.Text()
	h.logger.Info("backup_job_started", "jobId", jobID)
	go h.runInBackground(jobID)
	legacy.WriteJSON(w, http.StatusAccepted, map[string]any{
		"jobId": jobID,
	})
}

func (h *DebugHandler) runInBackground(jobID string) {
	if _, err := h.service.Run(context.Background()); err != nil {
		h.logger.Error("backup_job_failed", "jobId", jobID, "error", err)
	}
}
