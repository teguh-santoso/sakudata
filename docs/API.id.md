# Referensi API SakuData

Base URL: `https://<host-anda>` (atau `http://127.0.0.1:8090` secara lokal).

- [Autentikasi](#autentikasi)
- [Error](#error)
- [Health dan metadata](#health-dan-metadata)
- [Auth admin](#auth-admin)
- [Database](#database)
- [Collection](#collection)
- [Record](#record)
- [Collection auth](#collection-auth)
- [File](#file)
- [Realtime](#realtime)
- [Export dan import](#export-dan-import)
- [SQL console](#sql-console)
- [Settings](#settings)
- [Log](#log)
- [Backup](#backup)
- [API token](#api-token)
- [Superuser](#superuser)

## Autentikasi

Tiga jenis kredensial:

| Kredensial | Header | Cakupan |
| --- | --- | --- |
| JWT superuser (`/api/admin/login`) | `Authorization: <jwt>` | semua |
| JWT user collection auth | `Authorization: <jwt>` | satu database + collection |
| API token (`mdb_...`) | `Authorization: <token>` atau `X-API-Key` | akses data satu database |

`Authorization: Bearer <token>` juga diterima. Untuk SSE gunakan
`?token=<token>` karena `EventSource` tidak bisa set header.

URL database memakai **ID opaque**, bukan nama:

```bash
curl https://host/api/admin/databases -H "Authorization: $ADMIN_TOKEN"
# [{"id":"298461b6...","name":"my_db","created":"..."}]
```

## Error

```json
{"code":401,"message":"Missing or invalid token.","data":{}}
```

| Status | Arti |
| --- | --- |
| 400 | Input/SQL tidak valid |
| 401 | Kredensial tidak ada/tidak valid |
| 403 | Kredensial valid tetapi tidak diizinkan |
| 404 | Resource tidak ditemukan |
| 409 | Konflik (nama/email duplikat) |
| 429 | Batas percobaan login |
| 500 | Error internal |

Request anonim ke database/collection yang tidak ada sengaja mengembalikan `401`.

## Health dan metadata

```http
GET /api/health
GET /api/meta
```

`/api/meta` mengembalikan nama aplikasi, versi, tipe field, dan status kebutuhan
setup superuser.

## Auth admin

```http
POST /api/admin/setup
{"email":"admin@example.com","password":"min8karakter"}

POST /api/admin/login
{"email":"admin@example.com","password":"..."}
```

Keduanya mengembalikan:

```json
{"token":"<jwt>","record":{"id":"...","email":"..."}}
```

`/api/admin/setup` mengembalikan `409` bila superuser sudah ada. Token superuser
berlaku 12 jam.

## Database

```http
GET    /api/admin/databases
POST   /api/admin/databases          {"name":"my_db"}
DELETE /api/admin/databases/{id}
```

- Nama harus cocok `[A-Za-z][A-Za-z0-9_]{0,63}` dan unik.
- Create mengembalikan `{id,name,created}`.
- Delete menghapus file database, record, dan file tersimpan.
- Kompatibilitas: URL berbasis nama masih diterima sebagai fallback.

## Collection

```http
GET    /api/d/{dbID}/collections
POST   /api/d/{dbID}/collections
GET    /api/d/{dbID}/collections/{name}
PATCH  /api/d/{dbID}/collections/{name}
PUT    /api/d/{dbID}/collections/{name}
DELETE /api/d/{dbID}/collections/{name}
```

Body create/update:

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

Tipe field: `text`, `number`, `bool`, `json`, `file`, `email`, `url`, `date`,
`select`, `relation`, `autodate`.

Collection auth wajib punya field `email` (`text`/`email`) dan `password`
(`text`). Mengubah `fields` memigrasikan tabel dan mempertahankan data lama.

## Record

```http
GET    /api/d/{dbID}/c/{col}/records
POST   /api/d/{dbID}/c/{col}/records
GET    /api/d/{dbID}/c/{col}/records/{id}
PATCH  /api/d/{dbID}/c/{col}/records/{id}
DELETE /api/d/{dbID}/c/{col}/records/{id}
```

Parameter list:

| Parameter | Keterangan |
| --- | --- |
| `page` | Halaman berbasis 1 (default 1) |
| `perPage` | 1–200 (default 20) |
| `sort` | `-created,name` (`-` = menurun) |
| `filter` | ekspresi, mis. `aktif = true && nama ~ "buk"` |
| `fields` | field respons dipisah koma |
| `skipTotal` | `1` untuk melewati query count |

Envelope list:

```json
{"page":1,"perPage":20,"totalItems":2,"totalPages":1,"items":[...]}
```

Sintaks filter:

- `=`, `==`, `!=`, `>`, `>=`, `<`, `<=`, `~`, `!~`
- `&&`, `||`, `!`, tanda kurung
- `@request.auth.id`, `@request.auth.email`, `@request.auth.collectionName`
- placeholder `{:param}` dari query string

Contoh:

```bash
curl -G "https://host/api/d/$DB/c/posts/records" \
  -H "Authorization: $TOKEN" \
  --data-urlencode 'filter=status = "published" && title ~ "hello"' \
  --data-urlencode 'sort=-created' \
  --data-urlencode 'page=1' --data-urlencode 'perPage=50'
```

## Collection auth

```http
POST /api/d/{dbID}/c/{col}/auth/login
{"identity":"user@example.com","password":"..."}

POST /api/d/{dbID}/c/{col}/auth/refresh
Authorization: <token-user>
```

Login mengembalikan `{token, record}`. User harus `verified: true`; buat/ubah
record collection auth dengan `"verified": true` sebagai superuser. Token user
berlaku 7 hari.

## File

```http
POST   /api/d/{dbID}/c/{col}/records/{id}/files/{field}        multipart "file"
GET    /api/d/{dbID}/c/{col}/records/{id}/files/{field}
GET    /api/d/{dbID}/c/{col}/records/{id}/files/{field}/{name}
DELETE /api/d/{dbID}/c/{col}/records/{id}/files/{field}
```

Maksimum upload 10 MB. Nama file disanitasi. Backend: disk lokal atau S3.

## Realtime

```http
GET /api/d/{dbID}/c/{col}/subscribe?token=<token>
```

Stream Server-Sent Events. Setiap event:

```json
{"event":"create","db":"my_db","collection":"posts","record":{...}}
```

`event` = `create`, `update`, atau `delete`. Webhook per collection menerima JSON
yang sama lewat HTTP POST.

## Export dan import

```http
GET  /api/d/{dbID}/c/{col}/export?format=json
GET  /api/d/{dbID}/c/{col}/export?format=csv
POST /api/d/{dbID}/c/{col}/import
```

Export mengikuti rule `list` dan `filter` aktif. Import menerima array JSON (maks
5000 baris) dan mengembalikan:

```json
{"created":2,"failed":0,"errors":[]}
```

## SQL console

```http
POST /api/d/{dbID}/sql
{"query":"SELECT * FROM \"c_posts\" LIMIT 20","params":[]}
```

Statement baca (`SELECT`, `WITH`, `PRAGMA`, `EXPLAIN`, `VALUES`) mengembalikan:

```json
{"type":"query","columns":["id"],"rows":[["1"]],"rowCount":1,"truncated":false,"elapsedMs":3}
```

Statement lain:

```json
{"type":"exec","rowsAffected":1,"elapsedMs":2}
```

Batas: hanya superuser, satu statement, timeout 20 detik, 1000 baris, query
100 KB. API token ditolak.

## Settings

```http
GET /api/admin/settings
PUT /api/admin/settings
```

`GET` mengembalikan `{"app":{...},"s3":{...},"backend":"local|s3"}`. Secret
dimasking `••••••`. `PUT` menerima objek flat, contoh:

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

## Log

```http
GET /api/admin/logs?page=1&perPage=30&level=info&db=<nama>
```

Mengembalikan envelope `items`. Tiap item: `created`, `level`, `message`,
`data`, `actor`, `db`, `collection`.

## Backup

```http
GET    /api/admin/backups
POST   /api/admin/backups
GET    /api/admin/backups/{name}
DELETE /api/admin/backups/{name}
POST   /api/admin/backups/{name}/restore
```

List mengembalikan `{"items":[{name,size,modified,source}],"s3":true|false}`,
`source` = `local`, `s3`, atau `local+s3`.

Restore menaruh arsip di `data/restore-pending` dan mengembalikan `202` dengan
`restartRequired: true`. Snapshot diterapkan saat start berikutnya.

## API token

```http
GET    /api/admin/tokens
POST   /api/admin/tokens        {"name":"ci","dbId":"<DB_ID>"}
DELETE /api/admin/tokens/{id}
```

Create mengembalikan token mentah **sekali**:

```json
{"id":"...","name":"ci","dbId":"...","dbName":"my_db","prefix":"mdb_abcd1234","created":"...","token":"mdb_..."}
```

Pakai sebagai `Authorization: mdb_...` atau `X-API-Key: mdb_...`. Cakupan:
record, file, export/import untuk database tersebut. Revoke langsung berlaku.

## Superuser

```http
GET    /api/admin/superusers
POST   /api/admin/superusers          {"email":"...","password":"..."}
PATCH  /api/admin/superusers/{id}     {"email":"...","password":"..."}
DELETE /api/admin/superusers/{id}
```

Minimal satu superuser harus tersisa.
