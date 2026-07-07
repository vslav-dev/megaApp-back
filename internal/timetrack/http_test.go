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

	catalog := assertTimetrackJSON(t, http.MethodGet, server.URL+"/api/time/catalog", tokens.AccessToken, nil, http.StatusOK)
	assertTimetrackEmptyCatalog(t, catalog)
	assertTimetrackEmptyListData(t, assertTimetrackJSON(t, http.MethodGet, server.URL+"/api/time/entries", tokens.AccessToken, nil, http.StatusOK))

	groupID := decodeCreatedID(t, assertTimetrackJSON(t, http.MethodPost, server.URL+"/api/time/category-groups", tokens.AccessToken, map[string]any{
		"name": "Game",
	}, http.StatusCreated))

	optionID := decodeCreatedID(t, assertTimetrackJSON(t, http.MethodPost, server.URL+"/api/time/category-options", tokens.AccessToken, map[string]any{
		"groupId": groupID,
		"name":    "ATS",
	}, http.StatusCreated))

	kindID := decodeCreatedID(t, assertTimetrackJSON(t, http.MethodPost, server.URL+"/api/time/activity-kinds", tokens.AccessToken, map[string]any{
		"name": "Game",
		"groupBindings": []map[string]any{
			{"groupId": groupID, "required": true},
		},
	}, http.StatusCreated))

	assertTimetrackStatus(t, http.MethodPut, server.URL+"/api/time/activity-kinds/"+itoa(kindID), tokens.AccessToken, map[string]any{
		"name": "Game (updated)",
		"groupBindings": []map[string]any{
			{"groupId": groupID, "required": true},
		},
	}, http.StatusOK)

	assertTimetrackStatus(t, http.MethodPut, server.URL+"/api/time/category-groups/"+itoa(groupID), tokens.AccessToken, map[string]any{
		"name": "Game updated",
	}, http.StatusOK)

	entryID := decodeCreatedID(t, assertTimetrackJSON(t, http.MethodPost, server.URL+"/api/time/entries", tokens.AccessToken, map[string]any{
		"activityKindId": kindID,
		"track":          "primary",
		"startAt":        "2026-07-06T09:00:00Z",
		"endAt":          "2026-07-06T12:00:00Z",
		"options": []map[string]any{
			{"groupId": groupID, "optionId": optionID},
		},
	}, http.StatusCreated))

	assertTimetrackStatus(t, http.MethodPost, server.URL+"/api/time/entries", tokens.AccessToken, map[string]any{
		"activityKindId": kindID,
		"track":          "primary",
		"startAt":        "2026-07-06T11:00:00Z",
		"endAt":          "2026-07-06T13:00:00Z",
		"options": []map[string]any{
			{"groupId": groupID, "optionId": optionID},
		},
	}, http.StatusConflict)

	assertTimetrackStatus(t, http.MethodGet, server.URL+"/api/time/entries?start=2026-07-01T00:00:00Z", tokens.AccessToken, nil, http.StatusOK)

	assertTimetrackStatus(t, http.MethodPatch, server.URL+"/api/time/entries/"+itoa(entryID)+"/time", tokens.AccessToken, map[string]any{
		"track":   "primary",
		"startAt": "2026-07-06T09:30:00Z",
		"endAt":   "2026-07-06T12:30:00Z",
	}, http.StatusOK)

	assertTimetrackStatus(t, http.MethodPatch, server.URL+"/api/time/entries/"+itoa(entryID)+"/selection", tokens.AccessToken, map[string]any{
		"activityKindId": kindID,
		"options": []map[string]any{
			{"groupId": groupID, "optionId": optionID},
		},
	}, http.StatusOK)

	assertTimetrackStatus(t, http.MethodDelete, server.URL+"/api/time/entries/"+itoa(entryID), tokens.AccessToken, nil, http.StatusOK)

	// The kind still binds the group — unbind first, same as the UI would
	// require before letting the group/option be deleted.
	assertTimetrackStatus(t, http.MethodPut, server.URL+"/api/time/activity-kinds/"+itoa(kindID), tokens.AccessToken, map[string]any{
		"name":          "Game (updated)",
		"groupBindings": []map[string]any{},
	}, http.StatusOK)

	// CategoryOption/CategoryGroup are now referenced by nothing, so a hard
	// delete of each must succeed.
	assertTimetrackStatus(t, http.MethodDelete, server.URL+"/api/time/category-options/"+itoa(optionID), tokens.AccessToken, nil, http.StatusOK)
	assertTimetrackStatus(t, http.MethodDelete, server.URL+"/api/time/category-groups/"+itoa(groupID), tokens.AccessToken, nil, http.StatusOK)
}

// assertTimetrackEmptyCatalog catches nil-slice-marshals-to-JSON-null regressions
// across all three catalog collections.
func assertTimetrackEmptyCatalog(t *testing.T, payload map[string]any) {
	t.Helper()
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing data object: %+v", payload)
	}
	for _, key := range []string{"activityKinds", "categoryGroups", "categoryOptions"} {
		if _, ok := data[key].([]any); !ok {
			t.Fatalf("payload data[%q] = %#v (%T), want empty []any, not null", key, data[key], data[key])
		}
	}
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
