package ws

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"megaapp-back/internal/auth"
	"megaapp-back/internal/httpx/legacy"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

type Handler struct {
	authService *auth.Service
	hub         *Hub
	upgrader    websocket.Upgrader
}

func NewHandler(authService *auth.Service, hub *Hub) *Handler {
	return &Handler{
		authService: authService,
		hub:         hub,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return isAllowedOrigin(r)
			},
		},
	}
}

func RegisterRoutes(router chi.Router, handler *Handler) {
	router.Get("/api/ws", handler.Connect)
}

func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) {
	identity, err := h.authService.AuthenticateCookie(r.Context(), sessionCookieValue(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid session")
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	clientID := strings.TrimSpace(r.URL.Query().Get("clientId"))
	if clientID == "" {
		clientID = strings.TrimSpace(r.Header.Get("X-Client-ID"))
	}
	if clientID == "" {
		clientID = strings.TrimSpace(r.Header.Get("X-Client-Id"))
	}

	if _, err := h.hub.AddClient(conn, identity.UserID, identity.SessionID, identity.ExpiresAt, clientID); err != nil {
		_ = conn.Close()
	}
}

func sessionCookieValue(r *http.Request) string {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func isAllowedOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || (host != "localhost" && host != "127.0.0.1") || port != "3000" {
		return false
	}
	return (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1") && (parsed.Port() == "4200" || parsed.Port() == "4201")
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	legacy.WriteJSON(w, statusCode, map[string]string{"error": message})
}
