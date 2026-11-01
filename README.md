# Send Bulk Email API using GO 

![Email API](https://github.com/mayura-andrew/send-bulk-email-client-api/assets/48531182/243b444d-9284-4008-be17-f678fe602c75)


This API powers AI4ALL certificate delivery: it sends personalized certificate emails and tracked one-off emails, and exposes endpoints to manage and monitor them.

## Endpoints

### POST /send (Status : Completed ☑️)

This endpoint is used to send an email. The request body should be a JSON object with the following fields:

- `recipient`: The email address of the recipient.
- `subject`: The subject of the email.
- `body`: The body of the email.
  
```
{
    "sender": "",
    "recipients": [],
    "subject": "example",
    "body": "example"
}
```

### POST /track  (Status : Completed ☑️)

This endpoint is used to update the tracking status of an email. The request body should be a JSON object with the following fields:

- `id`: The ID of the email to track.
- `opened`: A boolean indicating whether the email has been opened.
## How it works 

![image](https://github.com/mayura-andrew/send-bulk-email-client-api/assets/48531182/2c5f7568-97d3-46e3-8645-35663a5b43db)


### GET /successfullysent (Status : Not Started ❌)

This endpoint retrieves all emails that have been successfully sent. It returns a JSON array of email objects.

### GET /notsuccessfullysent  (Status : Not Started ❌)

This endpoint retrieves all emails that have not been successfully sent. It returns a JSON array of email objects.

### GET /totalcount  (Status : Not Started ❌)

This endpoint retrieves the total count of emails. It returns a JSON object with a single field, `count`, containing the count.

### GET /search  (Status : Not Started ❌)

This endpoint searches for emails based on certain criteria. It accepts the following query parameters:

- `recipient`: The email address to search for.
- `subject`: The subject to search for.
- `sent`: A boolean indicating whether to search for emails that have been sent (`true`) or not sent (`false`).
- `opened`: A boolean indicating whether to search for emails that have been opened (`true`) or not opened (`false`).

## Email Template  (Status : Completed ☑️)

The content of the emails is generated from a Go template file, `email_template.tmpl`. This file defines two templates, `subject` and `plainBody`, which are used to generate the subject and body of the email, respectively. The templates have access to the following data:

- `Subject`: The subject of the email.
- `Body`: The body of the email.
- `Recipient`: The email address of the recipient.

## Docker Usage

Copy the example environment file, fill in the SMTP values, and start the API,
PostgreSQL and Redis containers:

```sh
cp .env.example .env
docker compose up --build
```

The API is available at `http://localhost:4000`. PostgreSQL is exposed on
`localhost:5434` for local tools; the API connects to it through the Compose
service name `db`.

The application runs database migrations automatically when the API starts.

The admin UI is available at `http://localhost:3000`. Sign in with the
`ADMIN_EMAIL` and `ADMIN_PASSWORD` values from `.env`. Upload a CSV with
`student_name,email` columns, review validation results, then start the
campaign. Each valid row receives a personalized PDF generated from
`assests/template.jpg`.

After signing in, use **Settings** to configure Google SMTP with an App
Password. SMTP credentials are encrypted before being stored in PostgreSQL.
Use **Campaign history** to monitor sent/failed counts, inspect every recipient's
status, and retry failed recipients. Queued work is persisted in PostgreSQL, so
it survives API restarts (see [Delivery architecture](#delivery-architecture)).
The **Emails per batch** field is kept for compatibility but no longer affects
delivery; use the delivery settings below instead.

View live logs while testing:

```sh
docker compose logs -f --tail=100 api
docker compose logs -f --tail=100 web
docker compose logs -f --tail=100 db
```

The API logs SMTP test failures, campaign delivery failures, certificate
generation failures, and successful deliveries. Passwords are never logged.

Certificate placement uses A4 millimetres. The supplied template defaults to
centered text at `X=0`, `Y=60`, with font size `24`. Override
`CERTIFICATE_NAME_X`, `CERTIFICATE_NAME_Y`, and `CERTIFICATE_NAME_SIZE` in
`.env` if the name needs fine adjustment.

Administrators can replace the certificate template from **Settings** by
uploading a PDF or JPG up to 20 MB. The template is validated by rendering a
test certificate, stored in PostgreSQL so every replica uses it, and applied to
newly generated certificates. `CERTIFICATE_TEMPLATE` is only the default used
until a template has been uploaded. PDF templates receive the personalized student
name as a text overlay; JPG templates are converted to a new PDF.

## Delivery architecture

All email (certificate campaigns and `POST /api/v1/send`) goes through one
transactional outbox:

```
 HTTP request ──▶ PostgreSQL transaction ──────────────▶ outbox_messages (pending)
                  (campaign/recipients + outbox rows)          │
                                                               ▼  relay (every replica, SKIP LOCKED,
                                                               │  round-robin across campaigns,
                                                               │  capped by OUTBOX_MAX_INFLIGHT)
                                     Redis stream outbox:messages (consumer group "mailers")
                                                               │
                                                               ▼  N workers per replica, each with a
                                                               │  reusable SMTP connection
                         Redis GCRA limiter + daily quota ◀────┤  (global across replicas)
                                                               ▼
                                   conditional claim (published ─▶ sending, lease)
                                   render certificate in memory ─▶ SMTP
                                   sent | retry with backoff | failed  (one transaction)
```

- **PostgreSQL is the source of truth.** A message exists only if the business
  change committed. Redis can lose data and the reaper rebuilds the stream.
- **At-least-once delivery.** A worker can only send after atomically claiming
  `published ─▶ sending`, so duplicate stream entries are skipped. A crash after
  the SMTP server accepted a message but before it was recorded resends it;
  every message has a stable `Message-ID` so mail providers collapse that copy.
- **Retries:** network errors, 4xx replies, authentication and quota errors
  retry with exponential backoff (30 s, 1 m, 2 m ... max 30 m) up to
  `OUTBOX_MAX_ATTEMPTS`. Permanent 5xx replies (unknown mailbox) fail at once.
  When `SMTP_DAILY_LIMIT` is reached, messages wait for the next UTC day without
  using an attempt.
- **Recovery:** the reaper re-queues messages whose worker lease expired (crash)
  and re-publishes stale ones; workers take over stream entries abandoned by a
  dead consumer. On `SIGTERM` the API stops accepting work and lets in-flight
  sends finish.
- **Multiple replicas** are safe: sessions, rate limits, SMTP quota and settings
  changes (pub/sub) live in Redis, and the certificate template lives in
  PostgreSQL.

### Configuration

| Variable | Default | Purpose |
|---|---|---|
| `REDIS_URL` | `redis://localhost:6379/0` | Redis connection |
| `WORKER_CONCURRENCY` | `4` | SMTP workers (and connections) per replica |
| `SMTP_RATE_PER_SECOND` | `2` | Global send rate across replicas, `0` = unlimited |
| `SMTP_BURST` | `1` | Global send burst |
| `SMTP_DAILY_LIMIT` | `0` | Global sends per UTC day (Gmail 500, Workspace 2000), `0` = unlimited |
| `OUTBOX_MAX_ATTEMPTS` | `5` | Attempts before a message is marked failed |
| `OUTBOX_MAX_INFLIGHT` | `100` | Published-but-unfinished messages (keeps the stream short) |
| `OUTBOX_LEASE` | `2m` | How long a worker owns a claimed message |
| `API_RATE_LIMIT_RPS` / `API_RATE_LIMIT_BURST` | `10` / `20` | Per-client API limit |
| `TRUSTED_PROXIES` | (empty) | Load balancer CIDRs allowed to set `X-Forwarded-For` |
| `COOKIE_SECURE` | `false` | Set `true` behind HTTPS |
| `CORS_TRUSTED_ORIGINS` | `localhost:3000`, `localhost:5173` | Space-separated origins; add your production admin URL |
| `TRACKING_REDIRECT_URL` | (empty) | Where tracked-email link clicks go; empty returns a pixel |
| `RUN_MIGRATIONS` | `true` | Apply migrations at startup |

`GET /debug/vars` (admin only) exposes `outbox` metrics: published, sent,
retried, deferred, failed, duplicates skipped, reclaimed leases, average send
time and queue depth by status.

For local development without a real mailbox, run
`docker compose --profile dev up` and point SMTP at `mailpit:1025`; messages
appear at `http://localhost:8025`.

### API changes

- `POST /api/v1/send`, `GET /api/v1/sent`, `GET /api/v1/recipients/:email` and
  `/debug/vars` now require an admin session.
- `POST /api/v1/send` returns `202 Accepted` with `emailId` and `recipientIds`
  instead of waiting for delivery. The `From` address is always the configured
  SMTP sender.
- List endpoints accept `?page=` and `?page_size=` and return `metadata`.
- `POST /api/v1/auth/logout` revokes the session.
  `GET /api/v1/auth/session` checks it.

### Development commands

Run `make` to list every target. The common ones:

```sh
make run/deps          # PostgreSQL + Redis in Docker
make run/mailpit       # local SMTP catcher, UI at http://localhost:8025
make run/api           # API against the Docker databases
make test              # unit tests with the race detector
make test/integration  # outbox tests against throwaway PostgreSQL + Redis containers
make audit             # mod verify, gofmt, vet, golangci-lint, tests
make outbox/status     # queue depth and Redis consumer group state
```

Database targets use `EMAIL_API_DB_DSN` and `EMAIL_API_REDIS_URL`, which default to
the Compose ports (`localhost:5434` and `localhost:6380`). Override them in
`.envrc` or on the command line. They deliberately ignore a `DB_DSN` exported for
another project.

This application is also available as a Docker image and can be pulled from Docker Hub and run locally. 


https://hub.docker.com/r/mayuraandrew/go-email-client

---

Thank you 😄
