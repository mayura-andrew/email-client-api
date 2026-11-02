package desktop

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mayura-andrew/email-client/internal/jsonlog"
	"github.com/mayura-andrew/email-client/migrations"
)

var ui = fstest.MapFS{
	"index.html":         {Data: []byte("home")},
	"campaigns.html":     {Data: []byte("campaign list")},
	"campaigns/new.html": {Data: []byte("new campaign")},
	"404.html":           {Data: []byte("not found page")},
	"_next/static/a.js":  {Data: []byte("js")},
}

func newHost(t *testing.T) (*Host, string) {
	t.Helper()
	dir := t.TempDir()
	host, err := New(dir, ui, migrations.FS, nil, jsonlog.New(io.Discard, jsonlog.LevelOff))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Stop)
	return host, dir
}

func do(t *testing.T, h http.Handler, method, path, body string, headers ...string) (int, map[string]any, string) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var payload map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &payload)
	return w.Code, payload, w.Body.String()
}

func TestAssets(t *testing.T) {
	host, _ := newHost(t)
	cases := map[string]struct {
		code int
		body string
	}{
		"/":                  {200, "home"},
		"/campaigns":         {200, "campaign list"},
		"/campaigns/new":     {200, "new campaign"},
		"/campaigns/new?x=1": {200, "new campaign"},
		"/_next/static/a.js": {200, "js"},
		"/missing":           {404, "not found page"},
		"/../../etc/passwd":  {404, "not found page"},
	}
	for path, want := range cases {
		code, _, body := do(t, host, "GET", path, "")
		if code != want.code || body != want.body {
			t.Errorf("%s: got %d %q, want %d %q", path, code, body, want.code, want.body)
		}
	}
}

func TestUnconfigured(t *testing.T) {
	host, _ := newHost(t)
	host.Start()

	code, status, _ := do(t, host, "GET", "/api/v1/setup/status", "")
	if code != 200 || status["state"] != "setup" || status["needsConnections"] != true || status["needsAdmin"] != true {
		t.Fatalf("status = %d %v", code, status)
	}
	// The API tells the UI to go to setup instead of failing silently.
	code, payload, _ := do(t, host, "GET", "/api/v1/campaigns", "")
	if code != http.StatusServiceUnavailable || payload["setup"] != "setup" {
		t.Fatalf("api before setup = %d %v", code, payload)
	}
	// Connection details are only shown to a signed-in admin.
	if code, _, _ := do(t, host, "GET", "/api/v1/setup/connections", ""); code != http.StatusUnauthorized {
		t.Fatalf("connections before setup = %d", code)
	}
}

func TestSaveValidates(t *testing.T) {
	host, dir := newHost(t)
	code, payload, _ := do(t, host, "POST", "/api/v1/setup", `{"adminEmail":"not-an-email","adminPassword":"short","secret":"too-short"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d %v", code, payload)
	}
	problems := payload["error"].(map[string]any)
	for _, field := range []string{"databaseUrl", "redisUrl", "adminEmail", "adminPassword", "secret"} {
		if problems[field] == nil {
			t.Errorf("no problem reported for %s: %v", field, problems)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, configFile)); err == nil {
		t.Fatal("an invalid setup was saved")
	}
}

func TestSaveRejectsUnreachableServers(t *testing.T) {
	host, dir := newHost(t)
	body := `{"databaseUrl":"postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=2","redisUrl":"redis://127.0.0.1:1/0",
		"adminEmail":"admin@example.com","adminPassword":"correct horse battery"}`
	start := time.Now()
	code, payload, _ := do(t, host, "POST", "/api/v1/setup", body)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d %v", code, payload)
	}
	problems := payload["error"].(map[string]any)
	if !strings.HasPrefix(problems["databaseUrl"].(string), "PostgreSQL: ") || !strings.HasPrefix(problems["redisUrl"].(string), "Redis: ") {
		t.Fatalf("problems = %v", problems)
	}
	if time.Since(start) > 25*time.Second {
		t.Fatal("connection check did not time out")
	}
	if _, err := os.Stat(filepath.Join(dir, configFile)); err == nil {
		t.Fatal("an unreachable setup was saved")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Config{DatabaseURL: "postgres://u:secret@h/db", RedisURL: "rediss://default:pw@h:1", AdminEmail: "a@b.co",
		AdminPasswordHash: "hash", Secret: strings.Repeat("k", 43), SMTPRatePerSecond: 1.5, SMTPDailyLimit: 300}
	if err := saveConfig(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(dir)
	if err != nil || got != want {
		t.Fatalf("got %+v, %v", got, err)
	}
	info, err := os.Stat(filepath.Join(dir, configFile))
	if err != nil || (os.PathSeparator == '/' && info.Mode().Perm() != 0o600) {
		t.Fatalf("config file mode = %v, %v", info.Mode(), err)
	}
	if redacted := redact(want.DatabaseURL); strings.Contains(redacted, "secret") {
		t.Fatalf("password not redacted: %s", redacted)
	}
}

// TestFullSetup runs first-run setup against real services when
// DESKTOP_TEST_DATABASE_URL and DESKTOP_TEST_REDIS_URL point at throwaway ones.
func TestFullSetup(t *testing.T) {
	databaseURL, redisURL := os.Getenv("DESKTOP_TEST_DATABASE_URL"), os.Getenv("DESKTOP_TEST_REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		t.Skip("set DESKTOP_TEST_DATABASE_URL and DESKTOP_TEST_REDIS_URL to run")
	}
	host, dir := newHost(t)
	body, _ := json.Marshal(map[string]string{"databaseUrl": databaseURL, "redisUrl": redisURL,
		"adminEmail": "Admin@Example.com", "adminPassword": "correct horse battery"})
	if code, payload, _ := do(t, host, "POST", "/api/v1/setup", string(body)); code != http.StatusAccepted {
		t.Fatalf("setup = %d %v", code, payload)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, status, _ := do(t, host, "GET", "/api/v1/setup/status", "")
		if status["state"] == "running" {
			break
		}
		if status["state"] == "error" || time.Now().After(deadline) {
			t.Fatalf("did not start: %v", status)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if cfg, _ := loadConfig(dir); cfg.AdminPasswordHash == "" || strings.Contains(cfg.AdminPasswordHash, "battery") || len(cfg.Secret) < 32 {
		t.Fatalf("saved config = %+v", cfg)
	}

	if code, _, _ := do(t, host, "POST", "/api/v1/auth/login", `{"email":"admin@example.com","password":"wrong password!"}`); code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", code)
	}
	code, payload, _ := do(t, host, "POST", "/api/v1/auth/login", `{"email":"admin@example.com","password":"correct horse battery"}`)
	token, _ := payload["token"].(string)
	if code != 200 || token == "" {
		t.Fatalf("login = %d %v", code, payload)
	}
	auth := []string{"Authorization", "Bearer " + token}
	if code, _, _ := do(t, host, "GET", "/api/v1/campaigns", ""); code != http.StatusUnauthorized {
		t.Fatalf("campaigns without token = %d", code)
	}
	if code, payload, _ := do(t, host, "GET", "/api/v1/campaigns", "", auth...); code != 200 {
		t.Fatalf("campaigns = %d %v", code, payload)
	}
	// Once running, setup changes need a session.
	if code, _, _ := do(t, host, "POST", "/api/v1/setup", `{"smtpDailyLimit":300}`); code != http.StatusUnauthorized {
		t.Fatalf("anonymous change = %d", code)
	}
	if code, payload, _ := do(t, host, "GET", "/api/v1/setup/connections", "", auth...); code != 200 || strings.Contains(payload["databaseUrl"].(string), "@") && !strings.Contains(payload["databaseUrl"].(string), "xxxxx") {
		t.Fatalf("connections = %d %v", code, payload)
	}
}
