package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/mayura-andrew/email-client/internal/ratelimit"
	"github.com/mayura-andrew/email-client/internal/session"
)

const sessionCookie = "admin_session"

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
	// Compare fixed-length digests in constant time so response timing does not
	// reveal how much of the credentials matched.
	emailOK := secureEqual(request.Email, app.config.admin.email)
	passwordOK := secureEqual(request.Password, app.config.admin.password)
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
		SameSite: http.SameSiteLaxMode, Secure: app.config.admin.cookieSecure, Path: "/",
	})
	_ = app.writeJSON(w, http.StatusOK, envelop{"authenticated": true}, nil)
}

func (app *application) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := app.sessions.Delete(r.Context(), cookie.Value); err != nil {
			app.serverErrorRespone(w, r, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: app.config.admin.cookieSecure, Path: "/"})
	_ = app.writeJSON(w, http.StatusOK, envelop{"authenticated": false}, nil)
}

func (app *application) sessionHandler(w http.ResponseWriter, r *http.Request) {
	_ = app.writeJSON(w, http.StatusOK, envelop{"authenticated": true}, nil)
}

func (app *application) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			app.errorResponse(w, r, http.StatusUnauthorized, "authentication required")
			return
		}
		if _, err := app.sessions.Lookup(r.Context(), cookie.Value); err != nil {
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
