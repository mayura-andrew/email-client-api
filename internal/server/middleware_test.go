package server

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	app := &application{}
	if err := parseCIDRs("10.0.0.0/8 192.168.1.5", &app.config.limiter.trustedProxies); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		remote, forwarded, want string
	}{
		{"203.0.113.9:5000", "", "203.0.113.9"},
		// An untrusted peer cannot choose its own identity.
		{"203.0.113.9:5000", "1.2.3.4", "203.0.113.9"},
		// Behind the load balancer, the right-most untrusted hop is the client.
		{"10.1.2.3:5000", "198.51.100.7", "198.51.100.7"},
		{"10.1.2.3:5000", "6.6.6.6, 198.51.100.7, 192.168.1.5", "198.51.100.7"},
		{"10.1.2.3:5000", "", "10.1.2.3"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.forwarded != "" {
			r.Header.Set("X-Forwarded-For", tc.forwarded)
		}
		if got := app.clientIP(r); got != tc.want {
			t.Errorf("remote=%s xff=%q: got %s, want %s", tc.remote, tc.forwarded, got, tc.want)
		}
	}
}
