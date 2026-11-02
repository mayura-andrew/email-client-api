package main

import (
	"context"
	"database/sql"
	"errors"
	"expvar"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/joho/godotenv"
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

var (
	version = vcs.Version()
)

type config struct {

	// API SERVER - configurations
	port      int
	env       string
	publicURL string
	migrate   bool
	// trackingRedirectURL is where tracked-email link clicks land; empty serves a pixel.
	trackingRedirectURL string

	// SMTP - defaults until an admin saves settings in the UI
	smtp mailer.Config

	cors struct {
		trustedOrigns []string
	}

	db struct {
		dsn          string
		maxOpenConns int
		maxIdleConns int
		maxIdleTime  string
	}

	redisURL string

	limiter struct {
		rps     float64
		burst   int
		enabled bool
		// trustedProxies are allowed to set X-Forwarded-For.
		trustedProxies []*net.IPNet
	}

	admin struct {
		email        string
		password     string
		secret       []byte
		cookieSecure bool
	}

	// delivery tunes the outbox relay and worker pool
	delivery struct {
		workers       int
		maxInFlight   int
		maxAttempts   int
		lease         time.Duration
		ratePerSecond float64
		burst         int
		dailyLimit    int
	}

	certificate         certificate.Config
	certificateTemplate string
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

const (
	outboxStream = "outbox:messages"
	outboxGroup  = "mailers"
)

func main() {
	var cfg config

	// .env is optional; containers receive configuration through the environment.
	if err := godotenv.Load(".env"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Fatalf("Error loading environment variables file: %v", err)
	}

	flag.IntVar(&cfg.port, "port", envInt("PORT", 4000), "Email API Server Port")
	flag.StringVar(&cfg.env, "env", envString("APP_ENV", "development"), "Environment (development|statging|production)")
	flag.StringVar(&cfg.publicURL, "public-url", envString("URL", "http://localhost:4000"), "Public base URL used in tracking links")
	flag.StringVar(&cfg.trackingRedirectURL, "tracking-redirect-url", os.Getenv("TRACKING_REDIRECT_URL"), "Where tracked-email link clicks are redirected")
	flag.BoolVar(&cfg.migrate, "migrate", envBool("RUN_MIGRATIONS", true), "Apply database migrations on startup")
	flag.StringVar(&cfg.db.dsn, "db-dsn", os.Getenv("DB_DSN"),
		"PostgreSQL DSN")

	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", 25, "PostgreSQL max open connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", 25, "PostgreSQL max idle connections")
	flag.StringVar(&cfg.db.maxIdleTime, "db-max-idle-times", "15m", "PostgreSQL max connection idle time")
	flag.StringVar(&cfg.redisURL, "redis-url", envString("REDIS_URL", "redis://localhost:6379/0"), "Redis URL")

	cfg.admin.email = os.Getenv("ADMIN_EMAIL")
	cfg.admin.password = os.Getenv("ADMIN_PASSWORD")
	cfg.admin.secret = []byte(os.Getenv("ADMIN_SESSION_SECRET"))
	flag.BoolVar(&cfg.admin.cookieSecure, "cookie-secure", envBool("COOKIE_SECURE", false), "Mark the session cookie Secure (enable behind HTTPS)")

	cfg.certificateTemplate = envString("CERTIFICATE_TEMPLATE", "./assests/template.jpg")
	cfg.certificate = certificate.Config{
		NameX:    envFloat("CERTIFICATE_NAME_X", 0),
		NameY:    envFloat("CERTIFICATE_NAME_Y", 60),
		NameSize: envFloat("CERTIFICATE_NAME_SIZE", 24),
		// mm from the bottom-left corner
		NumberX:    envFloat("CERTIFICATE_NUMBER_X", 15),
		NumberY:    envFloat("CERTIFICATE_NUMBER_Y", 15),
		NumberSize: envFloat("CERTIFICATE_NUMBER_SIZE", 10),
	}

	flag.Float64Var(&cfg.limiter.rps, "limiter-rps", envFloat("API_RATE_LIMIT_RPS", 10), "Rate limiter maximum requests per second per client")
	flag.IntVar(&cfg.limiter.burst, "limiter-burst", envInt("API_RATE_LIMIT_BURST", 20), "Rate limiter maximum burst")
	flag.BoolVar(&cfg.limiter.enabled, "limiter-enabled", true, "Enable rate limiter")
	flag.Func("trusted-proxies", "CIDRs allowed to set X-Forwarded-For (space separated)", func(val string) error {
		return parseCIDRs(val, &cfg.limiter.trustedProxies)
	})
	if err := parseCIDRs(os.Getenv("TRUSTED_PROXIES"), &cfg.limiter.trustedProxies); err != nil {
		log.Fatalf("Invalid TRUSTED_PROXIES: %v", err)
	}

	smtpSender, err := url.QueryUnescape(os.Getenv("SMTPSENDER"))
	if err != nil {
		log.Fatalf("Failed to decore the SMTPSENDER : %v", err)
	}
	flag.StringVar(&cfg.smtp.Host, "SMTPHOST", os.Getenv("SMTPHOST"), "SMTP host")
	flag.IntVar(&cfg.smtp.Port, "SMTPPORT", envInt("SMTPPORT", 587), "SMTP port")
	flag.StringVar(&cfg.smtp.Username, "SMTPUSERNAME", os.Getenv("SMTPUSERNAME"), "SMTP username")
	flag.StringVar(&cfg.smtp.Password, "SMTPPASS",
		os.Getenv("SMTPPASS"), "SMTP password")
	flag.StringVar(&cfg.smtp.Sender, "SMTPSENDER", smtpSender, "SMTP sender")

	flag.IntVar(&cfg.delivery.workers, "workers", envInt("WORKER_CONCURRENCY", 4), "Concurrent SMTP workers per replica")
	flag.IntVar(&cfg.delivery.maxInFlight, "outbox-max-inflight", envInt("OUTBOX_MAX_INFLIGHT", 100), "Maximum published-but-unfinished outbox messages")
	flag.IntVar(&cfg.delivery.maxAttempts, "outbox-max-attempts", envInt("OUTBOX_MAX_ATTEMPTS", 5), "Delivery attempts before a message fails")
	flag.DurationVar(&cfg.delivery.lease, "outbox-lease", envDuration("OUTBOX_LEASE", 2*time.Minute), "How long a worker owns a claimed message")
	flag.Float64Var(&cfg.delivery.ratePerSecond, "smtp-rate", envFloat("SMTP_RATE_PER_SECOND", 2), "Global SMTP sends per second across all replicas (0 = unlimited)")
	flag.IntVar(&cfg.delivery.burst, "smtp-burst", envInt("SMTP_BURST", 1), "Global SMTP send burst")
	flag.IntVar(&cfg.delivery.dailyLimit, "smtp-daily-limit", envInt("SMTP_DAILY_LIMIT", 0), "Global SMTP sends per UTC day (0 = unlimited)")

	cfg.cors.trustedOrigns = strings.Fields(envString("CORS_TRUSTED_ORIGINS", "http://localhost:3000 http://localhost:5173"))
	flag.Func("cors-trusted-origins", "Trusted CORS origins (space separated)", func(val string) error {
		cfg.cors.trustedOrigns = strings.Fields(val)
		return nil
	})

	displayVersion := flag.Bool("version", false, "Display version and exit")
	flag.Parse()

	if *displayVersion {
		fmt.Printf("Version: \t%s\n", version)
		os.Exit(0)
	}

	logger := jsonlog.New(os.Stdout, jsonlog.LevelInfo)

	if len(cfg.admin.secret) == 0 {
		logger.PrintFatal(errors.New("ADMIN_SESSION_SECRET must be set; it encrypts the stored SMTP password"), nil)
	}
	if len(cfg.admin.secret) < 32 {
		logger.PrintError(errors.New("ADMIN_SESSION_SECRET is shorter than 32 characters"), map[string]string{"component": "config"})
	}
	if cfg.admin.email == "" || cfg.admin.password == "" {
		logger.PrintFatal(errors.New("ADMIN_EMAIL and ADMIN_PASSWORD must be set"), nil)
	}

	expvar.NewString("version").Set(version)

	expvar.Publish("goroutines", expvar.Func(func() any {
		return runtime.NumGoroutine()
	}))

	expvar.Publish("timestamp", expvar.Func(func() any {
		return time.Now().Unix()
	}))

	db, err := openDB(cfg)

	if err != nil {
		logger.PrintFatal(err, map[string]string{})
	}
	defer db.Close()

	logger.PrintInfo("database connection pool established", map[string]string{})

	if cfg.migrate {
		if err := runMigration(db); err != nil {
			logger.PrintFatal(err, map[string]string{})
		}
	}

	rdb, err := openRedis(cfg)
	if err != nil {
		logger.PrintFatal(err, map[string]string{"component": "redis"})
	}
	defer rdb.Close()
	logger.PrintInfo("redis connection established", map[string]string{})

	models := data.NewModel(db)
	settingsStore := settings.New(models.Settings, rdb, cfg.admin.secret, logger, cfg.smtp, cfg.certificate, cfg.certificateTemplate)
	if err := settingsStore.Reload(context.Background()); err != nil {
		logger.PrintFatal(err, map[string]string{"component": "settings"})
	}

	app := &application{
		config:   cfg,
		logger:   logger,
		db:       db,
		redis:    rdb,
		models:   models,
		settings: settingsStore,
		sessions: &session.Store{Redis: rdb, TTL: 12 * time.Hour},
		limiter:  &ratelimit.Limiter{Redis: rdb, Prefix: "ratelimit:"},
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var background sync.WaitGroup
	app.startBackground(ctx, &background)

	err = app.serve(ctx)
	stop()
	if err != nil {
		logger.PrintError(err, nil)
	}

	// Workers finish their in-flight send (bounded by the lease) before exit, so
	// a deploy does not leave messages to be resent by the reaper.
	done := make(chan struct{})
	go func() { background.Wait(); close(done) }()
	select {
	case <-done:
		logger.PrintInfo("background workers stopped", nil)
	case <-time.After(cfg.delivery.lease + 5*time.Second):
		logger.PrintError(errors.New("background workers did not stop in time"), nil)
	}
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
		SendRate:  ratelimit.Limit{Rate: app.config.delivery.ratePerSecond, Burst: app.config.delivery.burst},
		Quota:     &ratelimit.Quota{Redis: app.redis, Prefix: "smtp:quota:", Daily: app.config.delivery.dailyLimit},
		PublicURL: strings.TrimRight(app.config.publicURL, "/"),
	}

	run("settings-watch", app.settings.Watch)
	run("outbox-relay", (&outbox.Relay{
		DB: app.db, Redis: app.redis, Stream: outboxStream, Logger: app.logger,
		BatchSize: 100, MaxInFlight: app.config.delivery.maxInFlight, Interval: 2 * time.Second,
	}).Run)
	run("outbox-worker", func(ctx context.Context) {
		worker := &outbox.Worker{
			DB: app.db, Redis: app.redis, Stream: outboxStream, Group: outboxGroup,
			ConsumerName: fmt.Sprintf("%s-%d", hostname, os.Getpid()),
			Concurrency:  app.config.delivery.workers, Lease: app.config.delivery.lease,
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

func openDB(cfg config) (*sql.DB, error) {

	db, err := sql.Open("postgres", cfg.db.dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(cfg.db.maxOpenConns)
	db.SetMaxIdleConns(cfg.db.maxIdleConns)

	duration, err := time.ParseDuration(cfg.db.maxIdleTime)
	if err != nil {
		return nil, err
	}

	db.SetConnMaxIdleTime(duration)

	maxRetries := 10
	for i := 0; i < maxRetries; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = db.PingContext(ctx)
		cancel()
		if err != nil {
			if i == maxRetries-1 {
				return nil, err
			}

			log.Printf("Database not ready, retrying in 5 seconds... (%d/%d)", i+1, maxRetries)
			time.Sleep(5 * time.Second)
			continue
		}

		return db, nil
	}

	return nil, fmt.Errorf("database connection failed after %d attempts", maxRetries)
}

func openRedis(cfg config) (*redis.Client, error) {
	options, err := redis.ParseURL(cfg.redisURL)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(options)
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			return rdb, nil
		}
		log.Printf("Redis not ready, retrying in 3 seconds... (%d/10)", i+1)
		time.Sleep(3 * time.Second)
	}
	return nil, err
}

func runMigration(db *sql.DB) error {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return err
	}

	m, err := migrate.NewWithDatabaseInstance("file://./migrations", "postgres", driver)
	if err != nil {
		return err
	}

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return err
	}

	return nil
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

func envFloat(name string, fallback float64) float64 {
	// An explicit 0 is meaningful (e.g. SMTP_RATE_PER_SECOND=0 means unlimited).
	value, err := strconv.ParseFloat(os.Getenv(name), 64)
	if err != nil {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	value, err := strconv.ParseBool(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
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
