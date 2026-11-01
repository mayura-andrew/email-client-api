package data

import (
	"database/sql"
	"errors"
)

var (
	ErrRecordNotFound = errors.New("record not found")
)

type Models struct {
	Emails    EmailModel
	Campaigns CampaignModel
	Settings  SettingsModel
	Outbox    OutboxModel
}

func NewModel(db *sql.DB) Models {
	return Models{
		Emails:    EmailModel{DB: db},
		Campaigns: CampaignModel{DB: db},
		Settings:  SettingsModel{DB: db},
		Outbox:    OutboxModel{DB: db},
	}
}

// Page is a 1-based page request; Size is clamped to [1, MaxSize].
type Page struct {
	Number  int
	Size    int
	MaxSize int
}

func (p Page) normalized() Page {
	if p.MaxSize < 1 {
		p.MaxSize = 100
	}
	if p.Size < 1 || p.Size > p.MaxSize {
		p.Size = p.MaxSize
	}
	if p.Number < 1 {
		p.Number = 1
	}
	return p
}

func (p Page) Limit() int  { return p.normalized().Size }
func (p Page) Offset() int { n := p.normalized(); return (n.Number - 1) * n.Size }
