package timetrack

import (
	"net/http"
	"strconv"
	"strings"

	"megaapp-back/internal/auth"
	"megaapp-back/internal/httpx/legacy"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func RegisterRoutes(router chi.Router, authService *auth.Service, handler *Handler) {
	router.Route("/api/time", func(r chi.Router) {
		r.Use(auth.Middleware(authService))

		r.Get("/activities", handler.GetActivities)
		r.Post("/activities", handler.CreateActivity)
		r.Put("/activities/{id}", handler.UpdateActivity)
		r.Get("/activities/recent", handler.GetRecentActivities)

		r.Get("/categories", handler.GetCategories)
		r.Post("/categories", handler.CreateCategory)
		r.Put("/categories/{id}", handler.UpdateCategory)
		r.Delete("/categories/{id}", handler.DeleteCategory)

		r.Get("/entries", handler.GetEntries)
		r.Post("/entries", handler.CreateEntry)
		r.Put("/entries/{id}", handler.UpdateEntry)
		r.Delete("/entries/{id}", handler.DeleteEntry)
	})
}

func (h *Handler) GetActivities(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var isArchivedFilter *bool
	if raw := strings.TrimSpace(r.URL.Query().Get("isArchived")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid isArchived")
			return
		}
		isArchivedFilter = &parsed
	}

	response, err := h.service.GetActivities(r.Context(), claims.UserID, isArchivedFilter)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to get activities")
		return
	}

	writeSuccessData(w, http.StatusOK, response)
}

func (h *Handler) CreateActivity(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var request ActivityInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	createdID, err := h.service.CreateActivity(r.Context(), claims.UserID, request)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to create activity")
		return
	}

	writeSuccessData(w, http.StatusCreated, map[string]int64{"id": createdID})
}

func (h *Handler) UpdateActivity(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	var request ActivityInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.service.UpdateActivity(r.Context(), claims.UserID, id, request); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to update activity")
		return
	}

	writeSuccessMessage(w, http.StatusOK, "Activity updated successfully")
}

func (h *Handler) GetRecentActivities(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var track *Track
	if raw := strings.TrimSpace(r.URL.Query().Get("track")); raw != "" {
		parsed := Track(raw)
		track = &parsed
	}

	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = parsed
	}

	response, err := h.service.GetRecentActivityIDs(r.Context(), claims.UserID, track, limit)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to get recent activities")
		return
	}

	writeSuccessData(w, http.StatusOK, response)
}

func (h *Handler) GetCategories(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	response, err := h.service.GetCategories(r.Context(), claims.UserID)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to get categories")
		return
	}

	writeSuccessData(w, http.StatusOK, response)
}

func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var request CategoryInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	createdID, err := h.service.CreateCategory(r.Context(), claims.UserID, request)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to create category")
		return
	}

	writeSuccessData(w, http.StatusCreated, map[string]int64{"id": createdID})
}

func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	var request CategoryInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.service.UpdateCategory(r.Context(), claims.UserID, id, request); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to update category")
		return
	}

	writeSuccessMessage(w, http.StatusOK, "Category updated successfully")
}

func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	if err := h.service.DeleteCategory(r.Context(), claims.UserID, id); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to delete category")
		return
	}

	writeSuccessMessage(w, http.StatusOK, "Category deleted successfully")
}

func (h *Handler) GetEntries(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var start *string
	if raw := strings.TrimSpace(r.URL.Query().Get("start")); raw != "" {
		start = &raw
	}

	response, err := h.service.GetEntries(r.Context(), claims.UserID, start)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to get entries")
		return
	}

	writeSuccessData(w, http.StatusOK, response)
}

func (h *Handler) CreateEntry(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var request EntryInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	createdID, err := h.service.CreateEntry(r.Context(), claims.UserID, request)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to create entry")
		return
	}

	writeSuccessData(w, http.StatusCreated, map[string]int64{"id": createdID})
}

func (h *Handler) UpdateEntry(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	var request EntryInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.service.UpdateEntry(r.Context(), claims.UserID, id, request); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to update entry")
		return
	}

	writeSuccessMessage(w, http.StatusOK, "Entry updated successfully")
}

func (h *Handler) DeleteEntry(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	if err := h.service.DeleteEntry(r.Context(), claims.UserID, id); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to delete entry")
		return
	}

	writeSuccessMessage(w, http.StatusOK, "Entry deleted successfully")
}

func parseIDParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "Invalid id")
		return 0, false
	}
	return id, true
}

func writeSuccessData(w http.ResponseWriter, statusCode int, data any) {
	legacy.WriteJSON(w, statusCode, map[string]any{"success": true, "data": data})
}

func writeSuccessMessage(w http.ResponseWriter, statusCode int, message string) {
	legacy.WriteJSON(w, statusCode, map[string]any{"success": true, "message": message})
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	legacy.WriteJSON(w, statusCode, map[string]any{"success": false, "error": message})
}

func writeAppError(w http.ResponseWriter, err error, fallbackStatus int, fallbackMessage string) {
	writeError(w, legacy.ErrorStatusCodeOf(err, fallbackStatus), legacy.ErrorMessageOf(err, fallbackMessage))
}
