package main

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mayura-andrew/email-client/internal/ratelimit"
)

// rateLimit applies a per-client budget stored in Redis, so the limit holds no
// matter which replica serves the request. If Redis is unavailable the request
// is allowed: an outage of the limiter must not take the API down with it.
func (app *application) rateLimit(next http.Handler) http.Handler {
	limit := ratelimit.Limit{Rate: app.config.limiter.rps, Burst: app.config.limiter.burst}
	exempt := map[string]bool{
		"/api/v1/healthcheck": true,
		// Opens arrive from mail clients behind shared NATs; one office must not
		// exhaust the budget for everyone.
		"/api/v1/redirect": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !app.config.limiter.enabled || exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		allowed, retryAfter, err := app.limiter.Allow(r.Context(), "api:"+app.clientIP(r), limit)
		if err != nil {
			app.logger.PrintError(err, map[string]string{"component": "rate-limiter"})
			next.ServeHTTP(w, r)
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
			app.rateLimitExceededResponse(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP trusts X-Forwarded-For only when the direct peer is a configured
// proxy, and then takes the right-most address that is not itself a proxy, which
// is the first hop a client cannot forge.
func (app *application) clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !app.isTrustedProxy(ip) {
		return ip
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}
		if !app.isTrustedProxy(hop) {
			return hop
		}
	}
	return ip
}

func (app *application) isTrustedProxy(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil {
		return false
	}
	for _, network := range app.config.limiter.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (app *application) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				w.Header().Set("Connection", "close")
				app.serverErrorRespone(w, r, fmt.Errorf("%s", err))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (app *application) enableCORS(next http.Handler) http.Handler {
	allowedOrigins := make(map[string]bool, len(app.config.cors.trustedOrigns))
	for _, origin := range app.config.cors.trustedOrigns {
		allowedOrigins[origin] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")

		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int((10 * time.Minute).Seconds())))
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
