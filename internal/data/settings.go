package data

import (
	"context"
	"database/sql"
	"errors"
)

type SMTPSettings struct {
	Host               string
	Port               int
	Username           string
	PasswordCiphertext string
	Sender             string
}

type CertificateTemplate struct {
	Filename string
	Content  []byte
}

type SettingsModel struct{ DB *sql.DB }

func (m SettingsModel) GetSMTP(ctx context.Context) (SMTPSettings, error) {
	var settings SMTPSettings
	err := m.DB.QueryRowContext(ctx, `SELECT host, port, username, password_ciphertext, sender FROM smtp_settings WHERE id=1`).
		Scan(&settings.Host, &settings.Port, &settings.Username, &settings.PasswordCiphertext, &settings.Sender)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, ErrRecordNotFound
	}
	return settings, err
}

func (m SettingsModel) SaveSMTP(ctx context.Context, settings SMTPSettings) error {
	_, err := m.DB.ExecContext(ctx, `INSERT INTO smtp_settings (id, host, port, username, password_ciphertext, sender)
		VALUES (1, $1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET host=EXCLUDED.host, port=EXCLUDED.port,
		username=EXCLUDED.username, password_ciphertext=EXCLUDED.password_ciphertext,
		sender=EXCLUDED.sender, updated_at=NOW()`,
		settings.Host, settings.Port, settings.Username, settings.PasswordCiphertext, settings.Sender)
	return err
}

func (m SettingsModel) GetTemplate(ctx context.Context) (CertificateTemplate, error) {
	var template CertificateTemplate
	err := m.DB.QueryRowContext(ctx, `SELECT filename, content FROM certificate_templates WHERE id=1`).
		Scan(&template.Filename, &template.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return template, ErrRecordNotFound
	}
	return template, err
}

func (m SettingsModel) SaveTemplate(ctx context.Context, template CertificateTemplate) error {
	_, err := m.DB.ExecContext(ctx, `INSERT INTO certificate_templates (id, filename, content) VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET filename=EXCLUDED.filename, content=EXCLUDED.content, updated_at=NOW()`,
		template.Filename, template.Content)
	return err
}
