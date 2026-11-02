# AI4ALL Certificate Mailer

A Windows desktop app for sending personalized PDF certificates by email. Import
a CSV of students, preview each certificate, and watch every email leave the
queue. Built with Go and [Wails](https://wails.io); the interface is a Next.js
app exported to static files and embedded in the executable.

The app keeps its data in **PostgreSQL** and runs its delivery queue on
**Redis**. You bring both, for example from Aiven's free plans; the app asks for
them on first start.

## Installing and first start

1. Build or download `certificate-mailer.exe` (see [Building](#building)). It
   needs the Microsoft Edge WebView2 runtime, which Windows 10 and 11 already
   include.
2. Create a database and a Redis instance. With [Aiven](https://console.aiven.io):
   - a free **PostgreSQL** service: copy its **Service URI**, which ends in
     `?sslmode=require`;
   - a free **Valkey** service (Aiven's Redis-compatible store): copy its
     **Service URI**, which starts with `rediss://`.
3. Start the app. The setup screen asks for both URLs (use **Test connections**
   to check them) and for the admin email and password you will sign in with.
4. The app creates its tables, then shows the sign-in screen.

Setup is stored on this computer, in
`%APPDATA%\AI4ALL Certificate Mailer\config.json`, readable only by your
Windows account. Logs are in the `logs` folder next to it.

**If the app cannot connect later** (no network, a changed password), it shows
the error with **Retry** and lets you enter new connection URLs.

**Forgot the admin password?** Close the app, remove `adminEmail` and
`adminPasswordHash` from `config.json`, and start it again: it asks for a new
admin account and keeps everything else.

**Several computers, one database:** set up the first computer, then copy the
encryption key from **Settings → Connections**. On the next computer, choose
"Using a database another computer already set up?" during setup and paste it.
The key protects the saved SMTP password; without it, that password has to be
entered again.

## Using the app

1. **Settings → Sending account:** enter your SMTP server. Gmail needs an App
   Password. Brevo, SendGrid and Mailgun work too. Use **Send test** to check.
2. **Settings → Delivery limits:** set your provider's limits (Gmail: 500 a day,
   Brevo free: 300 a day).
3. **New campaign:** upload a CSV with `student_name` and `email` columns and an
   optional `certificate_number` column. Other column names are detected too,
   and you can map columns by hand. Invalid rows, duplicates and likely typos
   are flagged before anything is sent.
4. Write the message. `{{student_name}}`, `{{email}}` and
   `{{certificate_number}}` are replaced for each student.
5. Preview certificates, review, and send.

**Keep the app open while a campaign is sending.** Closing it pauses sending
(you are asked first). Nothing is lost: the queue is in PostgreSQL and sending
continues the next time the app starts.

### Certificates

Replace the template under **Settings → Certificate template** with a PDF or
JPG (A4 landscape, up to 20 MB). It is checked by rendering a test certificate
and stored in PostgreSQL. Until then the bundled `assests/template.jpg` is used.

The student name is centred `60` mm from the top at `24` pt. The certificate
number is placed `15` mm from the left and `15` mm from the bottom at `10` pt;
rows without a number get none. To move them for a custom template, add all six
values to `config.json` (A4 millimetres) and restart the app:

```json
"certificate": { "nameX": 0, "nameY": 60, "nameSize": 24, "numberX": 15, "numberY": 15, "numberSize": 10 }
```

## Building

Requirements: Go 1.26, [Bun](https://bun.sh) 1.3, and the Wails CLI:

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1
```

```sh
make build/windows             # build/bin/certificate-mailer.exe
make build/windows/installer   # plus an installer; needs NSIS (makensis)
```

`wails build` exports the UI (`web/out`) and embeds it, the migrations and the
default template into the executable. Windows builds cross-compile from Linux
and macOS.

## Development

```sh
make run/deps      # PostgreSQL (localhost:5434) and Redis (localhost:6380) in Docker
make run/mailpit   # SMTP catcher on localhost:1025, inbox at http://localhost:8025
make run/dev       # the app's backend and UI in a browser: http://127.0.0.1:4000
make run/web       # UI with hot reload at http://localhost:3000, proxying /api to run/dev
make test          # unit tests with the race detector
make test/integration  # outbox and full-setup tests against throwaway containers
make audit         # mod verify, gofmt, vet, golangci-lint, tests
```

`run/dev` serves exactly what the desktop window gets, so the whole app can be
developed and tested in a browser, including on Linux where the Wails window
needs WebKitGTK. It keeps its setup in `.devdata/`. For local services, enter
`postgres://user:password@localhost:5434/emailclient?sslmode=disable` and
`redis://localhost:6380/0`, and use host `localhost`, port `1025` for SMTP with
Mailpit.

### Layout

| Path | What it is |
|---|---|
| `main.go` | Wails entry point: window, close confirmation, log file |
| `internal/desktop` | Setup, `config.json`, and the window's request handler |
| `internal/server` | The JSON API and delivery workers, started once connected |
| `internal/outbox`, `internal/delivery` | Transactional outbox, workers, retries |
| `internal/certificate` | PDF rendering |
| `migrations` | SQL migrations, applied on every start |
| `cmd/devserver` | The same handler over HTTP for browser development |
| `web` | The Next.js interface, exported to `web/out` |

## Delivery architecture

All email goes through one transactional outbox:

```
 action in the app ──▶ PostgreSQL transaction ────────────▶ outbox_messages (pending)
                       (campaign/recipients + outbox rows)        │
                                                                  ▼  relay (SKIP LOCKED,
                                                                  │  round-robin across campaigns)
                                       Redis stream outbox:messages (consumer group "mailers")
                                                                  │
                                                                  ▼  4 workers, each with a
                                                                  │  reusable SMTP connection
                            Redis GCRA limiter + daily quota ◀────┤  (shared by every computer)
                                                                  ▼
                                      conditional claim (published ─▶ sending, lease)
                                      render certificate in memory ─▶ SMTP
                                      sent | retry with backoff | failed  (one transaction)
```

- **PostgreSQL is the source of truth.** A message exists only if the change that
  created it committed. Redis can lose data; the reaper rebuilds the stream.
- **At-least-once delivery.** A worker sends only after atomically claiming
  `published ─▶ sending`, so duplicate stream entries are skipped. Every message
  has a stable `Message-ID`, so a resend after a crash is collapsed by mail
  providers.
- **Retries:** network errors, 4xx replies, authentication and quota errors retry
  with exponential backoff (30 s, 1 m, 2 m … max 30 m), up to 5 attempts.
  Permanent 5xx replies (unknown mailbox) fail at once. When the daily limit is
  reached, messages wait for the next UTC day without using an attempt.
- **Recovery:** the reaper re-queues messages whose worker lease expired, for
  example after the app was closed mid-send.
- **Several computers** can run the app against one database and Redis. Sending
  limits, sessions and settings changes are shared through Redis.

`GET /debug/vars` (signed in) exposes outbox metrics: published, sent, retried,
deferred, failed, duplicates skipped, reclaimed leases, average send time, and
queue depth by status.
