// Package server is the certificate mailer's HTTP API and delivery workers. The
// desktop app starts one Server once the PostgreSQL and Redis connections are
// configured, and stops it when the window closes or the connections change.
package server

import (
	"context"
	"database/sql"
	"errors"
	"expvar"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
	"github.com/mayura-andrew/email-client/internal/certificate"
	"github.com/mayura-andrew/email-client/internal/data"
	"github.com/mayura-andrew/email-client/internal/delivery"
	"github.com/mayura-andrew/email-client/internal/jsonlog"
	"github.com/mayura-andrew/email-client/internal/mailer"
	"github.com/mayura-andrew/email-client/internal/outbox"
	"github.com/mayura-andrew/email-client/internal/ratelimit"
	"github.com/mayura-andrew/email-client/internal/session"
	"github.com/mayura-andrew/email-client/internal/settings"
	"github.com/mayura-andrew/email-client/internal/vcs"
	"github.com/redis/go-redis/v9"
)

var version = vcs.Version()

func init() {
	expvar.NewString("version").Set(version)
	expvar.Publish("goroutines", expvar.Func(func() any { return runtime.NumGoroutine() }))
	expvar.Publish("timestamp", expvar.Func(func() any { return time.Now().Unix() }))
}

const (
	outboxStream = "outbox:messages"
	outboxGroup  = "mailers"
	// Hosted free tiers allow few connections (Aiven free: 20).
	maxOpenConns = 10
	maxIdleConns = 5
)

// Delivery tunes sending. Rate and daily limit are global across every running
// copy of the app that shares the same Redis.
type Delivery struct {
	Workers       int
	MaxInFlight   int
	MaxAttempts   int
	Lease         time.Duration
	RatePerSecond float64 // 0 = unlimited
	Burst         int
	DailyLimit    int // 0 = unlimited
}

func DefaultDelivery() Delivery {
	return Delivery{Workers: 4, MaxInFlight: 100, MaxAttempts: 5, Lease: 2 * time.Minute, RatePerSecond: 2, Burst: 1}
}

func DefaultCertificate() certificate.Config {
	return certificate.Config{NameX: 0, NameY: 60, NameSize: 24, NumberX: 15, NumberY: 15, NumberSize: 10}
}

// Options configures one running Server.
type Options struct {
	DatabaseURL string
	RedisURL    string
	// AdminEmail and AdminPasswordHash (bcrypt) are the only login.
	AdminEmail        string
	AdminPasswordHash string
	// Secret encrypts the SMTP password stored in PostgreSQL.
	Secret []byte
	Logger *jsonlog.Logger
	// Migrations holds the *.sql files applied on start.
	Migrations fs.FS
	// Template is the certificate background used until an admin uploads one.
	Template    certificate.Template
	Certificate certificate.Config
	Delivery    Delivery
}

type config struct {
	env       string
	publicURL string
	// trackingRedirectURL is where tracked-email link clicks land; empty serves a pixel.
	trackingRedirectURL string

	limiter struct {
		// trustedProxies are allowed to set X-Forwarded-For.
		trustedProxies []*net.IPNet
	}

	admin struct {
		email        string
		passwordHash string
	}

	delivery Delivery
}

type application struct {
	config   config
	logger   *jsonlog.Logger
	db       *sql.DB
	redis    *redis.Client
	models   data.Models
	settings *settings.Store
	sessions *session.Store
	limiter  *ratelimit.Limiter
}

// Server is a running API with its delivery workers.
type Server struct {
	app        *application
	handler    http.Handler
	cancel     context.CancelFunc
	background sync.WaitGroup
}

// Start connects to PostgreSQL and Redis, applies migrations and starts the
// delivery workers. On error nothing is left running.
func Start(ctx context.Context, opts Options) (*Server, error) {
	if len(opts.Secret) < 32 {
		return nil, errors.New("the encryption secret must be at least 32 bytes")
	}
	db, err := OpenDB(ctx, opts.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("PostgreSQL: %w", err)
	}
	schema, err := runMigration(db, opts.Migrations)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("database migrations: %w", err)
	}
	opts.Logger.PrintInfo("database migrations applied", map[string]string{"version": fmt.Sprint(schema)})

	rdb, err := OpenRedis(ctx, opts.RedisURL)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("Redis: %w", err)
	}

	cfg := config{env: "desktop", delivery: opts.Delivery}
	cfg.admin.email, cfg.admin.passwordHash = opts.AdminEmail, opts.AdminPasswordHash
	certificateConfig := opts.Certificate
	certificateConfig.Template = opts.Template

	models := data.NewModel(db)
	settingsStore := settings.New(models.Settings, rdb, opts.Secret, opts.Logger, mailer.Config{}, certificateConfig)
	if err := settingsStore.Reload(ctx); err != nil {
		db.Close()
		rdb.Close()
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	app := &application{
		config:   cfg,
		logger:   opts.Logger,
		db:       db,
		redis:    rdb,
		models:   models,
		settings: settingsStore,
		sessions: &session.Store{Redis: rdb, TTL: 12 * time.Hour},
		limiter:  &ratelimit.Limiter{Redis: rdb, Prefix: "ratelimit:"},
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s := &Server{app: app, handler: app.routes(), cancel: cancel}
	app.startBackground(runCtx, &s.background)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Stop halts the workers, waiting up to timeout for in-flight sends. A send cut
// off by the timeout is retried by the reaper on the next start.
func (s *Server) Stop(timeout time.Duration) {
	s.cancel()
	done := make(chan struct{})
	go func() { s.background.Wait(); close(done) }()
	select {
	case <-done:
		s.app.logger.PrintInfo("background workers stopped", nil)
	case <-time.After(timeout):
		s.app.logger.PrintError(errors.New("background workers did not stop in time"), nil)
	}
	s.app.db.Close()
	s.app.redis.Close()
}

// Sending reports whether any certificate or email is still waiting to be sent.
func (s *Server) Sending(ctx context.Context) (bool, error) {
	var sending bool
	err := s.app.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM outbox_messages
		WHERE status IN ('pending', 'published', 'sending'))`).Scan(&sending)
	return sending, err
}

func (app *application) startBackground(ctx context.Context, wg *sync.WaitGroup) {
	run := func(name string, fn func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					app.logger.PrintError(fmt.Errorf("%v", r), map[string]string{"component": name, "event": "panic"})
				}
			}()
			fn(ctx)
		}()
	}

	hooks := delivery.Hooks()
	hostname, _ := os.Hostname()
	service := &delivery.Service{
		Campaigns: app.models.Campaigns,
		Emails:    app.models.Emails,
		Settings:  app.settings,
		Limiter:   app.limiter,
		SendRate:  ratelimit.Limit{Rate: app.config.delivery.RatePerSecond, Burst: app.config.delivery.Burst},
		Quota:     &ratelimit.Quota{Redis: app.redis, Prefix: "smtp:quota:", Daily: app.config.delivery.DailyLimit},
		PublicURL: strings.TrimRight(app.config.publicURL, "/"),
	}

	run("settings-watch", app.settings.Watch)
	run("outbox-relay", (&outbox.Relay{
		DB: app.db, Redis: app.redis, Stream: outboxStream, Logger: app.logger,
		BatchSize: 100, MaxInFlight: app.config.delivery.MaxInFlight, Interval: 2 * time.Second,
	}).Run)
	run("outbox-worker", func(ctx context.Context) {
		worker := &outbox.Worker{
			DB: app.db, Redis: app.redis, Stream: outboxStream, Group: outboxGroup,
			ConsumerName: fmt.Sprintf("%s-%d", hostname, os.Getpid()),
			Concurrency:  app.config.delivery.Workers, Lease: app.config.delivery.Lease,
			NewProcessor: service.NewProcessor, Hooks: hooks, Logger: app.logger,
		}
		if err := worker.Run(ctx); err != nil {
			app.logger.PrintError(err, map[string]string{"component": "outbox-worker"})
		}
	})
	run("outbox-reaper", func(ctx context.Context) {
		(&outbox.Reaper{
			DB: app.db, Redis: app.redis, Stream: outboxStream, Group: outboxGroup, Hooks: hooks, Logger: app.logger,
			RepublishAfter: 15 * time.Minute,
		}).Run(ctx, 15*time.Second)
	})
	run("campaign-completion", func(ctx context.Context) {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := app.models.Campaigns.CompleteFinished(ctx); err != nil && ctx.Err() == nil {
					app.logger.PrintError(err, map[string]string{"component": "campaign-completion"})
				}
			}
		}
	})
}

// OpenDB connects to PostgreSQL once; the caller decides whether to retry.
func OpenDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxIdleTime(15 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// OpenRedis connects to Redis once; the caller decides whether to retry.
func OpenRedis(ctx context.Context, url string) (*redis.Client, error) {
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(options)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		rdb.Close()
		return nil, err
	}
	return rdb, nil
}

// runMigration applies pending migrations and returns the schema version.
func runMigration(db *sql.DB, migrations fs.FS) (uint, error) {
	source, err := iofs.New(migrations, ".")
	if err != nil {
		return 0, err
	}
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return 0, err
	}
	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return 0, err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return 0, err
	}
	version, _, err := m.Version()
	return version, err
}

func parseCIDRs(value string, dst *[]*net.IPNet) error {
	for _, field := range strings.Fields(value) {
		if !strings.Contains(field, "/") {
			if strings.Contains(field, ":") {
				field += "/128"
			} else {
				field += "/32"
			}
		}
		_, network, err := net.ParseCIDR(field)
		if err != nil {
			return err
		}
		*dst = append(*dst, network)
	}
	return nil
}
