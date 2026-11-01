package mailer

import (
	"bytes"
	_ "embed"
	"errors"
	"html/template"
	"net/textproto"
	"strings"
	"time"

	"github.com/go-mail/mail/v2"
)

type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	Sender   string
}

func (c Config) Configured() bool {
	return c.Host != "" && c.Port > 0 && c.Username != "" && c.Password != "" && c.Sender != ""
}

const (
	// Providers commonly cap messages per session (Gmail ~100) and drop idle
	// sessions; reconnect before either happens.
	maxMessagesPerConn = 90
	maxIdle            = 30 * time.Second
)

// Conn is a reusable SMTP session owned by a single goroutine. Reusing it skips
// the TCP + STARTTLS + AUTH handshake that dominated per-message cost.
type Conn struct {
	cfg      Config
	sender   mail.SendCloser
	sent     int
	lastUsed time.Time
}

// Send delivers m through cfg, reconnecting when the config changed, the
// session is old, or a reused session turns out to be dead.
func (c *Conn) Send(cfg Config, from string, to []string, m *mail.Message) error {
	if c.sender != nil && (cfg != c.cfg || c.sent >= maxMessagesPerConn || time.Since(c.lastUsed) > maxIdle) {
		c.Close()
	}
	fresh := c.sender == nil
	if fresh {
		if err := c.dial(cfg); err != nil {
			return err
		}
	}
	err := c.sender.Send(from, to, m)
	if err != nil {
		c.Close()
		var smtpErr *textproto.Error
		if !fresh && !errors.As(err, &smtpErr) {
			// The pooled session died between messages; that is not the message's fault.
			if err := c.dial(cfg); err != nil {
				return err
			}
			err = c.sender.Send(from, to, m)
			if err != nil {
				c.Close()
			}
		}
	}
	if err == nil {
		c.sent++
		c.lastUsed = time.Now()
	}
	return err
}

func (c *Conn) dial(cfg Config) error {
	dialer := mail.NewDialer(cfg.Host, cfg.Port, cfg.Username, cfg.Password)
	dialer.Timeout = 30 * time.Second
	if cfg.Port == 465 {
		dialer.SSL = true
	}
	sender, err := dialer.Dial()
	if err != nil {
		return err
	}
	c.cfg, c.sender, c.sent, c.lastUsed = cfg, sender, 0, time.Now()
	return nil
}

func (c *Conn) Close() error {
	if c.sender == nil {
		return nil
	}
	err := c.sender.Close()
	c.sender = nil
	return err
}

// IsPermanent reports whether retrying err can never succeed for this message.
// Authentication and quota errors are treated as transient because fixing the
// SMTP settings or waiting for the quota to reset makes the same message deliverable.
func IsPermanent(err error) bool {
	var smtpErr *textproto.Error
	if !errors.As(err, &smtpErr) || smtpErr.Code < 500 {
		// Network failures, timeouts and 4xx replies are all worth retrying.
		return false
	}
	switch smtpErr.Code {
	case 530, 534, 535, 538:
		return false
	}
	message := strings.ToLower(smtpErr.Msg)
	if strings.Contains(message, "5.4.5") || strings.Contains(message, "quota") || strings.Contains(message, "rate limit") {
		return false
	}
	return true
}

// IsQuotaExceeded detects provider-side sending limits (e.g. Gmail 550 5.4.5).
func IsQuotaExceeded(err error) bool {
	var smtpErr *textproto.Error
	if !errors.As(err, &smtpErr) {
		return false
	}
	message := strings.ToLower(smtpErr.Msg)
	return strings.Contains(message, "5.4.5") || strings.Contains(message, "daily user sending quota")
}

//go:embed email_template.tmpl
var trackedTemplateSource string

var trackedTemplate = template.Must(template.New("email").Parse(trackedTemplateSource))

type TrackedEmailData struct {
	Subject   string
	Body      string
	Recipient string
	EmailId   int64
	URL       string
}

// RenderTracked renders the HTML body for a tracked one-off email.
func RenderTracked(data TrackedEmailData) (string, error) {
	var buf bytes.Buffer
	if err := trackedTemplate.ExecuteTemplate(&buf, "htmlBody", data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
