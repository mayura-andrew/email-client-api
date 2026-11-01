package main

import (
	"context"
	"net/http"
	"time"
)

// healthcheckHandler reports 503 when PostgreSQL or Redis is unreachable so a
// load balancer stops routing to a replica that cannot do useful work.
func (app *application) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]string{"database": "ok", "redis": "ok"}
	status, code := "available", http.StatusOK
	if err := app.db.PingContext(ctx); err != nil {
		checks["database"], status, code = err.Error(), "degraded", http.StatusServiceUnavailable
	}
	if err := app.redis.Ping(ctx).Err(); err != nil {
		checks["redis"], status, code = err.Error(), "degraded", http.StatusServiceUnavailable
	}

	env := envelop{
		"status": status,
		"checks": checks,
		"system_info": map[string]string{
			"environment": app.config.env,
			"version":     version,
		},
	}

	err := app.writeJSON(w, code, env, nil)
	if err != nil {
		app.logger.PrintError(err, nil)
		app.serverErrorRespone(w, r, err)
	}
}
