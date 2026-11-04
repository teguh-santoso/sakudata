# Deployment

SakuData is a single static Go binary. It can run behind Caddy/Nginx on a VPS or
inside Docker.

## Option A — systemd + Caddy (recommended)

On the server:

```bash
useradd --system --home /var/lib/sakudata --shell /usr/sbin/nologin sakudata
mkdir -p /opt/sakudata /var/lib/sakudata
chown -R sakudata:sakudata /var/lib/sakudata

# copy the binary and unit
scp sakudata-linux-amd64 root@HOST:/opt/sakudata/sakudata
scp deploy/sakudata.service root@HOST:/etc/systemd/system/sakudata.service
ssh root@HOST 'chmod +x /opt/sakudata/sakudata && systemctl daemon-reload && systemctl enable --now sakudata'

# reverse proxy
scp deploy/Caddyfile root@HOST:/etc/caddy/Caddyfile
ssh root@HOST 'caddy validate --config /etc/caddy/Caddyfile && systemctl reload caddy'
```

`deploy/deploy.sh` automates build + upload + restart. It reads `SAKUDATA_HOST`,
`SAKUDATA_USER`, `SAKUDATA_PATH`, and optionally `SAKUDATA_SSH_KEY` and
`SAKUDATA_SERVICE` from the environment. Credentials are never stored in the
repository.

```bash
SAKUDATA_HOST=203.0.113.10 SAKUDATA_USER=root ./deploy/deploy.sh
```

## Option B — Docker

```bash
docker compose up -d --build
```

The compose file binds to `127.0.0.1:8090`; put Caddy/Nginx in front for TLS.

## Checklist

1. Rotate any password that was ever written into a local credentials file.
2. Visit `https://<domain>/_/` and create the first superuser.
3. Create a database, a collection, and verify records + filters.
4. Configure S3 under **Settings → File storage** if you want offsite backups.
5. Enable automatic backups under **Backups**.
6. Confirm `/api/health` returns `{"code":200,...}`.
