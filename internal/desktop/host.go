// Package desktop runs the certificate mailer inside a desktop window. Host is
// the window's single HTTP handler: it serves the exported UI, the first-run
// setup endpoints, and, once PostgreSQL and Redis are reachable, the API.
package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/mayura-andrew/email-client/internal/certificate"
	"github.com/mayura-andrew/email-client/internal/jsonlog"
	"github.com/mayura-andrew/email-client/internal/server"
	"github.com/mayura-andrew/email-client/internal/validator"
	"golang.org/x/crypto/bcrypt"
)

type State string

const (
	// StateSetup: connections or the admin account are not configured yet.
	StateSetup    State = "setup"
	StateStarting State = "starting"
	StateRunning  State = "running"
	// StateError: configured, but PostgreSQL or Redis cannot be reached.
	StateError State = "error"
)

// stopTimeout bounds how long closing the window waits for in-flight sends;
// anything cut off is retried on the next start.
const stopTimeout = 10 * time.Second

const minPasswordLength = 10

type Host struct {
	dir        string
	assets     fs.FS
	migrations fs.FS
	template   []byte
	logger     *jsonlog.Logger

	mu      sync.Mutex
	cfg     Config
	state   State
	lastErr string
	srv     *server.Server
	// gen discards a start that finishes after a newer restart or Stop.
	gen int
}

// New loads the config from dir. assets is the exported UI, migrations the
// *.sql files, template the default certificate background (JPG).
func New(dir string, assets, migrations fs.FS, template []byte, logger *jsonlog.Logger) (*Host, error) {
	cfg, err := loadConfig(dir)
	if err != nil {
		return nil, err
	}
	return &Host{dir: dir, assets: assets, migrations: migrations, template: template, logger: logger,
		cfg: cfg, state: StateSetup}, nil
}

// Start connects in the background when the app is configured.
func (h *Host) Start() { h.restart() }

// Stop shuts the API and its workers down.
func (h *Host) Stop() {
	h.mu.Lock()
	old := h.srv
	h.srv, h.gen = nil, h.gen+1
	h.mu.Unlock()
	if old != nil {
		old.Stop(stopTimeout)
	}
}

// Sending reports whether certificates are still waiting to be sent.
func (h *Host) Sending(ctx context.Context) bool {
	srv := h.server()
	if srv == nil {
		return false
	}
	sending, err := srv.Sending(ctx)
	return err == nil && sending
}

func (h *Host) server() *server.Server {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.srv
}

func (h *Host) restart() {
	h.mu.Lock()
	old := h.srv
	h.srv, h.gen = nil, h.gen+1
	gen, cfg := h.gen, h.cfg
	if !cfg.connected() || !cfg.hasAdmin() {
		h.state, h.lastErr = StateSetup, ""
		h.mu.Unlock()
		if old != nil {
			old.Stop(stopTimeout)
		}
		return
	}
	h.state, h.lastErr = StateStarting, ""
	h.mu.Unlock()

	go func() {
		if old != nil {
			old.Stop(stopTimeout)
		}
		srv, err := server.Start(context.Background(), h.options(cfg))
		h.mu.Lock()
		defer h.mu.Unlock()
		if gen != h.gen {
			if srv != nil {
				go srv.Stop(stopTimeout)
			}
			return
		}
		if err != nil {
			h.state, h.lastErr = StateError, err.Error()
			h.logger.PrintError(err, map[string]string{"component": "startup"})
			return
		}
		h.srv, h.state = srv, StateRunning
		h.logger.PrintInfo("connected", nil)
	}()
}

func (h *Host) options(cfg Config) server.Options {
	delivery := server.DefaultDelivery()
	delivery.RatePerSecond, delivery.DailyLimit = cfg.SMTPRatePerSecond, cfg.SMTPDailyLimit
	layout := server.DefaultCertificate()
	if c := cfg.Certificate; c != nil {
		layout = certificate.Config{NameX: c.NameX, NameY: c.NameY, NameSize: c.NameSize,
			NumberX: c.NumberX, NumberY: c.NumberY, NumberSize: c.NumberSize}
	}
	return server.Options{
		DatabaseURL: cfg.DatabaseURL, RedisURL: cfg.RedisURL,
		AdminEmail: cfg.AdminEmail, AdminPasswordHash: cfg.AdminPasswordHash,
		Secret: []byte(cfg.Secret), Logger: h.logger, Migrations: h.migrations,
		Template:    certificate.Template{Filename: "template.jpg", Content: h.template},
		Certificate: layout, Delivery: delivery,
	}
}

func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch p := r.URL.Path; {
	case p == "/api/v1/setup" || strings.HasPrefix(p, "/api/v1/setup/"):
		h.setup(w, r)
	case strings.HasPrefix(p, "/api/") || p == "/debug/vars":
		if srv := h.server(); srv != nil {
			srv.ServeHTTP(w, r)
			return
		}
		h.mu.Lock()
		state := h.state
		h.mu.Unlock()
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "The app is not connected to its database yet.", "setup": state})
	default:
		h.serveAsset(w, r)
	}
}

// serveAsset serves the statically exported UI: /campaigns/new is
// campaigns/new.html, and unknown paths get the exported 404 page.
func (h *Host) serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	candidates := []string{name, name + ".html", path.Join(name, "index.html")}
	if name == "" {
		candidates = []string{"index.html"}
	}
	for _, candidate := range candidates {
		content, err := fs.ReadFile(h.assets, candidate)
		if err == nil {
			http.ServeContent(w, r, candidate, time.Time{}, bytes.NewReader(content))
			return
		}
	}
	content, err := fs.ReadFile(h.assets, "404.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(content)
}

// ---------------------------------------------------------------------------
// Setup endpoints
// ---------------------------------------------------------------------------

func (h *Host) setup(w http.ResponseWriter, r *http.Request) {
	switch route := r.Method + " " + r.URL.Path; route {
	case "GET /api/v1/setup/status":
		h.status(w)
	case "POST /api/v1/setup/test":
		h.test(w, r)
	case "POST /api/v1/setup":
		h.save(w, r)
	case "POST /api/v1/setup/retry":
		h.retry(w)
	case "GET /api/v1/setup/connections":
		h.connections(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
}

func (h *Host) status(w http.ResponseWriter) {
	h.mu.Lock()
	cfg, state, lastErr := h.cfg, h.state, h.lastErr
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"state":            state,
		"error":            lastErr,
		"needsConnections": !cfg.connected(),
		"needsAdmin":       !cfg.hasAdmin(),
		"databaseUrl":      redact(cfg.DatabaseURL),
		"redisUrl":         redact(cfg.RedisURL),
	})
}

// access reports whether r may change the setup. While the app is running only
// a signed-in admin may; before that anyone at this computer may fix the
// connections, which the config file already allows.
func (h *Host) access(r *http.Request) (cfg Config, running, signedIn bool) {
	h.mu.Lock()
	cfg, srv := h.cfg, h.srv
	h.mu.Unlock()
	if srv == nil {
		return cfg, false, false
	}
	return cfg, true, srv.Authenticated(r)
}

type setupRequest struct {
	DatabaseURL       string   `json:"databaseUrl"`
	RedisURL          string   `json:"redisUrl"`
	AdminEmail        string   `json:"adminEmail"`
	AdminPassword     string   `json:"adminPassword"`
	CurrentPassword   string   `json:"currentPassword"`
	Secret            string   `json:"secret"`
	SMTPRatePerSecond *float64 `json:"smtpRatePerSecond"`
	SMTPDailyLimit    *int     `json:"smtpDailyLimit"`
}

func (h *Host) test(w http.ResponseWriter, r *http.Request) {
	cfg, running, signedIn := h.access(r)
	if running && !signedIn {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	var req setupRequest
	if !readJSON(w, r, &req) {
		return
	}
	databaseURL, redisURL := orDefault(req.DatabaseURL, cfg.DatabaseURL), orDefault(req.RedisURL, cfg.RedisURL)
	problems := checkConnections(r.Context(), databaseURL, redisURL)
	result := map[string]string{"databaseUrl": "ok", "redisUrl": "ok"}
	for key, problem := range problems {
		result[key] = problem
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Host) save(w http.ResponseWriter, r *http.Request) {
	cfg, running, signedIn := h.access(r)
	if running && !signedIn {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	var req setupRequest
	if !readJSON(w, r, &req) {
		return
	}

	next := cfg
	next.DatabaseURL = orDefault(req.DatabaseURL, cfg.DatabaseURL)
	next.RedisURL = orDefault(req.RedisURL, cfg.RedisURL)
	problems := map[string]string{}
	if next.DatabaseURL == "" {
		problems["databaseUrl"] = "Enter the PostgreSQL connection URL"
	}
	if next.RedisURL == "" {
		problems["redisUrl"] = "Enter the Redis connection URL"
	}

	// Changing the secret would make the stored SMTP password unreadable.
	if secret := strings.TrimSpace(req.Secret); secret != "" && secret != cfg.Secret {
		switch {
		case cfg.Secret != "":
			problems["secret"] = "The encryption key can only be set during first setup"
		case len(secret) < 32:
			problems["secret"] = "The encryption key must be at least 32 characters"
		default:
			next.Secret = secret
		}
	}
	if next.Secret == "" {
		secret, err := newSecret()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		next.Secret = secret
	}

	if req.AdminEmail != "" || req.AdminPassword != "" {
		if cfg.hasAdmin() {
			if !signedIn {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Sign in to change the admin account"})
				return
			}
			if bcrypt.CompareHashAndPassword([]byte(cfg.AdminPasswordHash), []byte(req.CurrentPassword)) != nil {
				problems["currentPassword"] = "Current password is wrong"
			}
		}
		if email := strings.ToLower(strings.TrimSpace(req.AdminEmail)); email != "" {
			if problem := validator.EmailProblem(email); problem != "" {
				problems["adminEmail"] = problem
			}
			next.AdminEmail = email
		}
		if req.AdminPassword != "" {
			if len(req.AdminPassword) < minPasswordLength {
				problems["adminPassword"] = "Use at least 10 characters"
			} else if hash, err := bcrypt.GenerateFromPassword([]byte(req.AdminPassword), bcrypt.DefaultCost); err != nil {
				problems["adminPassword"] = err.Error()
			} else {
				next.AdminPasswordHash = string(hash)
			}
		}
	}
	if next.AdminEmail == "" {
		problems["adminEmail"] = "Enter the admin email"
	}
	if next.AdminPasswordHash == "" && problems["adminPassword"] == "" {
		problems["adminPassword"] = "Choose an admin password"
	}

	if req.SMTPRatePerSecond != nil {
		if *req.SMTPRatePerSecond < 0 {
			problems["smtpRatePerSecond"] = "Must be 0 (unlimited) or more"
		}
		next.SMTPRatePerSecond = *req.SMTPRatePerSecond
	}
	if req.SMTPDailyLimit != nil {
		if *req.SMTPDailyLimit < 0 {
			problems["smtpDailyLimit"] = "Must be 0 (unlimited) or more"
		}
		next.SMTPDailyLimit = *req.SMTPDailyLimit
	}
	if len(problems) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": problems})
		return
	}

	if !running || next.DatabaseURL != cfg.DatabaseURL || next.RedisURL != cfg.RedisURL {
		if problems := checkConnections(r.Context(), next.DatabaseURL, next.RedisURL); len(problems) > 0 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": problems})
			return
		}
	}
	if err := saveConfig(h.dir, next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "saving the configuration: " + err.Error()})
		return
	}
	h.mu.Lock()
	h.cfg = next
	h.mu.Unlock()
	h.restart()
	writeJSON(w, http.StatusAccepted, map[string]any{"state": StateStarting})
}

func (h *Host) retry(w http.ResponseWriter) {
	h.mu.Lock()
	state := h.state
	h.mu.Unlock()
	if state == StateError {
		h.restart()
	}
	h.status(w)
}

func (h *Host) connections(w http.ResponseWriter, r *http.Request) {
	cfg, running, signedIn := h.access(r)
	if !running || !signedIn {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"databaseUrl":       redact(cfg.DatabaseURL),
		"redisUrl":          redact(cfg.RedisURL),
		"adminEmail":        cfg.AdminEmail,
		"secret":            cfg.Secret,
		"smtpRatePerSecond": cfg.SMTPRatePerSecond,
		"smtpDailyLimit":    cfg.SMTPDailyLimit,
		"configDir":         h.dir,
	})
}

// checkConnections tries both services in parallel and returns a problem per
// field that failed.
func checkConnections(ctx context.Context, databaseURL, redisURL string) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var dbErr, redisErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		db, err := server.OpenDB(ctx, databaseURL)
		if err == nil {
			db.Close()
		}
		dbErr = err
	}()
	go func() {
		defer wg.Done()
		rdb, err := server.OpenRedis(ctx, redisURL)
		if err == nil {
			rdb.Close()
		}
		redisErr = err
	}()
	wg.Wait()
	problems := map[string]string{}
	if dbErr != nil {
		problems["databaseUrl"] = "PostgreSQL: " + dbErr.Error()
	}
	if redisErr != nil {
		problems["redisUrl"] = "Redis: " + redisErr.Error()
	}
	return problems
}

func orDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var syntax *json.SyntaxError
		message := err.Error()
		if errors.As(err, &syntax) {
			message = "body contains badly-formed JSON"
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": message})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
