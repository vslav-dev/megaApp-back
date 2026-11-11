package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOriginMiddleware(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		host    string
		headers map[string]string
		want    int
	}{
		// Non-browser clients and same-origin browsers: prod and test sit behind nginx with `Host $host`.
		{name: "curl without origin headers", method: http.MethodPost, host: "app.vslav.dev", want: http.StatusOK},
		{name: "prod same-origin browser", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://app.vslav.dev"}, want: http.StatusOK},
		{name: "test same-origin browser", method: http.MethodPost, host: "test.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://test.vslav.dev"}, want: http.StatusOK},
		{name: "same-origin browser without Sec-Fetch-Site", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Origin": "https://app.vslav.dev"}, want: http.StatusOK},
		{name: "same-origin PUT", method: http.MethodPut, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://app.vslav.dev"}, want: http.StatusOK},
		{name: "same-origin DELETE", method: http.MethodDelete, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://app.vslav.dev"}, want: http.StatusOK},
		{name: "user-initiated navigation", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "none"}, want: http.StatusOK},
		{name: "swagger try-it-out", method: http.MethodPost, host: "test.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://test.vslav.dev"}, want: http.StatusOK},

		// Local dev: `ng serve` proxies /api with changeOrigin, so the backend sees Host localhost:3000
		// while the browser considers the call same-origin to :4200.
		{name: "dev proxy from 4200", method: http.MethodPost, host: "localhost:3000", headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://localhost:4200"}, want: http.StatusOK},
		{name: "dev proxy from 4201", method: http.MethodPost, host: "localhost:3000", headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://localhost:4201"}, want: http.StatusOK},
		{name: "dev proxy via 127.0.0.1", method: http.MethodPost, host: "127.0.0.1:3000", headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://127.0.0.1:4200"}, want: http.StatusOK},
		{name: "dev proxy through a tunnel", method: http.MethodPost, host: "localhost:3000", headers: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://dev.ngrok.app"}, want: http.StatusOK},

		// Cross-origin browser requests with a state-changing method.
		{name: "cross-site POST", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, want: http.StatusForbidden},
		{name: "same-site POST from a sibling subdomain", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://evil.vslav.dev"}, want: http.StatusForbidden},
		{name: "cross-site POST without Origin", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "cross-site"}, want: http.StatusForbidden},
		{name: "cross-site PUT", method: http.MethodPut, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, want: http.StatusForbidden},
		{name: "cross-site PATCH", method: http.MethodPatch, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, want: http.StatusForbidden},
		{name: "cross-site DELETE", method: http.MethodDelete, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, want: http.StatusForbidden},
		{name: "opaque origin", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "null"}, want: http.StatusForbidden},
		{name: "dev server hit from a foreign page", method: http.MethodPost, host: "localhost:3000", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "http://evil.example"}, want: http.StatusForbidden},

		// Browsers without Sec-Fetch-Site fall back to Origin against Host.
		{name: "foreign Origin without Sec-Fetch-Site", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Origin": "https://evil.example"}, want: http.StatusForbidden},
		{name: "opaque Origin without Sec-Fetch-Site", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Origin": "null"}, want: http.StatusForbidden},
		{name: "Origin of another port without Sec-Fetch-Site", method: http.MethodPost, host: "app.vslav.dev", headers: map[string]string{"Origin": "https://app.vslav.dev:8443"}, want: http.StatusForbidden},

		// Safe methods are never blocked.
		{name: "cross-site GET", method: http.MethodGet, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, want: http.StatusOK},
		{name: "cross-site HEAD", method: http.MethodHead, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, want: http.StatusOK},
		{name: "cross-site OPTIONS", method: http.MethodOptions, host: "app.vslav.dev", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, want: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := OriginMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			request := httptest.NewRequest(tt.method, "/api/auth/login", nil)
			request.Host = tt.host
			for name, value := range tt.headers {
				request.Header.Set(name, value)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.want)
			}
		})
	}
}

func TestOriginMiddlewareRejectionBody(t *testing.T) {
	handler := OriginMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler must not run for a cross-origin request")
	}))

	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	request.Host = "app.vslav.dev"
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	if body := strings.TrimSpace(recorder.Body.String()); body != `{"detail":"Cross-origin request rejected"}` {
		t.Fatalf("body = %s", body)
	}
}
