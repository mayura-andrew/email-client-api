// Package delivery turns outbox messages into SMTP sends. Each worker goroutine
// owns one Processor and therefore one reusable SMTP session.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"io"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/go-mail/mail/v2"
	"github.com/mayura-andrew/email-client/internal/certificate"
	"github.com/mayura-andrew/email-client/internal/data"
	"github.com/mayura-andrew/email-client/internal/mailer"
	"github.com/mayura-andrew/email-client/internal/outbox"
	"github.com/mayura-andrew/email-client/internal/ratelimit"
	"github.com/mayura-andrew/email-client/internal/settings"
)

type Service struct {
	Campaigns data.CampaignModel
	Emails    data.EmailModel
	Settings  *settings.Store
	Limiter   *ratelimit.Limiter
	// SendRate is shared by every worker on every replica, so it can be set to
	// the SMTP provider's limit directly.
	SendRate  ratelimit.Limit
	Quota     *ratelimit.Quota
	PublicURL string
}

func (s *Service) NewProcessor() outbox.Processor { return &processor{service: s} }

// Hooks maps each topic to the function that mirrors delivery state into its own table.
func Hooks() map[string]outbox.StateHook {
	return map[string]outbox.StateHook{
		outbox.TopicCertificateEmail: data.CertificateHook,
		outbox.TopicTrackedEmail:     data.TrackedHook,
	}
}

type processor struct {
	service *Service
	conn    mailer.Conn
}

func (p *processor) Close() error { return p.conn.Close() }

func (p *processor) Process(ctx context.Context, msg outbox.Message) error {
	snapshot := p.service.Settings.Current()
	if !snapshot.SMTP.Configured() {
		return outbox.Defer(errors.New("SMTP is not configured"), time.Now().Add(5*time.Minute))
	}

	var message *mail.Message
	var recipient string
	var err error
	switch msg.Topic {
	case outbox.TopicCertificateEmail:
		message, recipient, err = p.certificateMessage(ctx, snapshot, msg.AggregateID)
	case outbox.TopicTrackedEmail:
		message, recipient, err = p.trackedMessage(ctx, snapshot, msg.AggregateID)
	default:
		err = outbox.Permanent(fmt.Errorf("unknown outbox topic %q", msg.Topic))
	}
	if err != nil {
		return err
	}
	from, err := envelopeAddress(message.GetHeader("From"))
	if err != nil {
		return outbox.Permanent(err)
	}
	// Delivery is at-least-once: a crash after the server accepted the message
	// but before it was recorded causes a resend. A Message-ID that is stable
	// across attempts lets mailbox providers collapse that duplicate.
	message.SetHeader("Message-ID", fmt.Sprintf("<outbox.%d.%d@%s>", msg.ID, msg.AggregateID, domainOf(from)))

	if err := p.service.Limiter.Wait(ctx, "smtp", p.service.SendRate); err != nil {
		return err
	}
	ok, reset, err := p.service.Quota.Take(ctx, time.Now())
	if err != nil {
		return err
	}
	if !ok {
		return outbox.Defer(errors.New("daily SMTP quota reached"), reset)
	}

	if err := p.conn.Send(snapshot.SMTP, from, []string{recipient}, message); err != nil {
		p.service.Quota.Release(ctx, time.Now())
		switch {
		case mailer.IsQuotaExceeded(err):
			return outbox.Defer(err, time.Now().Add(time.Hour))
		case mailer.IsPermanent(err):
			return outbox.Permanent(err)
		}
		return err
	}
	return nil
}

func (p *processor) certificateMessage(ctx context.Context, snapshot settings.Snapshot, recipientID int64) (*mail.Message, string, error) {
	delivery, err := p.service.Campaigns.CertificateDelivery(ctx, recipientID)
	if errors.Is(err, data.ErrRecordNotFound) {
		return nil, "", outbox.Permanent(err)
	}
	if err != nil {
		return nil, "", err
	}
	// A template problem is fixable by an admin, so it is retried rather than failed.
	content, filename, err := certificate.Generate(delivery.Recipient.StudentName, delivery.Recipient.Number, snapshot.Certificate)
	if err != nil {
		return nil, "", fmt.Errorf("certificate generation: %w", err)
	}
	name, email, number := delivery.Recipient.StudentName, delivery.Recipient.Email, delivery.Recipient.Number
	sender := delivery.Sender
	if sender == "" {
		sender = snapshot.SMTP.Sender
	}

	message := mail.NewMessage()
	message.SetHeader("From", sender)
	message.SetHeader("To", email)
	tokens := strings.NewReplacer("{{student_name}}", name, "{{email}}", email, "{{certificate_number}}", number)
	message.SetHeader("Subject", tokens.Replace(delivery.Subject))
	message.SetBody("text/plain", tokens.Replace(delivery.Body))
	// Attached from memory; the copy func can run more than once if a dead
	// pooled connection forces a resend.
	message.Attach(filename, mail.SetCopyFunc(func(w io.Writer) error {
		_, err := w.Write(content)
		return err
	}))
	return message, email, nil
}

func (p *processor) trackedMessage(ctx context.Context, snapshot settings.Snapshot, recipientID int64) (*mail.Message, string, error) {
	delivery, err := p.service.Emails.TrackedDelivery(ctx, recipientID)
	if errors.Is(err, data.ErrRecordNotFound) {
		return nil, "", outbox.Permanent(err)
	}
	if err != nil {
		return nil, "", err
	}
	html, err := mailer.RenderTracked(mailer.TrackedEmailData{
		Subject: delivery.Subject, Body: delivery.Body, Recipient: delivery.Recipient,
		EmailId: delivery.RecipientID, URL: p.service.PublicURL,
	})
	if err != nil {
		return nil, "", outbox.Permanent(err)
	}
	message := mail.NewMessage()
	// Always send as the configured account so callers cannot spoof the From address.
	message.SetHeader("From", snapshot.SMTP.Sender)
	message.SetHeader("To", delivery.Recipient)
	message.SetHeader("Subject", delivery.Subject)
	message.SetBody("text/html", html)
	return message, delivery.Recipient, nil
}

func domainOf(address string) string {
	if at := strings.LastIndex(address, "@"); at >= 0 && at < len(address)-1 {
		return address[at+1:]
	}
	return "localhost"
}

func envelopeAddress(header []string) (string, error) {
	if len(header) == 0 {
		return "", errors.New("message has no From header")
	}
	address, err := netmail.ParseAddress(header[0])
	if err != nil {
		return "", fmt.Errorf("invalid From address %q: %w", header[0], err)
	}
	return address.Address, nil
}
