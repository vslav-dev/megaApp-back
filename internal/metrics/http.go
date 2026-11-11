package metrics

import (
	"io"
	"net/http"

	"megaapp-back/internal/auth"
	"megaapp-back/internal/httpx/legacy"

	"github.com/go-chi/chi/v5"
)

type HistoryHandler struct {
	service *Service
	client  *FlatlineClient
}

func NewHistoryHandler(service *Service, client *FlatlineClient) *HistoryHandler {
	return &HistoryHandler{service: service, client: client}
}

func RegisterRoutes(router chi.Router, authService *auth.Service, handler *HistoryHandler) {
	router.Route("/api/metrics", func(r chi.Router) {
		r.Use(auth.Middleware(authService))
		r.Post("/history", handler.History)
	})
}

// historyRequest is a POST body, not query params — scope (per-service
// metric-name lists) doesn't fit cleanly on a query string.
type historyRequest struct {
	MinuteSince int64        `json:"minuteSince"`
	HourSince   int64        `json:"hourSince"`
	DaySince    int64        `json:"daySince"`
	Scope       []ScopeEntry `json:"scope"`
}

func (h *HistoryHandler) History(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		legacy.WriteDetail(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	isAdmin, err := h.service.IsAdmin(r.Context(), claims.UserID)
	if err != nil {
		legacy.WriteDetail(w, http.StatusInternalServerError, "Failed to check permissions")
		return
	}
	if !isAdmin {
		legacy.WriteDetail(w, http.StatusForbidden, "Forbidden")
		return
	}

	var request historyRequest
	if err := legacy.DecodeJSON(r, &request); err != nil {
		legacy.WriteDetail(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if request.MinuteSince <= 0 || request.HourSince <= 0 || request.DaySince <= 0 {
		legacy.WriteDetail(w, http.StatusBadRequest, "Invalid minuteSince/hourSince/daySince")
		return
	}
	// Megaapp-back doesn't transform scope — it validates the same contract
	// Flatline will validate again, and forwards the list unchanged.
	if _, err := buildScopeSet(request.Scope); err != nil {
		legacy.WriteDetail(w, http.StatusBadRequest, "Invalid metrics scope")
		return
	}

	resp, err := h.client.History(r.Context(), request.MinuteSince, request.HourSince, request.DaySince, request.Scope)
	if err != nil {
		legacy.WriteDetail(w, http.StatusBadGateway, "Failed to load metrics history")
		return
	}
	defer resp.Body.Close()

	// Streamed through unread — the body is Flatline's binary wire format,
	// meant for the browser to decode directly, not for megaapp-back.
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		w.Header().Set("Content-Length", contentLength)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}
