<p align="center">
  <img src="docs/logo.svg" alt="SakuData" width="96" height="96">
</p>

<h1 align="center">SakuData</h1>

<p align="center">
  A self-hosted, PocketBase-inspired backend for multiple isolated SQLite databases.
</p>

<p align="center">
  <a href="README.id.md">Bahasa Indonesia</a> ·
  <a href="docs/API.md">API reference</a> ·
  <a href="docs/API.id.md">Referensi API (ID)</a>
</p>

---

SakuData runs one Go binary that hosts many independent SQLite databases. Each
database has its own collections, records, auth users, API rules, files and
scheduled snapshots. The admin UI is a small vanilla-JS SPA embedded in the
binary; there is no Node build step.

## Table of contents

- [Features](#features)
- [Architecture](#architecture)
- [Requirements](#requirements)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Authentication](#authentication)
- [Collections and field types](#collections-and-field-types)
- [API rules and filters](#api-rules-and-filters)
- [Records API](#records-api)
- [Files](#files)
- [Realtime and webhooks](#realtime-and-webhooks)
- [API tokens](#api-tokens)
- [SQL console](#sql-console)
- [Backups](#backups)
- [Export and import](#export-and-import)
- [Security](#security)
- [Deployment](#deployment)
- [Development](#development)
- [Project layout](#project-layout)
- [License](#license)

## Features

- **Multi-database**: create, switch and delete isolated SQLite databases.
  URLs use opaque database IDs, never names.
- **Collections**: `base` and `auth` collections with a visual schema editor
  (add, reorder, edit and remove fields, including safe table migrations).
- **Field types**: `text`, `number`, `bool`, `json`, `file`, `email`, `url`,
  `date`, `select`, `relation`, `autodate`.
- **Records API**: pagination, multi-column sorting, filter expressions and
  field selection.
- **Access rules per operation** (`list`, `view`, `create`, `update`, `delete`)
  with presets and a visual condition builder.
- **Authentication**: argon2id password hashing, HS256 JWT, per-collection auth
  users, superusers, and per-database API tokens with instant revocation.
- **Files**: local disk or any S3-compatible object storage.
- **Realtime**: Server-Sent Events per collection, plus webhooks.
- **Backups**: automatic scheduled snapshots, S3 upload, retention, download and
  staged restore.
- **SQL console** per database for superusers (row-capped, time-limited).
- **Export/import** records as JSON or CSV / JSON.
- **Audit log**, application settings, mail settings and superuser management.
- **Responsive admin UI** with light/dark themes, collapsible sidebar and a
  minimal pocket logo.

## Architecture

```
Browser (vanilla ES modules, embedded)
        │
        ▼
Go net/http server ── middleware (security headers, CORS, rate limit, logs)
        │
        ├── /api/admin/*      superuser API (JWT)
        ├── /api/d/{id}/*     per-database data API (JWT user, API token)
        └── /_/               admin SPA
        │
        ├── system.db         admins, databases registry, settings, logs,
        │                     API tokens
        └── data/dbs/*.db     one SQLite file per database
            data/files/*      local file storage
            data/backups/*    local snapshots
```

- Each tenant database stores its collection metadata in `_collections` and one
  table per collection (`c_<name>`).
- Schema changes rebuild the SQLite table inside a transaction and preserve
  existing rows.
- Backups copy live databases with SQLite `VACUUM INTO` for consistency.

## Requirements

- Go **1.26+** (standard library HTTP, pure-Go SQLite via `modernc.org/sqlite`).
- No CGO, Node.js or external database required.
- Optional: an S3-compatible bucket (AWS S3, MinIO, R2, Backblaze B2, …).

## Quick start

```bash
git clone https://github.com/teguh-santoso/sakudata.git
cd sakudata
go run . -addr 127.0.0.1:8090 -data ./data
```

Open <http://127.0.0.1:8090/_/>. On a fresh instance the UI asks you to create
the first superuser. Equivalent API call:

```bash
curl -X POST http://127.0.0.1:8090/api/admin/setup \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"at-least-8-chars"}'
```

Build a static binary:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o sakudata .
./sakudata -addr 0.0.0.0:8090 -data /var/lib/sakudata
```

Docker:

```bash
docker compose up -d --build
# or
docker build -t sakudata .
docker run -p 8090:8090 -v sakudata-data:/data sakudata
```

## Configuration

Command-line flags:

| Flag | Default | Description |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8090` | HTTP listen address |
| `-data` | `./data` | Data directory |
| `-restore` | – | Restore a `.tar.gz` backup into `-data` and exit |

Settings are stored in `system.db` and edited from **Settings** / **Backups**:

| Key | Description |
| --- | --- |
| `app_name`, `app_base_url` | Branding and public URL |
| `mail_from`, `mail_host`, `mail_port`, `mail_user`, `mail_pass` | SMTP settings |
| `s3_enabled`, `s3_endpoint`, `s3_region`, `s3_bucket`, `s3_access`, `s3_secret`, `s3_prefix`, `s3_pathstyle` | S3-compatible storage |
| `backup_enabled`, `backup_interval_hours`, `backup_keep`, `backup_last` | Automatic backups |

Secrets (`s3_secret`, `mail_pass`) are never returned in full by the API.

## Authentication

### Superuser token

```bash
curl -X POST https://your-host/api/admin/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"..."}'
```

Returns `{"token":"...","record":{...}}`. Valid for 12 hours. Superuser tokens
bypass every collection rule and can manage all databases.

### Auth-collection user token

```bash
curl -X POST https://your-host/api/d/<DB_ID>/c/<collection>/auth/login \
  -H "Content-Type: application/json" \
  -d '{"identity":"user@example.com","password":"..."}'
```

Valid for 7 days. Scoped to that database **and** collection. Users must have
`verified: true`. Refresh:

```bash
curl -X POST https://your-host/api/d/<DB_ID>/c/<collection>/auth/refresh \
  -H "Authorization: <user-token>"
```

### Sending the token

```http
Authorization: <token>
# or
Authorization: Bearer <token>
# API tokens may also use:
X-API-Key: mdb_...
```

For SSE (`/subscribe`), EventSource cannot set headers, so `?token=` is accepted.

## Collections and field types

A collection has a name, type and fields. Field options:

- `required` — reject empty values.
- `unique` — create a unique index.
- `options` — allowed values for `select`.

`id`, `created` and `updated` are system columns and cannot be redefined.
Auth collections require an `email` field (type `text` or `email`) and a
`password` field (type `text`); passwords are hashed with argon2id and are never
returned by the API.

## API rules and filters

Each operation has a rule:

| Value | Meaning |
| --- | --- |
| `""` (empty) | Superusers only |
| `*` | Public |
| `user` | Any authenticated user of the same database and collection |
| expression | Per-record filter, e.g. `owner = @request.auth.id` |

Expression syntax:

- Comparisons: `=`, `==`, `!=`, `>`, `>=`, `<`, `<=`, `~` (contains), `!~`.
- Logic: `&&`, `||`, `!`, parentheses.
- Literals: strings (`"x"` or `'x'`), numbers, `true`, `false`, `null`.
- Request context: `@request.auth.id`, `@request.auth.email`,
  `@request.auth.collectionName`.
- Query placeholders: `{:name}` bound from the query string.

Identifiers are validated and all values are bound as SQL parameters. The admin
UI includes a **rule builder** with presets and visual condition rows.

## Records API

```bash
# list
curl "https://host/api/d/<DB_ID>/c/<collection>/records?page=1&perPage=20&sort=-created,name" \
  -H "Authorization: <token>"

# filter + placeholders
curl -G "https://host/api/d/<DB_ID>/c/<collection>/records" \
  -H "Authorization: <token>" \
  --data-urlencode 'filter=aktif = true && nama ~ "buk"' \
  --data-urlencode 'sort=-harga'

# create / update / delete
curl -X POST "https://host/api/d/<DB_ID>/c/<collection>/records" \
  -H "Authorization: <token>" -H "Content-Type: application/json" \
  -d '{"title":"Hello"}'
```

Query parameters: `page`, `perPage` (max 200), `sort`, `filter`, `fields`,
`skipTotal`.

Response envelope:

```json
{"page":1,"perPage":20,"totalItems":2,"totalPages":1,"items":[...]}
```

## Files

```http
POST   /api/d/{dbID}/c/{col}/records/{id}/files/{field}
GET    /api/d/{dbID}/c/{col}/records/{id}/files/{field}/{name}
DELETE /api/d/{dbID}/c/{col}/records/{id}/files/{field}
```

- Local storage: `data/files/<db>/<collection>/<record>/`.
- S3: configure credentials under **Settings → File storage**.
- File names are sanitized; max upload size is 10 MB.

## Realtime and webhooks

- SSE: `GET /api/d/{dbID}/c/{col}/subscribe` (auth via header or `?token=`).
- Webhooks per collection: `create`, `update`, `delete` → HTTP POST with JSON
  event `{event, db, collection, record}`.

## API tokens

Machine credentials per database, created from the **API** page.

- Scope: full data access (records, files, export/import) to **one database**.
- Cannot: manage collections/schema, settings, backups, SQL, superusers.
- Raw token (`mdb_...`) is shown once; only its SHA-256 hash is stored.
- Revocation is immediate.

```bash
TOKEN=$(curl -s -X POST https://host/api/admin/tokens \
  -H "Authorization: <admin-token>" -H "Content-Type: application/json" \
  -d '{"name":"ci","dbId":"<DB_ID>"}' | jq -r .token)

curl https://host/api/d/<DB_ID>/c/<collection>/records \
  -H "Authorization: $TOKEN"
```

## SQL console

Superusers can run one SQL statement per request:

```http
POST /api/d/{dbID}/sql
{"query":"SELECT * FROM \"c_posts\" LIMIT 20"}
```

- Read statements return `{type:"query", columns, rows, rowCount, truncated, elapsedMs}`.
- Write statements return `{type:"exec", rowsAffected, elapsedMs}`.
- Limits: 20 s timeout, 1000 returned rows, 100 KB query.
- Only superuser tokens are accepted (not API tokens).

## Backups

Snapshots are tar.gz archives containing `system.db`, every tenant database
(copied with `VACUUM INTO`) and `files/`. They are stored in `data/backups/` and
uploaded to `<s3_prefix>/backups/` when S3 is enabled.

- Automatic: enable in the Backups page; `backup_interval_hours` defaults to 24,
  `backup_keep` defaults to 7 (pruned locally and in S3).
- Manual: `POST /api/admin/backups`.
- List/download/delete: `/api/admin/backups[/{name}]`.
- Restore: `POST /api/admin/backups/{name}/restore` stages the archive into
  `data/restore-pending` and applies it on the next start. The previous data is
  kept in `restore-old-<timestamp>/`.
- Offline restore: `sakudata -data /var/lib/sakudata -restore backup.tar.gz`.

## Export and import

```http
GET  /api/d/{dbID}/c/{col}/export?format=json
GET  /api/d/{dbID}/c/{col}/export?format=csv
POST /api/d/{dbID}/c/{col}/import     # JSON array, max 5000 rows
```

Exports respect the collection's `list` rule and the current filter.

## Security

- Argon2id password hashing; JWT signed with a secret generated on first run.
- API responses and the admin UI send `Cache-Control: no-store`.
- Anonymous requests to unknown databases/collections return `401`, so resource
  existence is not disclosed.
- Login endpoints are rate limited (30 attempts/minute per IP).
- Security headers, CORS preflight, panic recovery, request logging and graceful
  shutdown are included.
- Secrets are masked in API responses.
- Keep `.credentials`, `.env`, key files and `data/` out of version control.

## Deployment

See [`deploy/README.md`](deploy/README.md). Options:

- systemd + Caddy (recommended): `deploy/sakudata.service`, `deploy/Caddyfile`,
  `deploy/deploy.sh`.
- Docker: `Dockerfile` + `docker-compose.yml`.

## Development

```bash
go test ./...
go vet ./...
gofmt -l .
```

The admin UI lives in `internal/ui/assets/` and is embedded with `//go:embed`.
Asset URLs are versioned (`assets/vN/...`) so CDN caches cannot serve stale
modules after a deploy.

## Project layout

```
main.go                     entry point, flags, restore-on-start
internal/api/               HTTP routes, handlers, middleware, backups, SQL
internal/auth/              argon2id password hashing and JWT
internal/backup/            tar.gz snapshot create/list/extract
internal/rules/             filter/rule parser and SQL compiler
internal/store/             system.db: admins, databases, settings, logs, tokens
internal/tenant/             per-database manager, schema migrations, records,
                            file storage (local/S3), realtime hub
internal/ui/assets/         admin SPA (vanilla ES modules)
docs/                       logo, API reference (EN/ID)
deploy/                     systemd, Caddy, deploy script, deployment guide
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
