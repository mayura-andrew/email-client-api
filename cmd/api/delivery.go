package main

import (
	"net/http"
	"strconv"
	"time"
)

// deliveryStatusHandler is the live operator view of the outbox: queue depth,
// throughput over the last 30 minutes, today's quota and workers online.
func (app *application) deliveryStatusHandler(w http.ResponseWriter, r *http.Request) {
	stats, err := app.models.Outbox.Stats(r.Context(), 30*time.Minute)
	if err != nil {
		app.serverErrorRespone(w, r, err)
		return
	}

	// A consumer blocked in XREADGROUP refreshes its idle time every read
	// (at most 2s), so anything idle for under 10s belongs to a live worker.
	workersOnline := 0
	if consumers, err := app.redis.XInfoConsumers(r.Context(), outboxStream, outboxGroup).Result(); err == nil {
		for _, consumer := range consumers {
			// "reaper" is the reaper's own claim identity, not a worker.
			if consumer.Name != "reaper" && consumer.Idle < 10*time.Second {
				workersOnline++
			}
		}
	}

	quotaUsed := 0
	if used, err := app.redis.Get(r.Context(), "smtp:quota:"+time.Now().UTC().Format("2006-01-02")).Result(); err == nil {
		quotaUsed, _ = strconv.Atoi(used)
	}

	_ = app.writeJSON(w, http.StatusOK, envelop{
		"queue":      stats.Queue,
		"today":      stats.Today,
		"throughput": stats.Throughput,
		"recent":     stats.Recent,
		"nextRetry":  stats.NextRetry,
		"workers": envelop{
			"online":     workersOnline,
			"perReplica": app.config.delivery.workers,
		},
		"limits": envelop{
			"ratePerSecond": app.config.delivery.ratePerSecond,
			"dailyLimit":    app.config.delivery.dailyLimit,
			"dailyUsed":     quotaUsed,
			"maxAttempts":   app.config.delivery.maxAttempts,
		},
		"smtpConfigured": app.settings.Current().SMTP.Configured(),
		"serverTime":     time.Now().UTC(),
	}, nil)
}
