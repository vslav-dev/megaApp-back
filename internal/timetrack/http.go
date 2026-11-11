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

		r.Get("/catalog", handler.GetCatalog)

		r.Post("/activity-kinds", handler.CreateActivityKind)
		r.Put("/activity-kinds/{id}", handler.UpdateActivityKind)

		r.Post("/category-groups", handler.CreateCategoryGroup)
		r.Put("/category-groups/{id}", handler.UpdateCategoryGroup)
		r.Delete("/category-groups/{id}", handler.DeleteCategoryGroup)

		r.Post("/category-options", handler.CreateCategoryOption)
		r.Put("/category-options/{id}", handler.UpdateCategoryOption)
		r.Delete("/category-options/{id}", handler.DeleteCategoryOption)

		r.Get("/entries", handler.GetEntries)
		r.Post("/entries", handler.CreateEntry)
		r.Patch("/entries/{id}/time", handler.UpdateEntryTime)
		r.Patch("/entries/{id}/selection", handler.UpdateEntrySelection)
		r.Delete("/entries/{id}", handler.DeleteEntry)
	})
}

func (h *Handler) GetCatalog(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	response, err := h.service.GetCatalog(r.Context(), claims.UserID)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to get catalog")
		return
	}

	writeSuccessData(w, http.StatusOK, response)
}

func (h *Handler) CreateActivityKind(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var request ActivityKindInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	createdID, err := h.service.CreateActivityKind(r.Context(), claims.UserID, request)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to create activity kind")
		return
	}

	writeSuccessData(w, http.StatusCreated, map[string]int64{"id": createdID})
}

func (h *Handler) UpdateActivityKind(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var request ActivityKindInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.service.UpdateActivityKind(r.Context(), claims.UserID, id, request); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to update activity kind")
		return
	}

	writeSuccessMessage(w, "ActivityKind updated successfully")
}

func (h *Handler) CreateCategoryGroup(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var request CategoryGroupInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	createdID, err := h.service.CreateCategoryGroup(r.Context(), claims.UserID, request)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to create category group")
		return
	}

	writeSuccessData(w, http.StatusCreated, map[string]int64{"id": createdID})
}

func (h *Handler) UpdateCategoryGroup(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var request CategoryGroupInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.service.UpdateCategoryGroup(r.Context(), claims.UserID, id, request); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to update category group")
		return
	}

	writeSuccessMessage(w, "CategoryGroup updated successfully")
}

func (h *Handler) DeleteCategoryGroup(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	if err := h.service.DeleteCategoryGroup(r.Context(), claims.UserID, id); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to delete category group")
		return
	}

	writeSuccessMessage(w, "CategoryGroup deleted successfully")
}

func (h *Handler) CreateCategoryOption(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var request CategoryOptionInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	createdID, err := h.service.CreateCategoryOption(r.Context(), claims.UserID, request)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to create category option")
		return
	}

	writeSuccessData(w, http.StatusCreated, map[string]int64{"id": createdID})
}

func (h *Handler) UpdateCategoryOption(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var request CategoryOptionInput
	if err := legacy.DecodeJSON(r, &request); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.service.UpdateCategoryOption(r.Context(), claims.UserID, id, request); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to update category option")
		return
	}

	writeSuccessMessage(w, "CategoryOption updated successfully")
}

func (h *Handler) DeleteCategoryOption(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	if err := h.service.DeleteCategoryOption(r.Context(), claims.UserID, id); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to delete category option")
		return
	}

	writeSuccessMessage(w, "CategoryOption deleted successfully")
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

	var request struct {
		EntryInput
		OperationID string `json:"operationId"`
	}
	if !decodeEntryWrite(w, r, &request, &request.OperationID) {
		return
	}

	createdID, err := h.service.CreateEntry(r.Context(), claims.UserID, request.OperationID, request.EntryInput)
	if err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to create entry")
		return
	}

	writeSuccessData(w, http.StatusCreated, map[string]int64{"id": createdID})
}

func (h *Handler) UpdateEntryTime(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var request struct {
		EntryTimeInput
		OperationID string `json:"operationId"`
	}
	if !decodeEntryWrite(w, r, &request, &request.OperationID) {
		return
	}

	if err := h.service.UpdateEntryTime(r.Context(), claims.UserID, request.OperationID, id, request.EntryTimeInput); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to update entry time")
		return
	}

	writeSuccessMessage(w, "Entry time updated successfully")
}

func (h *Handler) UpdateEntrySelection(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var request struct {
		EntrySelectionInput
		OperationID string `json:"operationId"`
	}
	if !decodeEntryWrite(w, r, &request, &request.OperationID) {
		return
	}

	if err := h.service.UpdateEntrySelection(r.Context(), claims.UserID, request.OperationID, id, request.EntrySelectionInput); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to update entry selection")
		return
	}

	writeSuccessMessage(w, "Entry selection updated successfully")
}

func (h *Handler) DeleteEntry(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var request struct {
		OperationID string `json:"operationId"`
	}
	if !decodeEntryWrite(w, r, &request, &request.OperationID) {
		return
	}

	if err := h.service.DeleteEntry(r.Context(), claims.UserID, request.OperationID, id); err != nil {
		writeAppError(w, err, http.StatusInternalServerError, "Failed to delete entry")
		return
	}

	writeSuccessMessage(w, "Entry deleted successfully")
}

// decodeEntryWrite decodes an entry write body and requires its operationId: entry writes go
// through the frontend sync engine, and the operationId is what makes its retries idempotent.
func decodeEntryWrite(w http.ResponseWriter, r *http.Request, dst any, operationID *string) bool {
	if err := legacy.DecodeJSON(r, dst); err != nil {
		writeAppError(w, err, http.StatusBadRequest, "Invalid request body")
		return false
	}
	if *operationID == "" {
		writeError(w, http.StatusBadRequest, "operationId is required")
		return false
	}
	return true
}

func parseIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "Invalid id")
		return 0, false
	}
	return id, true
}

func writeSuccessData(w http.ResponseWriter, statusCode int, data any) {
	legacy.WriteJSON(w, statusCode, map[string]any{"success": true, "data": data})
}

func writeSuccessMessage(w http.ResponseWriter, message string) {
	legacy.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": message})
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	legacy.WriteJSON(w, statusCode, map[string]any{"success": false, "error": message})
}

func writeAppError(w http.ResponseWriter, err error, fallbackStatus int, fallbackMessage string) {
	writeError(w, legacy.ErrorStatusCodeOf(err, fallbackStatus), legacy.ErrorMessageOf(err, fallbackMessage))
}
