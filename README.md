# connection_recorder

`connection_recorder` records local TCP/UDP network connections with the owning process.
It scans network namespaces, so ordinary Docker/Podman bridge-network containers
are included when `networkmond` runs as root.

- `networkmond`: daemon, designed to run under systemd.
- `networkmonc`: client for status, queries, and runtime config.

Defaults:

- Poll interval: `500ms`
- Retention: `24h`
- Database: `/tmp/networkmon/networkmon.db`
- Socket: `/run/networkmon/networkmond.sock`

The database stores one row per unique connection fingerprint and updates `last_seen`
and `seen_count` on later polls. This keeps the default 24 hour database size under
about 100 MB on ordinary hosts. Very high churn hosts can move the database to
`/opt/networkmon/networkmon.db` with `NETWORKMON_DB` or `-db`.

## Build

```sh
go build -o bin/networkmond ./cmd/networkmond
go build -o bin/networkmonc ./cmd/networkmonc
```

## Run Manually

```sh
sudo ./bin/networkmond
./bin/networkmonc status
./bin/networkmonc list --since 1h
./bin/networkmonc list --remote 1.2.3.4 --json
./bin/networkmonc list --container abc123
./bin/networkmonc config set --interval 500ms --retention 24h
./bin/networkmonc config set --db /opt/networkmon/networkmon.db
```

## Install As systemd Service

```sh
sudo install -m 0755 bin/networkmond /usr/local/bin/networkmond
sudo install -m 0755 bin/networkmonc /usr/local/bin/networkmonc
sudo install -m 0644 packaging/systemd/networkmond.service /etc/systemd/system/networkmond.service
sudo systemctl daemon-reload
sudo systemctl enable --now networkmond
```

Query it:

```sh
networkmonc status
networkmonc list --limit 50
```
