package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-mail/mail/v2"
	"github.com/mayura-andrew/email-client/internal/certificate"
	"github.com/mayura-andrew/email-client/internal/mailer"
	"github.com/mayura-andrew/email-client/internal/validator"
)

const maxTemplateBytes = 20 << 20

// sampleNumber stands in for a real certificate number in previews, so admins
// can check its placement before sending.
const sampleNumber = "CERT-000001"

func previewNumber(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return sampleNumber
}

func (app *application) settingsHandler(w http.ResponseWriter, r *http.Request) {
	snapshot := app.settings.Current()
	_ = app.writeJSON(w, http.StatusOK, envelop{
		"smtp": envelop{
			"host":       snapshot.SMTP.Host,
			"port":       snapshot.SMTP.Port,
			"username":   snapshot.SMTP.Username,
			"sender":     snapshot.SMTP.Sender,
			"configured": snapshot.SMTP.Configured(),
		},
		"certificate": envelop{
			"template":   snapshot.Certificate.Template.Filename,
			"nameX":      snapshot.Certificate.NameX,
			"nameY":      snapshot.Certificate.NameY,
			"nameSize":   snapshot.Certificate.NameSize,
			"numberX":    snapshot.Certificate.NumberX,
			"numberY":    snapshot.Certificate.NumberY,
			"numberSize": snapshot.Certificate.NumberSize,
		},
		"delivery": envelop{
			"workersPerReplica": app.config.delivery.Workers,
			"ratePerSecond":     app.config.delivery.RatePerSecond,
			"dailyLimit":        app.config.delivery.DailyLimit,
			"maxAttempts":       app.config.delivery.MaxAttempts,
		},
	}, nil)
}

func (app *application) saveSMTPHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		Sender   string `json:"sender"`
	}
	if err := app.readJSON(w, r, &request); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	if request.Host == "" || request.Port < 1 || request.Username == "" || !strings.Contains(request.Sender, "@") {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "host, port, username, and sender are required")
		return
	}
	password := request.Password
	if password == "" {
		// Keep the stored password when the form leaves it blank.
		password = app.settings.Current().SMTP.Password
		if password == "" {
			app.errorResponse(w, r, http.StatusUnprocessableEntity, "password is required for the first SMTP configuration")
			return
		}
	}
	err := app.settings.SaveSMTP(r.Context(), mailer.Config{
		Host: request.Host, Port: request.Port, Username: request.Username, Password: password, Sender: request.Sender,
	})
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}
	_ = app.writeJSON(w, http.StatusOK, envelop{"status": "saved"}, nil)
}

func (app *application) testSMTPHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Recipient string `json:"recipient"`
	}
	if err := app.readJSON(w, r, &request); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	if validator.EmailProblem(request.Recipient) != "" {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "a test recipient email is required")
		return
	}
	smtp := app.settings.Current().SMTP
	if !smtp.Configured() {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "SMTP is not configured")
		return
	}
	message := mail.NewMessage()
	message.SetHeader("From", smtp.Sender)
	message.SetHeader("To", request.Recipient)
	message.SetHeader("Subject", "AI4ALL certificate mailer: SMTP test")
	message.SetBody("text/plain", "Your AI4ALL SMTP configuration is working.")

	var conn mailer.Conn
	defer conn.Close()
	if err := conn.Send(smtp, smtp.Sender, []string{request.Recipient}, message); err != nil {
		app.logger.PrintError(err, map[string]string{
			"operation": "smtp_test",
			"recipient": request.Recipient,
			"host":      smtp.Host,
		})
		app.errorResponse(w, r, http.StatusBadGateway, "SMTP test failed: "+err.Error())
		return
	}
	app.logger.PrintInfo("SMTP test email sent", map[string]string{"recipient": request.Recipient})
	_ = app.writeJSON(w, http.StatusOK, envelop{"status": "sent"}, nil)
}

func (app *application) certificateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	template := app.settings.Current().Certificate.Template
	if len(template.Content) == 0 {
		app.notFoundResponse(w, r)
		return
	}
	name := "template.jpg"
	if template.IsPDF() {
		name = "template.pdf"
	}
	w.Header().Set("Content-Type", template.ContentType())
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(template.Content))
}

// readTemplateUpload validates a multipart "template" file. On failure it has
// already written the error response and returns ok=false.
func (app *application) readTemplateUpload(w http.ResponseWriter, r *http.Request) (template certificate.Template, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTemplateBytes+(1<<20))
	if err := r.ParseMultipartForm(maxTemplateBytes); err != nil {
		app.errorResponse(w, r, http.StatusRequestEntityTooLarge, "template file must be 20 MB or smaller")
		return template, false
	}
	file, header, err := r.FormFile("template")
	if err != nil {
		app.badRequestResponse(w, r, fmt.Errorf("template file is required"))
		return template, false
	}
	defer file.Close()

	extension := strings.ToLower(filepath.Ext(header.Filename))
	if extension != ".pdf" && extension != ".jpg" && extension != ".jpeg" {
		app.errorResponse(w, r, http.StatusUnsupportedMediaType, "template must be a PDF or JPG file")
		return template, false
	}
	content, err := io.ReadAll(io.LimitReader(file, maxTemplateBytes+1))
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return template, false
	}
	if len(content) > maxTemplateBytes {
		app.errorResponse(w, r, http.StatusRequestEntityTooLarge, "template file must be 20 MB or smaller")
		return template, false
	}
	if extension == ".pdf" && !bytes.HasPrefix(content, []byte("%PDF-")) {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "file is not a valid PDF")
		return template, false
	}
	if extension != ".pdf" && !strings.HasPrefix(http.DetectContentType(content), "image/jpeg") {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "file is not a valid JPEG image")
		return template, false
	}
	return certificate.Template{Filename: "admin-template" + extension, Content: content}, true
}

// previewTemplateUploadHandler renders a personalized certificate from an
// uploaded template without saving it, so admins can check placement first.
func (app *application) previewTemplateUploadHandler(w http.ResponseWriter, r *http.Request) {
	template, ok := app.readTemplateUpload(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("student_name"))
	if name == "" {
		name = "Student Name"
	}
	cfg := app.settings.Current().Certificate
	cfg.Template = template
	content, filename, err := certificate.Generate(name, previewNumber(r.FormValue("certificate_number")), cfg)
	if err != nil {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "a certificate could not be rendered from this template: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)
	_, _ = w.Write(content)
}

// uploadCertificateTemplateHandler stores the template in PostgreSQL so every
// replica renders with it, after proving a certificate can be rendered from it.
func (app *application) uploadCertificateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	template, ok := app.readTemplateUpload(w, r)
	if !ok {
		return
	}
	trial := app.settings.Current().Certificate
	trial.Template = template
	if _, _, err := certificate.Generate("Template Check", sampleNumber, trial); err != nil {
		app.errorResponse(w, r, http.StatusUnprocessableEntity, "a certificate could not be rendered from this template: "+err.Error())
		return
	}
	if err := app.settings.SaveTemplate(r.Context(), template); err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}
	app.logger.PrintInfo("certificate template updated", map[string]string{
		"filename": template.Filename,
		"bytes":    fmt.Sprint(len(template.Content)),
	})
	_ = app.writeJSON(w, http.StatusOK, envelop{"template": template.Filename}, nil)
}

func (app *application) certificatePreviewHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("student_name"))
	if name == "" {
		name = "Student Name"
	}
	number := previewNumber(r.URL.Query().Get("certificate_number"))
	content, filename, err := certificate.Generate(name, number, app.settings.Current().Certificate)
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
