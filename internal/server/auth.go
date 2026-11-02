package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/mayura-andrew/email-client/internal/ratelimit"
	"github.com/mayura-andrew/email-client/internal/session"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "admin_session"

// The desktop webview is not guaranteed to keep cookies set by the embedded
// API, so the UI sends the session token as a bearer token; the cookie remains
// as a fallback.
func sessionToken(r *http.Request) string {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(token)
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		return cookie.Value
	}
	return ""
}

// Five attempts per minute per client blunts password guessing across replicas.
var loginLimit = ratelimit.PerMinute(5, 5)

func (app *application) loginHandler(w http.ResponseWriter, r *http.Request) {
	allowed, retryAfter, err := app.limiter.Allow(r.Context(), "login:"+app.clientIP(r), loginLimit)
	if err == nil && !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		app.rateLimitExceededResponse(w, r)
		return
	}

	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := app.readJSON(w, r, &request); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	// Both checks always run so response timing does not reveal which one failed.
	emailOK := secureEqual(strings.ToLower(strings.TrimSpace(request.Email)), strings.ToLower(app.config.admin.email))
	passwordOK := bcrypt.CompareHashAndPassword([]byte(app.config.admin.passwordHash), []byte(request.Password)) == nil
	if !emailOK || !passwordOK {
		app.errorResponse(w, r, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := app.sessions.Create(r.Context(), app.config.admin.email)
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, MaxAge: int(app.sessions.TTL.Seconds()), HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Path: "/",
	})
	_ = app.writeJSON(w, http.StatusOK, envelop{"authenticated": true, "token": token}, nil)
}

func (app *application) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if token := sessionToken(r); token != "" {
		if err := app.sessions.Delete(r.Context(), token); err != nil {
			app.serverErrorRespone(w, r, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Path: "/"})
	_ = app.writeJSON(w, http.StatusOK, envelop{"authenticated": false}, nil)
}

func (app *application) sessionHandler(w http.ResponseWriter, r *http.Request) {
	_ = app.writeJSON(w, http.StatusOK, envelop{"authenticated": true}, nil)
}

func (app *application) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := sessionToken(r)
		if token == "" {
			app.errorResponse(w, r, http.StatusUnauthorized, "authentication required")
			return
		}
		if _, err := app.sessions.Lookup(r.Context(), token); err != nil {
			if errors.Is(err, session.ErrNotFound) {
				app.errorResponse(w, r, http.StatusUnauthorized, "authentication required")
				return
			}
			app.serverErrorRespone(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secureEqual(given, expected string) bool {
	a := sha256.Sum256([]byte(given))
	b := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// Authenticated reports whether r carries a live admin session.
func (s *Server) Authenticated(r *http.Request) bool {
	token := sessionToken(r)
	if token == "" {
		return false
	}
	_, err := s.app.sessions.Lookup(r.Context(), token)
	return err == nil
}
