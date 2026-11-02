package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

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
