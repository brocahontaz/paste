# paste

A lightweight, anonymous paste service in pure Go. No accounts, no tracking,
no JavaScript frameworks — create a paste, share the link, delete it with the
one-time token you received at creation time.

## Features

- **Formats** — plain text, markdown (server-rendered via goldmark, raw HTML
  escaped), or source code (client-side highlighting via a vendored
  highlight.js; auto-detect or an explicit language).
- **Expiry** — 1 hour, 1 day, 7 days, 30 days, or never. Expired pastes read
  as 404; a background loop purges expired rows.
- **Burn after read** — the paste is destroyed immediately after the first
  successful view (or raw fetch).
- **One-time delete token** — ≥32 bytes of CSPRNG entropy, shown exactly
  once; only its SHA-256 hash is stored, and verification is constant-time.
- **Rate limited** — per client IP: 5 creates/min and 60 reads/min by
  default, with `Retry-After` on 429s.
- **Not indexed** — `X-Robots-Tag: noindex, nofollow` on every response plus
  a `robots.txt` that disallows everything.
- **Secure defaults** — strict CSP (`script-src 'self'`, no `unsafe-inline`),
  `nosniff`, `no-referrer`, `X-Frame-Options: DENY`, `Cache-Control: no-store`
  on paste pages, and `html/template` auto-escaping everywhere.

## Endpoints

| Method | Path                   | Purpose                                   |
| ------ | ---------------------- | ----------------------------------------- |
| GET    | `/`                    | Create form                               |
| POST   | `/`                    | Create a paste                            |
| GET    | `/{id}`                | View a paste                              |
| GET    | `/raw/{id}`            | Exact content as `text/plain`             |
| GET    | `/{id}/delete/{token}` | Delete confirmation page                  |
| POST   | `/{id}/delete`         | Delete a paste (token form field)         |
| GET    | `/healthz`             | Health check (200 + DB ping)              |
| GET    | `/robots.txt`          | `Disallow: /`                             |
| GET    | `/static/*`            | Embedded static assets                    |

Unknown IDs, invalid IDs, and expired pastes all return a styled 404;
unsupported methods return 405.

## Development

Requirements: Go ≥ 1.27.

```sh
make fmt     # gofmt
make vet     # go vet
make test    # go test ./...
make build   # static binary -> bin/paste
make run     # go run ./cmd/paste
```

The server listens on `:8080` and stores data in `data/paste.db` by default.
All assets (templates, CSS, JS, highlight.js) are embedded in the binary.

## Configuration (environment variables)

| Variable                    | Default        | Meaning                                     |
| --------------------------- | -------------- | ------------------------------------------- |
| `PASTE_BIND_ADDR`           | `:8080`        | Listen address                              |
| `PASTE_BASE_URL`            | *(derived)*    | Absolute base URL for share/raw links       |
| `PASTE_DB_PATH`             | `data/paste.db`| SQLite file (parent dir is created)         |
| `PASTE_MAX_BODY_BYTES`      | `1048576`      | Max paste/form size (413 above this)        |
| `PASTE_CREATE_RATE_PER_MIN` | `5`            | Creates per IP per minute (burst = rate)    |
| `PASTE_READ_RATE_PER_MIN`   | `60`           | Reads per IP per minute                     |
| `PASTE_CLEANUP_INTERVAL`    | `1m`           | Go duration between purge runs              |
| `PASTE_TRUST_PROXY`         | `false`        | Trust `X-Forwarded-For`/`-Proto` headers    |

Invalid values fall back to the defaults. Only enable `PASTE_TRUST_PROXY`
behind a proxy that overwrites these headers; otherwise clients can spoof
their rate-limit identity.

## Docker deployment

```sh
docker compose up -d --build
curl http://localhost:8080/healthz
```

The image is a distroless static binary running as nonroot with an exec-form
`HEALTHCHECK` (`/paste healthcheck`). Compose persists the database in the
`paste-data` volume mounted at `/data` (`PASTE_DB_PATH=/data/paste.db`) and
restarts the container unless it was stopped explicitly.

## Data persistence

SQLite with WAL mode and a 5 s busy timeout. Everything lives in the single
database file — copy or back it up while the service is stopped, or snapshot
the Docker volume. IDs are 10-character base62 strings generated with
`crypto/rand`.

## Security notes

- Paste content is served exactly as submitted (`text/plain` raw view with
  `nosniff`); HTML views escape all user content, and markdown rendering
  never passes raw HTML through (goldmark without `Unsafe`).
- Delete tokens are never logged; nothing about requests other than errors
  is logged at all. Token lookup compares SHA-256 hashes in constant time.
- Wrong delete tokens and unknown IDs both return 404, so paste existence is
  never leaked.
- The CSP bans inline scripts and styles; JS/CSS is served from `/static/`.
