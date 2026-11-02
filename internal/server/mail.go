package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/julienschmidt/httprouter"
	"github.com/mayura-andrew/email-client/internal/data"
	"github.com/mayura-andrew/email-client/internal/outbox"
	"github.com/mayura-andrew/email-client/internal/validator"
)

// sendEmailHandler queues a tracked email for every recipient and returns
// immediately. Delivery happens in the outbox workers, so request time no
// longer grows with the recipient count.
func (app *application) sendEmailHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sender     string   `json:"sender"`
		Recipients []string `json:"recipients"`
		Subject    string   `json:"subject"`
		Body       string   `json:"body"`
	}

	err := app.readJSON(w, r, &req)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	recipients := make([]string, 0, len(req.Recipients))
	for _, recipient := range req.Recipients {
		recipients = append(recipients, strings.ToLower(strings.TrimSpace(recipient)))
	}
	email := &data.Email{
		Sender:     req.Sender,
		Recipients: recipients,
		Subject:    req.Subject,
		Body:       req.Body,
	}

	v := validator.New()

	if data.ValidateEmail(v, email); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	ids, err := app.models.Emails.Queue(r.Context(), email, app.config.delivery.MaxAttempts)
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}
	outbox.Notify(r.Context(), app.redis)

	headers := make(http.Header)
	headers.Set("Location", "/api/v1/sent")

	err = app.writeJSON(w, http.StatusAccepted, envelop{"status": "queued", "emailId": email.ID, "recipientIds": ids}, headers)
	if err != nil {
		app.serverErrorRespone(w, r, err)
	}
}

// transparentGIF is a 1x1 tracking pixel.
var transparentGIF = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\x00\x00\x00!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

// track records an open (pixel load or link click). It redirects to
// TRACKING_REDIRECT_URL when one is configured, otherwise it returns a pixel.
func (app *application) track(w http.ResponseWriter, r *http.Request) {
	if id, err := app.readIDParam(r); err == nil {
		if err := app.models.Emails.MarkOpened(r.Context(), id); err != nil && !errors.Is(err, data.ErrRecordNotFound) {
			app.logError(r, err)
		}
	}
	if app.config.trackingRedirectURL != "" {
		http.Redirect(w, r, app.config.trackingRedirectURL, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(transparentGIF)
}

// listSentHandler lists delivery records, optionally filtered by the :email route parameter.
func (app *application) listSentHandler(w http.ResponseWriter, r *http.Request) {
	recipient := strings.ToLower(httprouter.ParamsFromContext(r.Context()).ByName("email"))
	page := app.readPage(r, 100)

	emails, total, err := app.models.Emails.List(r.Context(), recipient, page)
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelop{"emails": emails, "metadata": pageMetadata(page, total)}, nil)
	if err != nil {
		app.serverErrorRespone(w, r, err)
	}
}
