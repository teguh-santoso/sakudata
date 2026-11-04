# SakuData API Reference

Base URL: `https://<your-host>` (or `http://127.0.0.1:8090` locally).

- [Authentication](#authentication)
- [Errors](#errors)
- [Health and metadata](#health-and-metadata)
- [Admin auth](#admin-auth)
- [Databases](#databases)
- [Collections](#collections)
- [Records](#records)
- [Auth collections](#auth-collections)
- [Files](#files)
- [Realtime](#realtime)
- [Export and import](#export-and-import)
- [SQL console](#sql-console)
- [Settings](#settings)
- [Logs](#logs)
- [Backups](#backups)
- [API tokens](#api-tokens)
- [Superusers](#superusers)

## Authentication

Three credential types are accepted:

| Credential | Header | Scope |
| --- | --- | --- |
| Superuser JWT (`/api/admin/login`) | `Authorization: <jwt>` | everything |
| Auth-collection user JWT | `Authorization: <jwt>` | one database + collection |
| API token (`mdb_...`) | `Authorization: <token>` or `X-API-Key` | data access to one database |

`Authorization: Bearer <token>` is also accepted. For SSE you may pass
`?token=<token>` because `EventSource` cannot set headers.

Database URLs use the **opaque database ID**, not the database name:

```bash
curl https://host/api/admin/databases -H "Authorization: $ADMIN_TOKEN"
# [{"id":"298461b6...","name":"my_db","created":"..."}]
```

## Errors

```json
{"code":401,"message":"Missing or invalid token.","data":{}}
```

| Status | Meaning |
| --- | --- |
| 400 | Invalid input or SQL |
| 401 | Missing/invalid credential |
| 403 | Credential valid but not allowed |
| 404 | Resource not found |
| 409 | Conflict (duplicate name/email) |
| 429 | Login rate limit exceeded |
| 500 | Internal error |

Anonymous requests to unknown databases/collections intentionally return `401`.

## Health and metadata

```http
GET /api/health
GET /api/meta
```

`/api/meta` returns app name, version, supported field types and whether the
first superuser still needs to be created.

## Admin auth

```http
POST /api/admin/setup
{"email":"admin@example.com","password":"min8chars"}

POST /api/admin/login
{"email":"admin@example.com","password":"..."}
```

Both return:

```json
{"token":"<jwt>","record":{"id":"...","email":"..."}}
```

`/api/admin/setup` returns `409` if a superuser already exists. Superuser tokens
expire after 12 hours.

## Databases

```http
GET    /api/admin/databases
POST   /api/admin/databases          {"name":"my_db"}
DELETE /api/admin/databases/{id}
```

- Names must match `[A-Za-z][A-Za-z0-9_]{0,63}` and be unique.
- Create returns `{id,name,created}`.
- Delete removes the database file, records and stored files.
- Legacy: name-based URLs are still accepted where an ID is expected.

## Collections

```http
GET    /api/d/{dbID}/collections
POST   /api/d/{dbID}/collections
GET    /api/d/{dbID}/collections/{name}
PATCH  /api/d/{dbID}/collections/{name}
PUT    /api/d/{dbID}/collections/{name}
DELETE /api/d/{dbID}/collections/{name}
```

Create/update body:

```json
{
  "name": "posts",
  "type": "base",
  "fields": [
    {"name":"title","type":"text","required":true,"unique":false},
    {"name":"price","type":"number"},
    {"name":"status","type":"select","options":["draft","published"]}
  ],
  "rules": {
    "list":"*",
    "view":"*",
    "create":"@request.auth.id != \"\"",
    "update":"",
    "delete":""
  },
  "hooks": {"create":["https://example.com/hook"]},
  "options": {"minPasswordLength": 8}
}
```

Field types: `text`, `number`, `bool`, `json`, `file`, `email`, `url`, `date`,
`select`, `relation`, `autodate`.

Auth collections require `email` (`text`/`email`) and `password` (`text`)
fields. Updating `fields` migrates the table and preserves existing rows.

Collection responses include `name`, `type`, `fields`, `rules`, `hooks`,
`options`.

## Records

```http
GET    /api/d/{dbID}/c/{col}/records
POST   /api/d/{dbID}/c/{col}/records
GET    /api/d/{dbID}/c/{col}/records/{id}
PATCH  /api/d/{dbID}/c/{col}/records/{id}
DELETE /api/d/{dbID}/c/{col}/records/{id}
```

List query parameters:

| Parameter | Description |
| --- | --- |
| `page` | 1-based page (default 1) |
| `perPage` | 1–200 (default 20) |
| `sort` | `-created,name` (`-` = descending) |
| `filter` | expression, e.g. `aktif = true && nama ~ "buk"` |
| `fields` | comma-separated response fields |
| `skipTotal` | `1` to skip the count query |

List response:

```json
{"page":1,"perPage":20,"totalItems":2,"totalPages":1,"items":[...]}
```

Filter syntax:

- `=`, `==`, `!=`, `>`, `>=`, `<`, `<=`, `~`, `!~`
- `&&`, `||`, `!`, parentheses
- `@request.auth.id`, `@request.auth.email`, `@request.auth.collectionName`
- `{:param}` placeholders read from the query string

Example:

```bash
curl -G "https://host/api/d/$DB/c/posts/records" \
  -H "Authorization: $TOKEN" \
  --data-urlencode 'filter=status = "published" && title ~ "hello"' \
  --data-urlencode 'sort=-created' \
  --data-urlencode 'page=1' --data-urlencode 'perPage=50'
```

## Auth collections

```http
POST /api/d/{dbID}/c/{col}/auth/login
{"identity":"user@example.com","password":"..."}

POST /api/d/{dbID}/c/{col}/auth/refresh
Authorization: <user-token>
```

Login returns `{token, record}`. Users must be `verified: true`; create or update
records in the auth collection with `"verified": true` as a superuser. User
tokens expire after 7 days.

## Files

```http
POST   /api/d/{dbID}/c/{col}/records/{id}/files/{field}        multipart "file"
GET    /api/d/{dbID}/c/{col}/records/{id}/files/{field}
GET    /api/d/{dbID}/c/{col}/records/{id}/files/{field}/{name}
DELETE /api/d/{dbID}/c/{col}/records/{id}/files/{field}
```

Max upload size: 10 MB. File names are sanitized. Storage backend is local disk
or S3 depending on settings.

## Realtime

```http
GET /api/d/{dbID}/c/{col}/subscribe?token=<token>
```

Server-Sent Events stream. Each event:

```json
{"event":"create","db":"my_db","collection":"posts","record":{...}}
```

`event` is `create`, `update` or `delete`. Webhooks configured per collection
receive the same JSON via HTTP POST.

## Export and import

```http
GET  /api/d/{dbID}/c/{col}/export?format=json
GET  /api/d/{dbID}/c/{col}/export?format=csv
POST /api/d/{dbID}/c/{col}/import
```

Export respects the `list` rule and current `filter`. Import expects a JSON array
(max 5000 rows) and returns:

```json
{"created":2,"failed":0,"errors":[]}
```

## SQL console

```http
POST /api/d/{dbID}/sql
{"query":"SELECT * FROM \"c_posts\" LIMIT 20","params":[]}
```

Read statements (`SELECT`, `WITH`, `PRAGMA`, `EXPLAIN`, `VALUES`) return:

```json
{"type":"query","columns":["id"],"rows":[["1"]],"rowCount":1,"truncated":false,"elapsedMs":3}
```

Other statements return:

```json
{"type":"exec","rowsAffected":1,"elapsedMs":2}
```

Limits: superuser only, one statement, 20 s timeout, 1000 rows, 100 KB query.
API tokens are rejected.

## Settings

```http
GET /api/admin/settings
PUT /api/admin/settings
```

`GET` returns `{"app":{...},"s3":{...},"backend":"local|s3"}`. Secrets are masked
with `••••••`. `PUT` accepts a flat object of keys, for example:

```json
{
  "app_name":"SakuData",
  "app_base_url":"https://data.example.com",
  "backup_enabled":"1",
  "backup_interval_hours":"24",
  "backup_keep":"7",
  "s3_enabled":"1",
  "s3_endpoint":"https://s3.example.com",
  "s3_region":"auto",
  "s3_bucket":"sakudata",
  "s3_access":"...",
  "s3_secret":"...",
  "s3_prefix":"prod"
}
```

## Logs

```http
GET /api/admin/logs?page=1&perPage=30&level=info&db=<name>
```

Returns an envelope with `items`. Each item has `created`, `level`, `message`,
`data`, `actor`, `db`, `collection`.

## Backups

```http
GET    /api/admin/backups
POST   /api/admin/backups
GET    /api/admin/backups/{name}
DELETE /api/admin/backups/{name}
POST   /api/admin/backups/{name}/restore
```

List returns `{"items":[{name,size,modified,source}],"s3":true|false}` where
`source` is `local`, `s3` or `local+s3`.

Restore stages the archive into `data/restore-pending` and returns `202` with
`restartRequired: true`. The snapshot is applied on the next start.

## API tokens

```http
GET    /api/admin/tokens
POST   /api/admin/tokens        {"name":"ci","dbId":"<DB_ID>"}
DELETE /api/admin/tokens/{id}
```

Create returns the raw token **once**:

```json
{"id":"...","name":"ci","dbId":"...","dbName":"my_db","prefix":"mdb_abcd1234","created":"...","token":"mdb_..."}
```

Use it as `Authorization: mdb_...` or `X-API-Key: mdb_...`. Scope: records,
files and export/import for that database only. Revocation is immediate.

## Superusers

```http
GET    /api/admin/superusers
POST   /api/admin/superusers          {"email":"...","password":"..."}
PATCH  /api/admin/superusers/{id}     {"email":"...","password":"..."}
DELETE /api/admin/superusers/{id}
```

At least one superuser must remain.
