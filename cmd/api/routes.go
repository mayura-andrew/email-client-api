package main

import (
	"expvar"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func (app *application) routes() http.Handler {
	router := httprouter.New()

	router.NotFound = http.HandlerFunc(app.notFoundResponse)
	router.MethodNotAllowed = http.HandlerFunc(app.methodNotAllowedResponse)

	admin := func(h http.HandlerFunc) http.Handler { return app.requireAdmin(h) }

	// Public
	router.HandlerFunc(http.MethodGet, "/", app.rootHandler)
	router.HandlerFunc(http.MethodGet, "/api/v1/healthcheck", app.healthcheckHandler)
	router.HandlerFunc(http.MethodGet, "/api/v1/redirect", app.track)
	router.HandlerFunc(http.MethodPost, "/api/v1/auth/login", app.loginHandler)
	router.HandlerFunc(http.MethodPost, "/api/v1/auth/logout", app.logoutHandler)

	// Admin
	router.Handler(http.MethodGet, "/api/v1/auth/session", admin(app.sessionHandler))
	router.Handler(http.MethodGet, "/debug/vars", app.requireAdmin(expvar.Handler()))

	router.Handler(http.MethodPost, "/api/v1/send", admin(app.sendEmailHandler))
	router.Handler(http.MethodGet, "/api/v1/sent", admin(app.listSentHandler))
	router.Handler(http.MethodGet, "/api/v1/recipients/:email", admin(app.listSentHandler))

	router.Handler(http.MethodPost, "/api/v1/campaigns", admin(app.createCampaignHandler))
	router.Handler(http.MethodGet, "/api/v1/campaigns", admin(app.listCampaignsHandler))
	router.Handler(http.MethodGet, "/api/v1/campaigns/:id", admin(app.getCampaignHandler))
	router.Handler(http.MethodPost, "/api/v1/campaigns/:id/csv", admin(app.uploadCampaignCSVHandler))
	router.Handler(http.MethodPost, "/api/v1/campaigns/:id/start", admin(app.startCampaignHandler))
	router.Handler(http.MethodPost, "/api/v1/campaigns/:id/retry", admin(app.retryCampaignHandler))
	router.Handler(http.MethodPost, "/api/v1/campaigns/:id/recipients", admin(app.addRecipientHandler))
	router.Handler(http.MethodGet, "/api/v1/campaigns/:id/recipients", admin(app.listRecipientsHandler))

	router.Handler(http.MethodGet, "/api/v1/delivery/status", admin(app.deliveryStatusHandler))

	router.Handler(http.MethodGet, "/api/v1/settings", admin(app.settingsHandler))
	router.Handler(http.MethodPut, "/api/v1/settings/smtp", admin(app.saveSMTPHandler))
	router.Handler(http.MethodPost, "/api/v1/settings/smtp/test", admin(app.testSMTPHandler))
	router.Handler(http.MethodGet, "/api/v1/certificate/preview", admin(app.certificatePreviewHandler))
	router.Handler(http.MethodGet, "/api/v1/certificate/template", admin(app.certificateTemplateHandler))
	router.Handler(http.MethodPost, "/api/v1/certificate/template", admin(app.uploadCertificateTemplateHandler))
	router.Handler(http.MethodPost, "/api/v1/certificate/template/preview", admin(app.previewTemplateUploadHandler))

	return app.recoverPanic(app.enableCORS(app.rateLimit(router)))
}
