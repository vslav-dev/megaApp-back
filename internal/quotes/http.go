package quotes

import (
	"net/http"

	"megaapp-back/internal/httpx/legacy"

	"github.com/go-chi/chi/v5"
)

type DebugHandler struct {
	service *Service
}

func NewDebugHandler(service *Service) *DebugHandler {
	return &DebugHandler{service: service}
}

func RegisterDebugRoutes(router chi.Router, handler *DebugHandler) {
	if handler == nil {
		return
	}
	router.Get("/api/debug/run-quotes-job", handler.RunQuotesJob)
}

func (h *DebugHandler) RunQuotesJob(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Run(r.Context())
	if err != nil {
		legacy.WriteAppResultError(w, err, http.StatusInternalServerError, "Failed to run quotes job")
		return
	}
	legacy.WriteJSON(w, http.StatusOK, map[string]any{
		"result":        true,
		"upsertedCount": result.UpsertedCount,
		"fromISO":       result.FromISO,
		"toISO":         result.ToISO,
		"failures":      result.Failures,
		"degraded":      result.Degraded,
	})
}
