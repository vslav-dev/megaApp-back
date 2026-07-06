package timetrack

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"megaapp-back/internal/auth"

	"github.com/go-chi/chi/v5"
)

func TestTimetrackRoutesCrud(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")

	authRepo := auth.NewRepository(db)
	tokenManager := auth.NewTokenManager("test-secret", time.Hour, 24*time.Hour)
	authService := auth.NewService(authRepo, tokenManager)
	handler := NewHandler(NewService(NewRepository(db)))

	router := chi.NewRouter()
	RegisterRoutes(router, authService, handler)
	server := httptest.NewServer(router)
	defer server.Close()

	tokens, err := tokenManager.Issue(auth.TokenClaims{UserID: 1, Username: "alice"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	assertTimetrackEmptyListData(t, assertTimetrackJSON(t, http.MethodGet, server.URL+"/api/time/activities", tokens.AccessToken, nil, http.StatusOK))
	assertTimetrackEmptyListData(t, assertTimetrackJSON(t, http.MethodGet, server.URL+"/api/time/categories", tokens.AccessToken, nil, http.StatusOK))
	assertTimetrackEmptyListData(t, assertTimetrackJSON(t, http.MethodGet, server.URL+"/api/time/entries", tokens.AccessToken, nil, http.StatusOK))
	assertTimetrackEmptyListData(t, assertTimetrackJSON(t, http.MethodGet, server.URL+"/api/time/activities/recent", tokens.AccessToken, nil, http.StatusOK))

	categoryID := decodeCreatedID(t, assertTimetrackJSON(t, http.MethodPost, server.URL+"/api/time/categories", tokens.AccessToken, map[string]any{
		"name": "Work",
		"kind": "area",
	}, http.StatusCreated))

	activityID := decodeCreatedID(t, assertTimetrackJSON(t, http.MethodPost, server.URL+"/api/time/activities", tokens.AccessToken, map[string]any{
		"name":        "Coding",
		"categoryIds": []int64{categoryID},
	}, http.StatusCreated))

	assertTimetrackStatus(t, http.MethodPut, server.URL+"/api/time/activities/"+itoa(activityID), tokens.AccessToken, map[string]any{
		"name":        "Coding (updated)",
		"categoryIds": []int64{categoryID},
	}, http.StatusOK)

	assertTimetrackStatus(t, http.MethodPut, server.URL+"/api/time/categories/"+itoa(categoryID), tokens.AccessToken, map[string]any{
		"name": "Work updated",
		"kind": "area",
	}, http.StatusOK)

	entryID := decodeCreatedID(t, assertTimetrackJSON(t, http.MethodPost, server.URL+"/api/time/entries", tokens.AccessToken, map[string]any{
		"activityId": activityID,
		"track":      "primary",
		"startAt":    "2026-07-06T09:00:00Z",
		"endAt":      "2026-07-06T12:00:00Z",
	}, http.StatusCreated))

	assertTimetrackStatus(t, http.MethodPost, server.URL+"/api/time/entries", tokens.AccessToken, map[string]any{
		"activityId": activityID,
		"track":      "primary",
		"startAt":    "2026-07-06T11:00:00Z",
		"endAt":      "2026-07-06T13:00:00Z",
	}, http.StatusConflict)

	assertTimetrackStatus(t, http.MethodGet, server.URL+"/api/time/entries?start=2026-07-01T00:00:00Z", tokens.AccessToken, nil, http.StatusOK)
	assertTimetrackStatus(t, http.MethodGet, server.URL+"/api/time/activities/recent", tokens.AccessToken, nil, http.StatusOK)

	assertTimetrackStatus(t, http.MethodPut, server.URL+"/api/time/entries/"+itoa(entryID), tokens.AccessToken, map[string]any{
		"activityId": activityID,
		"track":      "primary",
		"startAt":    "2026-07-06T09:30:00Z",
		"endAt":      "2026-07-06T12:30:00Z",
	}, http.StatusOK)

	assertTimetrackStatus(t, http.MethodDelete, server.URL+"/api/time/entries/"+itoa(entryID), tokens.AccessToken, nil, http.StatusOK)
	assertTimetrackStatus(t, http.MethodDelete, server.URL+"/api/time/categories/"+itoa(categoryID), tokens.AccessToken, nil, http.StatusOK)
}

// assertTimetrackEmptyListData catches nil-slice-marshals-to-JSON-null regressions:
// the frontend spreads/iterates `data` directly, so an empty list must be `[]`, never `null`.
func assertTimetrackEmptyListData(t *testing.T, payload map[string]any) {
	t.Helper()
	if _, ok := payload["data"].([]any); !ok {
		t.Fatalf(`payload["data"] = %#v (%T), want empty []any, not null`, payload["data"], payload["data"])
	}
}

func decodeCreatedID(t *testing.T, payload map[string]any) int64 {
	t.Helper()
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing data object: %+v", payload)
	}
	id, ok := data["id"].(float64)
	if !ok {
		t.Fatalf("payload data missing numeric id: %+v", data)
	}
	return int64(id)
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

func assertTimetrackStatus(t *testing.T, method string, url string, accessToken string, body any, wantStatus int) {
	t.Helper()
	assertTimetrackJSON(t, method, url, accessToken, body, wantStatus)
}

func assertTimetrackJSON(t *testing.T, method string, url string, accessToken string, body any, wantStatus int) map[string]any {
	t.Helper()

	var requestBody *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	} else {
		requestBody = bytes.NewReader(nil)
	}

	request, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()

	var payload map[string]any
	_ = json.NewDecoder(response.Body).Decode(&payload)

	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %+v", method, url, response.StatusCode, wantStatus, payload)
	}
	return payload
}
