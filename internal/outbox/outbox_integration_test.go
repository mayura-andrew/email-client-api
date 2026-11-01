package outbox_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/lib/pq"
	"github.com/mayura-andrew/email-client/internal/data"
	"github.com/mayura-andrew/email-client/internal/outbox"
	"github.com/redis/go-redis/v9"
)

// These tests need a disposable PostgreSQL database and Redis instance:
//
//	TEST_DATABASE_URL=postgres://... TEST_REDIS_URL=redis://... go test ./internal/outbox/
//
// The database schema and the Redis keyspace are wiped.

const (
	stream = "test:outbox"
	group  = "test-mailers"
)

type testLogger struct{ t *testing.T }

func (l testLogger) PrintInfo(string, map[string]string) {}
func (l testLogger) PrintError(err error, properties map[string]string) {
	l.t.Logf("error: %v %v", err, properties)
}

func setup(t *testing.T) (*sql.DB, *redis.Client) {
	t.Helper()
	dsn, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_URL are not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://../../migrations", "postgres", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Up(); err != nil {
		t.Fatal(err)
	}

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(options)
	t.Cleanup(func() { rdb.Close() })
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	if err := outbox.EnsureGroup(context.Background(), rdb, stream, group); err != nil {
		t.Fatal(err)
	}
	return db, rdb
}

func seedCampaign(t *testing.T, db *sql.DB, emails ...string) int64 {
	t.Helper()
	ctx := context.Background()
	campaigns := data.CampaignModel{DB: db}
	campaign := &data.Campaign{Name: "c", Subject: "s", Body: "b", Sender: "from@example.com"}
	if err := campaigns.Create(ctx, campaign); err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(emails))
	for i := range emails {
		names[i] = "Student " + emails[i]
	}
	if _, err := campaigns.ImportRecipients(ctx, campaign.ID, names, emails, len(emails)); err != nil {
		t.Fatal(err)
	}
	if _, err := campaigns.Start(ctx, campaign.ID, 3); err != nil {
		t.Fatal(err)
	}
	return campaign.ID
}

var hooks = map[string]outbox.StateHook{outbox.TopicCertificateEmail: data.CertificateHook}

// fakeProcessor decides the outcome from the recipient email and counts calls.
type fakeProcessor struct {
	db    *sql.DB
	mu    *sync.Mutex
	calls map[int64]int
}

func (p fakeProcessor) Close() error { return nil }
func (p fakeProcessor) Process(ctx context.Context, msg outbox.Message) error {
	p.mu.Lock()
	p.calls[msg.AggregateID]++
	p.mu.Unlock()
	var email string
	if err := p.db.QueryRowContext(ctx, `SELECT email FROM campaign_recipients WHERE id=$1`, msg.AggregateID).Scan(&email); err != nil {
		return err
	}
	switch email {
	case "transient@example.com":
		return errors.New("421 try later")
	case "permanent@example.com":
		return outbox.Permanent(errors.New("550 no such user"))
	case "quota@example.com":
		return outbox.Defer(errors.New("quota"), time.Now().Add(time.Hour))
	}
	return nil
}

func newWorker(t *testing.T, db *sql.DB, rdb *redis.Client, calls map[int64]int, mu *sync.Mutex) *outbox.Worker {
	return &outbox.Worker{
		DB: db, Redis: rdb, Stream: stream, Group: group, ConsumerName: "test",
		Concurrency: 3, Lease: 30 * time.Second, Hooks: hooks, Logger: testLogger{t},
		NewProcessor: func() outbox.Processor { return fakeProcessor{db: db, mu: mu, calls: calls} },
	}
}

func runUntil(t *testing.T, worker *outbox.Worker, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { _ = worker.Run(ctx); close(finished) }()
	deadline := time.Now().Add(15 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("timed out waiting for the worker")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-finished
}

func countStatus(t *testing.T, db *sql.DB, statuses ...string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_messages WHERE status = ANY($1)`, pq.Array(statuses)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func recipientStatuses(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT email, status FROM campaign_recipients`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var email, status string
		if err := rows.Scan(&email, &status); err != nil {
			t.Fatal(err)
		}
		result[email] = status
	}
	return result
}

func TestOutboxDeliversRetriesAndFails(t *testing.T) {
	db, rdb := setup(t)
	ctx := context.Background()
	campaignID := seedCampaign(t, db, "a@example.com", "b@example.com", "transient@example.com",
		"permanent@example.com", "quota@example.com")

	relay := &outbox.Relay{DB: db, Redis: rdb, Stream: stream, Logger: testLogger{t}, BatchSize: 100, MaxInFlight: 100}
	published, err := relay.PublishOnce(ctx)
	if err != nil || published != 5 {
		t.Fatalf("published=%d err=%v", published, err)
	}

	mu, calls := &sync.Mutex{}, map[int64]int{}
	runUntil(t, newWorker(t, db, rdb, calls, mu), func() bool {
		return countStatus(t, db, "published", "sending") == 0
	})

	got := recipientStatuses(t, db)
	want := map[string]string{
		"a@example.com": "sent", "b@example.com": "sent", "transient@example.com": "retrying",
		"permanent@example.com": "failed", "quota@example.com": "queued",
	}
	for email, status := range want {
		if got[email] != status {
			t.Errorf("%s: status %q, want %q", email, got[email], status)
		}
	}
	for id, n := range calls {
		if n != 1 {
			t.Errorf("recipient %d processed %d times", id, n)
		}
	}

	var attempts int
	var nextAttempt time.Time
	if err := db.QueryRow(`SELECT o.attempts, o.next_attempt_at FROM outbox_messages o
		JOIN campaign_recipients r ON r.id = o.aggregate_id WHERE r.email = 'transient@example.com'`).Scan(&attempts, &nextAttempt); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || time.Until(nextAttempt) < 20*time.Second {
		t.Errorf("transient retry: attempts=%d next in %v, want 1 attempt and a backoff of ~30s", attempts, time.Until(nextAttempt))
	}
	if err := db.QueryRow(`SELECT o.attempts FROM outbox_messages o
		JOIN campaign_recipients r ON r.id = o.aggregate_id WHERE r.email = 'quota@example.com'`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Errorf("deferred message consumed an attempt: attempts=%d", attempts)
	}

	campaign, err := data.CampaignModel{DB: db}.Get(ctx, campaignID)
	if err != nil {
		t.Fatal(err)
	}
	if campaign.SentCount != 2 || campaign.FailedCount != 1 {
		t.Errorf("counts sent=%d failed=%d, want 2 and 1", campaign.SentCount, campaign.FailedCount)
	}

	// Nothing is due yet, so the relay must not publish the retry early.
	if published, _ := relay.PublishOnce(ctx); published != 0 {
		t.Errorf("relay published %d messages before their retry time", published)
	}
}

func TestDuplicateStreamEntriesAndDeliveredMarker(t *testing.T) {
	db, rdb := setup(t)
	ctx := context.Background()
	seedCampaign(t, db, "a@example.com", "b@example.com")

	relay := &outbox.Relay{DB: db, Redis: rdb, Stream: stream, Logger: testLogger{t}, BatchSize: 100, MaxInFlight: 100}
	if _, err := relay.PublishOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var first, second int64
	if err := db.QueryRow(`SELECT MIN(id), MAX(id) FROM outbox_messages`).Scan(&first, &second); err != nil {
		t.Fatal(err)
	}
	// Simulate a relay that crashed after XADD and published again.
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"id": fmt.Sprint(first), "topic": outbox.TopicCertificateEmail}}).Err(); err != nil {
		t.Fatal(err)
	}
	// Simulate a worker that sent the second message but died before recording it.
	if err := rdb.Set(ctx, fmt.Sprintf("outbox:delivered:%d", second), "1", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	mu, calls := &sync.Mutex{}, map[int64]int{}
	runUntil(t, newWorker(t, db, rdb, calls, mu), func() bool {
		pending, _ := rdb.XPending(ctx, stream, group).Result()
		length, _ := rdb.XLen(ctx, stream).Result()
		info, _ := rdb.XInfoGroups(ctx, stream).Result()
		return countStatus(t, db, "sent") == 2 && pending.Count == 0 && len(info) == 1 && info[0].EntriesRead == length
	})

	total := 0
	for _, n := range calls {
		total += n
	}
	if total != 1 {
		t.Fatalf("processor ran %d times, want 1 (duplicate skipped, delivered marker honoured)", total)
	}
}

func TestReaperRecoversExpiredLeasesAndStalePublishes(t *testing.T) {
	db, rdb := setup(t)
	ctx := context.Background()
	seedCampaign(t, db, "a@example.com", "b@example.com", "c@example.com")

	if _, err := db.Exec(`UPDATE outbox_messages SET status='sending', attempts=1, locked_until=NOW() - INTERVAL '1 minute'
		WHERE id = (SELECT MIN(id) FROM outbox_messages)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE outbox_messages SET status='sending', attempts=3, locked_until=NOW() - INTERVAL '1 minute'
		WHERE id = (SELECT MAX(id) FROM outbox_messages)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE outbox_messages SET status='published', published_at=NOW() - INTERVAL '1 hour'
		WHERE status='pending'`); err != nil {
		t.Fatal(err)
	}

	reaper := &outbox.Reaper{DB: db, Redis: rdb, Stream: stream, Group: group, Hooks: hooks,
		Logger: testLogger{t}, RepublishAfter: 15 * time.Minute}
	if err := reaper.ReapOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countStatus(t, db, "pending"); n != 2 {
		t.Errorf("pending=%d, want 2 (expired lease + stale publish)", n)
	}
	if n := countStatus(t, db, "failed"); n != 1 {
		t.Errorf("failed=%d, want 1 (lease expired on the last attempt)", n)
	}
	statuses := recipientStatuses(t, db)
	if statuses["a@example.com"] != "queued" || statuses["c@example.com"] != "failed" {
		t.Errorf("recipient statuses not mirrored: %v", statuses)
	}
}

func TestRetryFailedRequeuesOutboxRow(t *testing.T) {
	db, _ := setup(t)
	ctx := context.Background()
	campaigns := data.CampaignModel{DB: db}
	id := seedCampaign(t, db, "a@example.com")
	if _, err := db.Exec(`UPDATE outbox_messages SET status='failed', attempts=3, last_error='x'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE campaign_recipients SET status='failed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE campaigns SET status='completed'`); err != nil {
		t.Fatal(err)
	}

	if n, err := campaigns.RetryFailed(ctx, id); err != nil || n != 1 {
		t.Fatalf("RetryFailed n=%d err=%v", n, err)
	}
	if n, err := campaigns.Start(ctx, id, 3); err != nil || n != 1 {
		t.Fatalf("Start n=%d err=%v", n, err)
	}
	var status string
	var attempts int
	if err := db.QueryRow(`SELECT status, attempts FROM outbox_messages`).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 0 {
		t.Fatalf("outbox row status=%s attempts=%d, want pending/0", status, attempts)
	}

	if _, err := db.Exec(`UPDATE outbox_messages SET status='sent'`); err != nil {
		t.Fatal(err)
	}
	if err := campaigns.CompleteFinished(ctx); err != nil {
		t.Fatal(err)
	}
	campaign, _ := campaigns.Get(ctx, id)
	if campaign.Status != "completed" {
		t.Fatalf("campaign status=%s, want completed", campaign.Status)
	}
}

func TestRelayIsFairAndRespectsInFlightCap(t *testing.T) {
	db, rdb := setup(t)
	ctx := context.Background()
	big := make([]string, 10)
	for i := range big {
		big[i] = fmt.Sprintf("big%d@example.com", i)
	}
	seedCampaign(t, db, big...)
	small := seedCampaign(t, db, "s1@example.com", "s2@example.com")

	relay := &outbox.Relay{DB: db, Redis: rdb, Stream: stream, Logger: testLogger{t}, BatchSize: 4, MaxInFlight: 4}
	if n, err := relay.PublishOnce(ctx); err != nil || n != 4 {
		t.Fatalf("published=%d err=%v", n, err)
	}
	var smallPublished int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_messages WHERE campaign_id=$1 AND status='published'`, small).Scan(&smallPublished); err != nil {
		t.Fatal(err)
	}
	if smallPublished != 2 {
		t.Errorf("small campaign got %d of 4 slots, want 2 (round-robin)", smallPublished)
	}
	if n, _ := relay.PublishOnce(ctx); n != 0 {
		t.Errorf("relay exceeded the in-flight cap by %d", n)
	}
}

func TestDeliveryStatsAndRecipientFilters(t *testing.T) {
	db, _ := setup(t)
	ctx := context.Background()
	id := seedCampaign(t, db, "a@example.com", "b@example.com", "c@example.com", "d@example.com")
	// a: sent, b: failed, c: waiting for a retry, d: ready.
	for _, stmt := range []string{
		`UPDATE outbox_messages o SET status='sent', sent_at=NOW() FROM campaign_recipients r WHERE r.id=o.aggregate_id AND r.email='a@example.com'`,
		`UPDATE outbox_messages o SET status='failed', last_error='550 no such user' FROM campaign_recipients r WHERE r.id=o.aggregate_id AND r.email='b@example.com'`,
		`UPDATE outbox_messages o SET attempts=1, next_attempt_at=NOW() + INTERVAL '5 minutes', last_error='421 later' FROM campaign_recipients r WHERE r.id=o.aggregate_id AND r.email='c@example.com'`,
		`UPDATE campaign_recipients SET status='sent' WHERE email='a@example.com'`,
		`UPDATE campaign_recipients SET status='failed' WHERE email='b@example.com'`,
		`UPDATE campaign_recipients SET status='retrying' WHERE email='c@example.com'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := data.OutboxModel{DB: db}.Stats(ctx, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Queue.Ready != 1 || stats.Queue.Scheduled != 1 || stats.NextRetry == nil {
		t.Errorf("queue = %+v nextRetry=%v, want 1 ready and 1 scheduled", stats.Queue, stats.NextRetry)
	}
	if stats.Today.Sent != 1 || stats.Today.Failed != 1 {
		t.Errorf("today = %+v, want 1 sent and 1 failed", stats.Today)
	}
	if len(stats.Throughput) != 30 {
		t.Fatalf("throughput has %d points, want one per minute (30)", len(stats.Throughput))
	}
	last := stats.Throughput[len(stats.Throughput)-1]
	if last.Sent != 1 || last.Failed != 1 {
		t.Errorf("current minute = %+v, want 1 sent and 1 failed", last)
	}
	byRecipient := map[string]string{}
	for _, event := range stats.Recent {
		byRecipient[event.Recipient] = event.Status
	}
	if byRecipient["a@example.com"] != "sent" || byRecipient["b@example.com"] != "failed" || byRecipient["c@example.com"] != "retrying" {
		t.Errorf("recent events = %v", byRecipient)
	}

	campaigns := data.CampaignModel{DB: db}
	campaign, err := campaigns.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if campaign.Breakdown["sent"] != 1 || campaign.Breakdown["retrying"] != 1 || campaign.Breakdown["queued"] != 1 {
		t.Errorf("breakdown = %v", campaign.Breakdown)
	}
	page := data.Page{MaxSize: 100}
	if rows, total, _ := campaigns.Recipients(ctx, id, data.RecipientFilter{Status: "failed"}, page); total != 1 || rows[0].Email != "b@example.com" {
		t.Errorf("status filter returned %d rows", total)
	}
	if _, total, _ := campaigns.Recipients(ctx, id, data.RecipientFilter{Search: "C@EXAMPLE"}, page); total != 1 {
		t.Errorf("case-insensitive search returned %d rows, want 1", total)
	}
}
