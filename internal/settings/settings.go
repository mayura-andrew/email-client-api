// Package settings owns runtime configuration that admins can change while the
// app runs (SMTP credentials, certificate template). Readers get an
// immutable snapshot through an atomic pointer, so HTTP handlers and worker
// goroutines never race. Changes are written to PostgreSQL and broadcast over
// Redis pub/sub so every replica reloads the same values.
package settings

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/mayura-andrew/email-client/internal/certificate"
	"github.com/mayura-andrew/email-client/internal/data"
	"github.com/mayura-andrew/email-client/internal/mailer"
	"github.com/mayura-andrew/email-client/internal/secrets"
	"github.com/redis/go-redis/v9"
)

const changedChannel = "settings:changed"

type Snapshot struct {
	SMTP        mailer.Config
	Certificate certificate.Config
}

type Logger interface {
	PrintError(err error, properties map[string]string)
}

type Store struct {
	model  data.SettingsModel
	redis  *redis.Client
	secret []byte
	logger Logger
	// defaults apply until an admin saves settings; defaultCertificate carries
	// the bundled template.
	defaultSMTP        mailer.Config
	defaultCertificate certificate.Config
	current            atomic.Pointer[Snapshot]
}

func New(model data.SettingsModel, rdb *redis.Client, secret []byte, logger Logger,
	defaultSMTP mailer.Config, defaultCertificate certificate.Config) *Store {
	s := &Store{model: model, redis: rdb, secret: secret, logger: logger,
		defaultSMTP: defaultSMTP, defaultCertificate: defaultCertificate}
	s.current.Store(&Snapshot{SMTP: defaultSMTP, Certificate: defaultCertificate})
	return s
}

func (s *Store) Current() Snapshot { return *s.current.Load() }

// Reload rebuilds the snapshot from the database, falling back to defaults.
func (s *Store) Reload(ctx context.Context) error {
	next := Snapshot{SMTP: s.defaultSMTP, Certificate: s.defaultCertificate}

	saved, err := s.model.GetSMTP(ctx)
	switch {
	case err == nil:
		password, decryptErr := secrets.Decrypt(s.secret, saved.PasswordCiphertext)
		if decryptErr != nil {
			s.logger.PrintError(decryptErr, map[string]string{"component": "settings", "detail": "cannot decrypt stored SMTP password; was ADMIN_SESSION_SECRET changed?"})
		} else {
			next.SMTP = mailer.Config{Host: saved.Host, Port: saved.Port, Username: saved.Username,
				Password: string(password), Sender: saved.Sender}
		}
	case !errors.Is(err, data.ErrRecordNotFound):
		return err
	}

	template, err := s.model.GetTemplate(ctx)
	switch {
	case err == nil:
		next.Certificate.Template = certificate.Template{Filename: template.Filename, Content: template.Content}
	case errors.Is(err, data.ErrRecordNotFound):
	default:
		return err
	}

	s.current.Store(&next)
	return nil
}

func (s *Store) SaveSMTP(ctx context.Context, cfg mailer.Config) error {
	ciphertext, err := secrets.Encrypt(s.secret, []byte(cfg.Password))
	if err != nil {
		return err
	}
	if err := s.model.SaveSMTP(ctx, data.SMTPSettings{Host: cfg.Host, Port: cfg.Port, Username: cfg.Username,
		PasswordCiphertext: ciphertext, Sender: cfg.Sender}); err != nil {
		return err
	}
	return s.changed(ctx)
}

func (s *Store) SaveTemplate(ctx context.Context, template certificate.Template) error {
	if err := s.model.SaveTemplate(ctx, data.CertificateTemplate{Filename: template.Filename, Content: template.Content}); err != nil {
		return err
	}
	return s.changed(ctx)
}

func (s *Store) changed(ctx context.Context) error {
	if err := s.Reload(ctx); err != nil {
		return err
	}
	if err := s.redis.Publish(ctx, changedChannel, "1").Err(); err != nil {
		// The local snapshot is already correct; other replicas catch up on their next Watch reload.
		s.logger.PrintError(err, map[string]string{"component": "settings", "operation": "publish_change"})
	}
	return nil
}

// Watch reloads the snapshot whenever another replica saves settings, and
// periodically in case a notification was lost while Redis was unreachable.
func (s *Store) Watch(ctx context.Context) {
	sub := s.redis.Subscribe(ctx, changedChannel)
	defer sub.Close()
	changes := sub.Channel()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
		case <-ticker.C:
		}
		if err := s.Reload(ctx); err != nil && ctx.Err() == nil {
			s.logger.PrintError(err, map[string]string{"component": "settings", "operation": "reload"})
		}
	}
}
