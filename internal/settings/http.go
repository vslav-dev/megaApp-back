package settings

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"megaapp-back/internal/auth"
	"megaapp-back/internal/httpx/legacy"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service  *Service
	realtime *WSRealtimePublisher
}

func NewHandler(service *Service, realtime *WSRealtimePublisher) *Handler {
	return &Handler{service: service, realtime: realtime}
}

func RegisterRoutes(router chi.Router, authService *auth.Service, handler *Handler) {
	router.Route("/api/settings", func(r chi.Router) {
		r.Use(auth.Middleware(authService))
		r.Get("/{namespace}", handler.Get)
		r.Put("/{namespace}", handler.Put)
	})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		legacy.WriteMessage(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	namespace := chi.URLParam(r, "namespace")
	if !IsValidNamespace(namespace) {
		legacy.WriteMessage(w, http.StatusNotFound, "Unknown settings namespace")
		return
	}

	response, err := h.service.GetWithProfile(r.Context(), claims.UserID, namespace, claims.Username)
	if err != nil {
		legacy.WriteAppMessageError(w, err, http.StatusInternalServerError, "Internal server error")
		return
	}

	legacy.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		legacy.WriteMessage(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	namespace := chi.URLParam(r, "namespace")
	if !IsValidNamespace(namespace) {
		legacy.WriteMessage(w, http.StatusNotFound, "Unknown settings namespace")
		return
	}

	var request map[string]json.RawMessage
	if err := legacy.DecodeJSON(r, &request); err != nil {
		legacy.WriteAppMessageError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	var operationID string
	if raw, ok := request["operationId"]; ok {
		_ = json.Unmarshal(raw, &operationID)
	}
	delete(request, "operationId")
	if operationID == "" {
		legacy.WriteMessage(w, http.StatusBadRequest, "operationId is required")
		return
	}

	applied, updatedAtMillis, err := h.service.Put(r.Context(), claims.UserID, namespace, operationID, request)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidNamespace), errors.Is(err, ErrInvalidSettingPayload):
			legacy.WriteAppMessageError(w, err, http.StatusBadRequest, err.Error())
		default:
			legacy.WriteAppMessageError(w, err, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	legacy.WriteMessage(w, http.StatusOK, "Setting updated successfully")

	if applied {
		clientID := extractClientID(r)
		go h.realtime.PublishChanged(claims.UserID, namespace, request, clientID, updatedAtMillis)
	}
}

func extractClientID(r *http.Request) string {
	clientID := strings.TrimSpace(r.Header.Get("X-Client-ID"))
	if clientID == "" {
		clientID = strings.TrimSpace(r.Header.Get("X-Client-Id"))
	}
	return clientID
}
