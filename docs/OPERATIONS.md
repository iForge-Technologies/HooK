# Operations

[← Back to README](../README.md) · [Documentation index](DOCUMENTATION.md)

- [Health check](#health-check)
- [Dead-man switch](#dead-man-switch)
- [Backups and restore](#backups-and-restore)
- [Recovery](#recovery)

## Health check

`GET /healthz` returns 200 when the hub is healthy and 503 when it isn't, for load balancers and external monitors. It checks three things:

- The database accepts writes.
- The check scheduler has run in the last 10 minutes.
- The data disk has more than 200 MB free and is under 98% full.

**Settings → Hub health** shows the same checks in the dashboard.

## Dead-man switch

A monitor can't report its own death, so the hub checks in with an outside service instead:

1. Create a heartbeat check at Healthchecks.io, Better Stack or Cronitor.
2. Set `HOOK_DEADMAN_URL` to its ping URL.

The hub pings it every minute while healthy. If the hub dies, the pings stop and that service alerts you. If you also set `HOOK_DEADMAN_FAIL_URL`, a hub that is running but unhealthy pings that URL instead.

## Backups and restore

The database is `hook.db` in `HOOK_DATA`. Every stored secret is encrypted with `secret.key` from the same folder, or with `HOOK_SECRET` if you set it. That covers channel tokens, connection strings, 2FA secrets and the Web Push key.

> **A backup without the key can't decrypt any secret.** Keep a copy of the key somewhere other than the backup bucket.

### Continuous replication (Litestream)

1. Install [Litestream](https://litestream.io).
2. Copy [deploy/litestream.yml](../deploy/litestream.yml) to `/etc/litestream.yml` and set your bucket.
3. Put the credentials in `/etc/hook/litestream.env` (see [deploy/litestream.env.example](../deploy/litestream.env.example)).
4. Install the drop-in [deploy/hook-hub.service.d/litestream.conf](../deploy/hook-hub.service.d/litestream.conf). systemd then starts the hub through `litestream replicate -exec`, so replication runs exactly while the hub does.

Every change reaches S3 or R2 within about 10 seconds.

### Nightly snapshot

Add [deploy/backup-cron.example](../deploy/backup-cron.example) to cron. It runs the command below and keeps 14 days of copies:

```bash
hook-hub backup /var/backups/hook
```

It writes `hook-<timestamp>.db` and `secret-<timestamp>.key`, both owner-only (mode 0600), and never overwrites an existing file. It's safe to run while the hub is up.

### Restore

1. Stop the hub.
2. Put the database back. Either copy a snapshot to `/var/lib/hook/hook.db`, or restore from Litestream:

   ```bash
   litestream restore -config /etc/litestream.yml /var/lib/hook/hook.db
   ```

3. Put the matching key at `/var/lib/hook/secret.key` (or set `HOOK_SECRET`).
4. Start the hub.

## Recovery

Locked out of two-factor sign-in? Run either command on the server:

```bash
hook-hub reset-2fa <username>
```

```bash
hook-hub set-password <username>
```
