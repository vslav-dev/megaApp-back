package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"megaapp-back/internal/httpx/legacy"

	"github.com/go-chi/chi/v5"
)

type contextKey string

const identityContextKey contextKey = "auth_identity"

type Handler struct {
	service          *Service
	onSessionRevoked func(string)
	onSessionRenewed func(string)
}

type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	UserID        int64  `json:"userId"`
	Username      string `json:"username"`
	IsAdmin       bool   `json:"isAdmin"`
	ExpiresAt     string `json:"expiresAt"`
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) SetSessionRevoker(revoker func(string)) {
	h.onSessionRevoked = revoker
}

func (h *Handler) SetSessionRenewer(renewer func(string)) {
	h.onSessionRenewed = renewer
}

func RegisterRoutes(router chi.Router, handler *Handler) {
	router.Route("/api/auth", func(r chi.Router) {
		r.With(OriginMiddleware).Post("/register", handler.Register)
		r.With(OriginMiddleware).Post("/login", handler.Login)
		r.With(Middleware(handler.service)).Get("/session", handler.Session)
		r.With(Middleware(handler.service)).Post("/renew", handler.Renew)
		r.With(Middleware(handler.service)).Post("/logout", handler.Logout)
	})
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if err := legacy.DecodeJSON(r, &request); err != nil {
		legacy.WriteAppMessageError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	_, err := h.service.Register(r.Context(), request.Username, request.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrUsernameTaken):
			legacy.WriteDetail(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrInvalidPayload):
			legacy.WriteDetail(w, http.StatusBadRequest, "Username and password are required")
		default:
			legacy.WriteAppDetailError(w, err, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	legacy.WriteJSON(w, http.StatusCreated, map[string]string{"message": "User created successfully"})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if err := legacy.DecodeJSON(r, &request); err != nil {
		legacy.WriteAppMessageError(w, err, http.StatusBadRequest, "Invalid request body")
		return
	}

	result, err := h.service.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCreds) {
			legacy.WriteDetail(w, http.StatusUnauthorized, err.Error())
			return
		}
		legacy.WriteAppDetailError(w, err, http.StatusInternalServerError, "Internal server error")
		return
	}
	if previous, err := h.service.AuthenticateCookie(r.Context(), sessionCookieValue(r)); err == nil {
		if err := h.service.Revoke(r.Context(), previous.SessionID); err != nil {
			legacy.WriteAppDetailError(w, err, http.StatusInternalServerError, "Internal server error")
			return
		}
		if h.onSessionRevoked != nil {
			h.onSessionRevoked(previous.SessionID)
		}
	}

	setSessionCookie(w, r, result.Cookie, result.Identity.ExpiresAt)
	legacy.WriteJSON(w, http.StatusOK, makeSessionResponse(result.Identity))
}

func (h *Handler) Session(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		legacy.WriteDetail(w, http.StatusUnauthorized, "Invalid session")
		return
	}
	legacy.WriteJSON(w, http.StatusOK, makeSessionResponse(identity))
}

func (h *Handler) Renew(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		legacy.WriteDetail(w, http.StatusUnauthorized, "Invalid session")
		return
	}
	renewed, err := h.service.Renew(r.Context(), identity)
	if err != nil {
		if errors.Is(err, ErrInvalidSession) {
			clearSessionCookie(w, r)
			legacy.WriteDetail(w, http.StatusUnauthorized, "Invalid session")
			return
		}
		legacy.WriteAppDetailError(w, err, http.StatusInternalServerError, "Internal server error")
		return
	}
	setSessionCookie(w, r, sessionCookieValue(r), renewed.ExpiresAt)
	if renewed.ExpiresAt.After(identity.ExpiresAt) && h.onSessionRenewed != nil {
		h.onSessionRenewed(renewed.SessionID)
	}
	legacy.WriteJSON(w, http.StatusOK, makeSessionResponse(renewed))
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		clearSessionCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.service.Revoke(r.Context(), identity.SessionID); err != nil {
		legacy.WriteAppDetailError(w, err, http.StatusInternalServerError, "Internal server error")
		return
	}
	if h.onSessionRevoked != nil {
		h.onSessionRevoked(identity.SessionID)
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func Middleware(service *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return OriginMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, err := service.AuthenticateCookie(r.Context(), sessionCookieValue(r))
			if err != nil {
				legacy.WriteDetail(w, http.StatusUnauthorized, "Invalid session")
				return
			}
			ctx := context.WithValue(r.Context(), identityContextKey, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		}))
	}
}

func OriginMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUnsafeMethod(r.Method) && !isSameOrigin(r) {
			legacy.WriteDetail(w, http.StatusForbidden, "Cross-origin request rejected")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey).(Identity)
	return identity, ok
}

func UserClaimsFromContext(ctx context.Context) (Identity, bool) {
	return IdentityFromContext(ctx)
}

func makeSessionResponse(identity Identity) sessionResponse {
	return sessionResponse{
		Authenticated: true,
		UserID:        identity.UserID,
		Username:      identity.Username,
		IsAdmin:       identity.IsAdmin,
		ExpiresAt:     identity.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode})
}

func sessionCookieValue(r *http.Request) string {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func isSameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
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
