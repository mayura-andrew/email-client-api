package main

import (
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/mayura-andrew/email-client/internal/data"
	"github.com/mayura-andrew/email-client/internal/outbox"
	"github.com/mayura-andrew/email-client/internal/validator"
)

const (
	maxCSVBytes        = 10 << 20
	maxCSVRows         = 50000
	maxInvalidReported = 1000
)

func (app *application) createCampaignHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name      string `json:"name"`
		Subject   string `json:"subject"`
		Body      string `json:"body"`
		Sender    string `json:"sender"`
		BatchSize int    `json:"batchSize"`
	}
	if err := app.readJSON(w, r, &request); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.Subject) == "" || strings.TrimSpace(request.Body) == "" {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "name, subject, and body are required")
		return
	}
	if request.BatchSize == 0 {
		request.BatchSize = 50
	}
	if request.BatchSize < 1 || request.BatchSize > 1000 {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "batchSize must be between 1 and 1000")
		return
	}
	campaign := &data.Campaign{Name: request.Name, Subject: request.Subject, Body: request.Body, Sender: request.Sender, BatchSize: request.BatchSize}
	if campaign.Sender == "" {
		campaign.Sender = app.settings.Current().SMTP.Sender
	}
	if err := app.models.Campaigns.Create(r.Context(), campaign); err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}
	_ = app.writeJSON(w, http.StatusCreated, envelop{"campaign": campaign}, nil)
}

// uploadCampaignCSVHandler validates the whole file first and then imports
// every valid row in one transaction, so a failure never leaves a half import.
func (app *application) uploadCampaignCSVHandler(w http.ResponseWriter, r *http.Request) {
	campaignID, err := app.readRouteID(r)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCSVBytes+(1<<20))
	if err := r.ParseMultipartForm(maxCSVBytes); err != nil {
		app.errorResponse(w, r, http.StatusRequestEntityTooLarge, "CSV must be 10 MB or smaller")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		app.badRequestResponse(w, r, errors.New("file is required"))
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	headers, err := reader.Read()
	if err != nil {
		app.badRequestResponse(w, r, errors.New("CSV must include a header row"))
		return
	}
	header := map[string]int{}
	for index, value := range headers {
		// Excel prefixes UTF-8 CSVs with a byte-order mark.
		value = strings.TrimPrefix(value, "\ufeff")
		header[strings.ToLower(strings.TrimSpace(value))] = index
	}
	nameIndex, nameOK := header["student_name"]
	emailIndex, emailOK := header["email"]
	if !nameOK || !emailOK {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "CSV must contain student_name and email columns")
		return
	}

	var total, invalidCount int
	var names, emails []string
	invalid := make([]map[string]string, 0)
	seen := map[string]bool{}
	for {
		row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			app.badRequestResponse(w, r, readErr)
			return
		}
		total++
		if total > maxCSVRows {
			app.errorResponse(w, r, http.StatusRequestEntityTooLarge, "CSV must not contain more than 50000 rows")
			return
		}
		var name, email, reason string
		if nameIndex < len(row) && emailIndex < len(row) {
			name, email = strings.TrimSpace(row[nameIndex]), strings.ToLower(strings.TrimSpace(row[emailIndex]))
		} else {
			reason = "row is missing columns"
		}
		switch {
		case reason != "":
		case name == "":
			reason = "student_name is required"
		case validator.EmailProblem(email) != "":
			reason = validator.EmailProblem(email)
		case seen[email]:
			reason = "duplicate email"
		}
		if reason != "" {
			invalidCount++
			if len(invalid) < maxInvalidReported {
				invalid = append(invalid, map[string]string{"studentName": name, "email": email, "reason": reason})
			}
			continue
		}
		seen[email] = true
		names = append(names, name)
		emails = append(emails, email)
	}

	result, err := app.models.Campaigns.ImportRecipients(r.Context(), campaignID, names, emails, total)
	switch {
	case errors.Is(err, data.ErrRecordNotFound):
		app.notFoundResponse(w, r)
		return
	case errors.Is(err, data.ErrCampaignNotDraft):
		app.errorResponse(w, r, http.StatusConflict, "recipients can only be imported into a draft campaign")
		return
	case err != nil:
		app.serverErrorRespone(w, r, err)
		return
	}
	_ = app.writeJSON(w, http.StatusOK, envelop{
		"total":           total,
		"valid":           result.Inserted,
		"alreadyImported": len(emails) - result.Inserted,
		"invalidCount":    invalidCount,
		"invalid":         invalid,
	}, nil)
}

// addRecipientHandler adds one recipient by hand, e.g. a student missing from the CSV.
func (app *application) addRecipientHandler(w http.ResponseWriter, r *http.Request) {
	campaignID, err := app.readRouteID(r)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	var request struct {
		StudentName string `json:"studentName"`
		Email       string `json:"email"`
	}
	if err := app.readJSON(w, r, &request); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	name := strings.TrimSpace(request.StudentName)
	email := strings.ToLower(strings.TrimSpace(request.Email))
	if name == "" {
		app.failedValidationResponse(w, r, map[string]string{"studentName": "Student name is empty"})
		return
	}
	if problem := validator.EmailProblem(email); problem != "" {
		app.failedValidationResponse(w, r, map[string]string{"email": problem})
		return
	}
	recipient, queued, err := app.models.Campaigns.AddRecipient(r.Context(), campaignID, name, email, app.config.delivery.maxAttempts)
	switch {
	case errors.Is(err, data.ErrRecordNotFound):
		app.notFoundResponse(w, r)
		return
	case errors.Is(err, data.ErrDuplicateRecipient):
		app.errorResponse(w, r, http.StatusConflict, "This email is already in this campaign.")
		return
	case err != nil:
		app.serverErrorRespone(w, r, err)
		return
	}
	if queued {
		outbox.Notify(r.Context(), app.redis)
	}
	_ = app.writeJSON(w, http.StatusCreated, envelop{"recipient": recipient, "queued": queued}, nil)
}

func (app *application) startCampaignHandler(w http.ResponseWriter, r *http.Request) {
	campaignID, err := app.readRouteID(r)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	queued, err := app.models.Campaigns.Start(r.Context(), campaignID, app.config.delivery.maxAttempts)
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			app.errorResponse(w, r, http.StatusConflict, "campaign is not ready to start")
			return
		}
		app.serverErrorRespone(w, r, err)
		return
	}
	outbox.Notify(r.Context(), app.redis)
	_ = app.writeJSON(w, http.StatusAccepted, envelop{"status": "running", "campaignId": campaignID, "queued": queued}, nil)
}

func (app *application) listCampaignsHandler(w http.ResponseWriter, r *http.Request) {
	page := app.readPage(r, 50)
	campaigns, total, err := app.models.Campaigns.List(r.Context(), page)
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}
	_ = app.writeJSON(w, http.StatusOK, envelop{"campaigns": campaigns, "metadata": pageMetadata(page, total)}, nil)
}

func (app *application) getCampaignHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readRouteID(r)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	campaign, err := app.models.Campaigns.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			app.notFoundResponse(w, r)
			return
		}
		app.serverErrorRespone(w, r, err)
		return
	}
	_ = app.writeJSON(w, http.StatusOK, envelop{"campaign": campaign}, nil)
}

func (app *application) listRecipientsHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readRouteID(r)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	page := app.readPage(r, 5000)
	query := r.URL.Query()
	filter := data.RecipientFilter{Status: query.Get("status"), Search: strings.TrimSpace(query.Get("q"))}
	recipients, total, err := app.models.Campaigns.Recipients(r.Context(), id, filter, page)
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}
	_ = app.writeJSON(w, http.StatusOK, envelop{"recipients": recipients, "metadata": pageMetadata(page, total)}, nil)
}

func (app *application) retryCampaignHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readRouteID(r)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	count, err := app.models.Campaigns.RetryFailed(r.Context(), id)
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}
	_ = app.writeJSON(w, http.StatusOK, envelop{"status": "ready", "requeued": count}, nil)
}
