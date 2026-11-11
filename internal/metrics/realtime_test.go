package metrics

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"megaapp-back/internal/auth"
	"megaapp-back/internal/metrics/wire"
	"megaapp-back/internal/platform/sqlite"
	"megaapp-back/internal/ws"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	_ "modernc.org/sqlite"
)

func TestSubscribeHandlerOnlyAcceptsAdmins(t *testing.T) {
	authService, realtime, server, authDB := newMetricsTestEnv(t)
	defer server.Close()

	adminUserID := registerAdminUser(t, authService, authDB, "admin")
	plainUserID := registerUser(t, authService, "plain")

	adminCookie := issueMetricsSession(t, authService, adminUserID)
	plainCookie := issueMetricsSession(t, authService, plainUserID)

	adminConn := dialMetricsWS(t, server.URL+"/api/ws?clientId=admin-tab", adminCookie)
	defer func() { _ = adminConn.Close() }()
	drainMetricsMessage(t, adminConn)

	plainConn := dialMetricsWS(t, server.URL+"/api/ws?clientId=plain-tab", plainCookie)
	defer func() { _ = plainConn.Close() }()
	drainMetricsMessage(t, plainConn)

	if err := adminConn.WriteJSON(metricsSubscribeMessage("food", "food_diary_entry_created")); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if err := plainConn.WriteJSON(metricsSubscribeMessage("food", "food_diary_entry_created")); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	_ = plainConn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var plainUpdate map[string]any
	if err := plainConn.ReadJSON(&plainUpdate); err == nil {
		t.Fatalf("plain user unexpectedly received: %+v", plainUpdate)
	}

	realtime.BroadcastDetail(DetailUpdate{Points: []MetricPoint{{Service: "food", Name: "food_diary_entry_created", Bucket: 1, Value: 1}}})

	_ = adminConn.SetReadDeadline(time.Now().Add(time.Second))
	frameType, _ := readMetricsFrame(t, adminConn)
	if frameType != wsFrameMetricsUpdate {
		t.Fatalf("frameType = %d, want %d (METRICS_UPDATE)", frameType, wsFrameMetricsUpdate)
	}

	_ = plainConn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var plainBroadcast map[string]any
	if err := plainConn.ReadJSON(&plainBroadcast); err == nil {
		t.Fatalf("plain user unexpectedly received broadcast: %+v", plainBroadcast)
	}
}

func TestUnsubscribeStopsDetailBroadcast(t *testing.T) {
	authService, realtime, server, authDB := newMetricsTestEnv(t)
	defer server.Close()

	adminUserID := registerAdminUser(t, authService, authDB, "admin")
	adminCookie := issueMetricsSession(t, authService, adminUserID)

	conn := dialMetricsWS(t, server.URL+"/api/ws?clientId=admin-tab", adminCookie)
	defer func() { _ = conn.Close() }()
	drainMetricsMessage(t, conn)

	if err := conn.WriteJSON(metricsSubscribeMessage("food", "food_diary_entry_created")); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := conn.WriteJSON(map[string]any{"type": "METRICS_UNSUBSCRIBE"}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	realtime.BroadcastDetail(DetailUpdate{Points: []MetricPoint{{Service: "food", Name: "food_diary_entry_created", Bucket: 1, Value: 1}}})

	_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var message map[string]any
	if err := conn.ReadJSON(&message); err == nil {
		t.Fatalf("unexpectedly received message after unsubscribe: %+v", message)
	}
}

func TestBroadcastDetailFiltersOutOfScopePoints(t *testing.T) {
	authService, realtime, server, authDB := newMetricsTestEnv(t)
	defer server.Close()

	adminUserID := registerAdminUser(t, authService, authDB, "admin")
	adminCookie := issueMetricsSession(t, authService, adminUserID)

	conn := dialMetricsWS(t, server.URL+"/api/ws?clientId=admin-tab", adminCookie)
	defer func() { _ = conn.Close() }()
	drainMetricsMessage(t, conn)

	if err := conn.WriteJSON(metricsSubscribeMessage("food", "food_diary_entry_created")); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	realtime.BroadcastDetail(DetailUpdate{Points: []MetricPoint{{Service: "other", Name: "unrelated_metric", Bucket: 1, Value: 1}}})

	_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var message map[string]any
	if err := conn.ReadJSON(&message); err == nil {
		t.Fatalf("unexpectedly received out-of-scope point: %+v", message)
	}
}

func TestResubscribeReplacesScopeInsteadOfMerging(t *testing.T) {
	authService, realtime, server, authDB := newMetricsTestEnv(t)
	defer server.Close()

	adminUserID := registerAdminUser(t, authService, authDB, "admin")
	adminCookie := issueMetricsSession(t, authService, adminUserID)

	conn := dialMetricsWS(t, server.URL+"/api/ws?clientId=admin-tab", adminCookie)
	defer func() { _ = conn.Close() }()
	drainMetricsMessage(t, conn)

	if err := conn.WriteJSON(metricsSubscribeMessage("food", "old_metric")); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := conn.WriteJSON(metricsSubscribeMessage("food", "new_metric")); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// old_metric is no longer in scope after the resubscribe above — this
	// broadcast matches nothing and writes nothing to the socket (see
	// BroadcastDetail's continue on an empty filtered set), so it can't
	// race with the read below. A gorilla/websocket connection becomes
	// unusable for reads after any read deadline expires, which is why this
	// test can't also assert "no message" via a timed-out read on the same
	// connection it keeps using afterward.
	realtime.BroadcastDetail(DetailUpdate{Points: []MetricPoint{{Service: "food", Name: "old_metric", Bucket: 1, Value: 1}}})
	realtime.BroadcastDetail(DetailUpdate{Points: []MetricPoint{{Service: "food", Name: "new_metric", Bucket: 2, Value: 2}}})

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	frameType, series := readMetricsFrame(t, conn)
	if frameType != wsFrameMetricsUpdate {
		t.Fatalf("frameType = %d, want %d (METRICS_UPDATE)", frameType, wsFrameMetricsUpdate)
	}
	if len(series) != 1 || series[0].MetricName != "new_metric" {
		t.Fatalf("series = %+v, want exactly one series named new_metric (old_metric must not leak in)", series)
	}
}

func TestBroadcastLatestReachesOnlyAdmins(t *testing.T) {
	authService, realtime, server, authDB := newMetricsTestEnv(t)
	defer server.Close()

	adminUserID := registerAdminUser(t, authService, authDB, "admin")
	plainUserID := registerUser(t, authService, "plain")

	adminCookie := issueMetricsSession(t, authService, adminUserID)
	plainCookie := issueMetricsSession(t, authService, plainUserID)

	adminConn := dialMetricsWS(t, server.URL+"/api/ws?clientId=admin-tab", adminCookie)
	defer func() { _ = adminConn.Close() }()
	drainMetricsMessage(t, adminConn)

	plainConn := dialMetricsWS(t, server.URL+"/api/ws?clientId=plain-tab", plainCookie)
	defer func() { _ = plainConn.Close() }()
	drainMetricsMessage(t, plainConn)

	realtime.BroadcastLatest([]int64{adminUserID}, LatestSnapshot{Services: []ServiceLatest{{Service: MainServiceName, LastBucket: 60, Metrics: map[string]float64{"food_diary_entry_created": 1}}}})

	_ = adminConn.SetReadDeadline(time.Now().Add(time.Second))
	frameType, series := readMetricsFrame(t, adminConn)
	if frameType != wsFrameMetricsLatest {
		t.Fatalf("frameType = %d, want %d (METRICS_LATEST)", frameType, wsFrameMetricsLatest)
	}
	if len(series) != 1 || series[0].Service != MainServiceName || series[0].MetricName != "food_diary_entry_created" {
		t.Fatalf("series = %+v, want one series for %s/food_diary_entry_created", series, MainServiceName)
	}

	_ = plainConn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var plainLatest map[string]any
	if err := plainConn.ReadJSON(&plainLatest); err == nil {
		t.Fatalf("plain user unexpectedly received latest: %+v", plainLatest)
	}
}

func TestHistoryHandlerForwardsScopeAndFloorsToFlatline(t *testing.T) {
	authService, _, wsServer, authDB := newMetricsTestEnv(t)
	defer wsServer.Close()

	adminUserID := registerAdminUser(t, authService, authDB, "admin")
	adminCookie := issueMetricsSession(t, authService, adminUserID)

	requests := 0
	flatlineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		var body historyRequestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body error = %v", err)
		}
		if body.MinuteSince != 60 || body.HourSince != 3600 || body.DaySince != 86400 {
			t.Fatalf("floors = %+v, want unchanged 60/3600/86400", body)
		}
		if len(body.Scope) != 1 || body.Scope[0].Service != "bot" || len(body.Scope[0].MetricNames) != 1 || body.Scope[0].MetricNames[0] != "a" {
			t.Fatalf("scope = %+v, want forwarded unchanged", body.Scope)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(wire.Encode([]wire.Series{
			{Service: "bot", MetricName: "a", Granularity: wire.GranularityMinute, Points: []wire.Point{{Bucket: 60, Value: 1}}},
		}))
	}))
	defer flatlineServer.Close()

	service := NewService(MainServiceName, fixedMetricsClock{now: time.Now()}, authService)
	router := chi.NewRouter()
	RegisterRoutes(router, authService, NewHistoryHandler(service, NewFlatlineClient(flatlineServer.URL, time.Second)))

	req := newHistoryHTTPRequest(t, historyRequest{
		MinuteSince: 60, HourSince: 3600, DaySince: 86400,
		Scope: []ScopeEntry{{Service: "bot", MetricNames: []string{"a"}}},
	})
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: adminCookie})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", recorder.Header().Get("Content-Type"))
	}
	series, err := wire.Decode(recorder.Body.Bytes())
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(series) != 1 || series[0].Service != "bot" {
		t.Fatalf("series = %+v, want bot series", series)
	}
	if requests != 1 {
		t.Fatalf("Flatline requests = %d, want 1", requests)
	}
}

func TestHistoryHandlerRejectsMissingOrInvalidScope(t *testing.T) {
	authService, _, wsServer, authDB := newMetricsTestEnv(t)
	defer wsServer.Close()

	adminUserID := registerAdminUser(t, authService, authDB, "admin")
	adminCookie := issueMetricsSession(t, authService, adminUserID)

	service := NewService(MainServiceName, fixedMetricsClock{now: time.Now()}, authService)
	router := chi.NewRouter()
	RegisterRoutes(router, authService, NewHistoryHandler(service, NewFlatlineClient("http://unused.invalid", time.Second)))

	tests := []historyRequest{
		{MinuteSince: 60, HourSince: 3600, DaySince: 86400, Scope: nil},
		{MinuteSince: 60, HourSince: 3600, DaySince: 86400, Scope: []ScopeEntry{{Service: "bot", MetricNames: nil}}},
		{MinuteSince: 0, HourSince: 3600, DaySince: 86400, Scope: []ScopeEntry{{Service: "bot", MetricNames: []string{"a"}}}},
	}
	for _, body := range tests {
		req := newHistoryHTTPRequest(t, body)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: adminCookie})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d for %+v", recorder.Code, http.StatusBadRequest, body)
		}
	}
}

func newHistoryHTTPRequest(t *testing.T, body historyRequest) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/metrics/history", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func metricsSubscribeMessage(service string, metricNames ...string) map[string]any {
	return map[string]any{
		"type": "METRICS_SUBSCRIBE",
		"payload": map[string]any{
			"scope": []map[string]any{{"service": service, "metricNames": metricNames}},
		},
	}
}

func newMetricsTestEnv(t *testing.T) (*auth.Service, *Realtime, *httptest.Server, *sql.DB) {
	t.Helper()

	authDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = authDB.Close() })

	if _, err := authDB.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT,
			hashedPassword TEXT,
			isAdmin BOOLEAN
		);
		CREATE TABLE auth_sessions (id TEXT PRIMARY KEY, secretHash BLOB NOT NULL, userId INTEGER NOT NULL, createdAt TEXT NOT NULL, expiresAt TEXT NOT NULL, renewedAt TEXT NOT NULL, revokedAt TEXT);
	`); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	authService := auth.NewService(auth.NewRepository(authDB, sqlite.WriteDB{DB: authDB}), auth.SessionConfig{})

	service := NewService(MainServiceName, fixedMetricsClock{now: time.Now()}, authService)

	hub := ws.NewHub(time.Second, ws.NewSyncState())
	t.Cleanup(func() { _ = hub.Close() })
	realtime := NewRealtime(hub)

	hub.RegisterHandler("METRICS_SUBSCRIBE", NewSubscribeHandler(service, realtime))
	hub.RegisterHandler("METRICS_UNSUBSCRIBE", NewUnsubscribeHandler(realtime))

	wsHandler := ws.NewHandler(authService, hub)
	router := chi.NewRouter()
	ws.RegisterRoutes(router, wsHandler)
	server := httptest.NewServer(router)

	return authService, realtime, server, authDB
}

func registerUser(t *testing.T, authService *auth.Service, username string) int64 {
	t.Helper()

	userID, err := authService.Register(context.Background(), username, "password123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return userID
}

func registerAdminUser(t *testing.T, authService *auth.Service, authDB *sql.DB, username string) int64 {
	t.Helper()

	userID := registerUser(t, authService, username)

	if _, err := authDB.Exec(`UPDATE users SET isAdmin = 1 WHERE id = ?`, userID); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	return userID
}

func issueMetricsSession(t *testing.T, service *auth.Service, userID int64) string {
	t.Helper()

	session, err := service.CreateSession(t.Context(), userID)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	return session.Cookie
}

func dialMetricsWS(t *testing.T, httpURL string, sessionCookie string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + httpURL[len("http"):]
	parsed, err := url.Parse(httpURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	headers := http.Header{
		"Cookie": {auth.SessionCookieName + "=" + sessionCookie},
		"Origin": {"http://" + parsed.Host},
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	return conn
}

// readMetricsFrame reads one binary WS frame carrying a METRICS_UPDATE/
// METRICS_LATEST payload — see the wsFrameMetricsUpdate/wsFrameMetricsLatest
// prefix byte in realtime.go — and decodes the wire payload after it.
func readMetricsFrame(t *testing.T, conn *websocket.Conn) (byte, []wire.Series) {
	t.Helper()

	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("messageType = %d, want %d (binary)", messageType, websocket.BinaryMessage)
	}
	if len(data) == 0 {
		t.Fatal("frame data is empty, want at least a type-prefix byte")
	}

	series, err := wire.Decode(data[1:])
	if err != nil {
		t.Fatalf("wire.Decode() error = %v", err)
	}
	return data[0], series
}

func drainMetricsMessage(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var message map[string]any
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
}
