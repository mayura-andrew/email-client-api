package desktop

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
)

const configFile = "config.json"

// Config is what the app needs before it can reach PostgreSQL, so it lives in
// a file in the user's config directory rather than in the database.
type Config struct {
	DatabaseURL string `json:"databaseUrl"`
	RedisURL    string `json:"redisUrl"`

	AdminEmail        string `json:"adminEmail"`
	AdminPasswordHash string `json:"adminPasswordHash"`

	// Secret encrypts the SMTP password stored in PostgreSQL. Every computer
	// that shares one database needs the same secret.
	Secret string `json:"secret"`

	SMTPRatePerSecond float64 `json:"smtpRatePerSecond"`
	SMTPDailyLimit    int     `json:"smtpDailyLimit"`

	// Certificate optionally moves the text for a custom template (A4 mm).
	Certificate *CertificateLayout `json:"certificate,omitempty"`
}

type CertificateLayout struct {
	NameX      float64 `json:"nameX"`
	NameY      float64 `json:"nameY"`
	NameSize   float64 `json:"nameSize"`
	NumberX    float64 `json:"numberX"`
	NumberY    float64 `json:"numberY"`
	NumberSize float64 `json:"numberSize"`
}

func (c Config) connected() bool { return c.DatabaseURL != "" && c.RedisURL != "" && c.Secret != "" }
func (c Config) hasAdmin() bool  { return c.AdminEmail != "" && c.AdminPasswordHash != "" }

// ConfigDir is where the config and logs live, e.g. %APPDATA%\AI4ALL Certificate Mailer.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "AI4ALL Certificate Mailer"), nil
}

func loadConfig(dir string) (Config, error) {
	cfg := Config{SMTPRatePerSecond: 2}
	content, err := os.ReadFile(filepath.Join(dir, configFile))
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	return cfg, json.Unmarshal(content, &cfg)
}

// saveConfig writes atomically so a crash never leaves a half-written file. The
// file holds database passwords, so only the current user may read it.
func saveConfig(dir string, cfg Config) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, configFile+".tmp")
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, configFile))
}

func newSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// redact hides the password in a connection URL so it can be shown in the UI.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	return u.Redacted()
}
