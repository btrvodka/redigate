# Configuration

redigate is configured with environment variables.

## Redis connection

| Variable | Default | Description |
|---|---|---|
| `REDIS_URL` | | Connection URL, overrides the variables below, see [URL format](#redis-url). |
| `REDIS_MODE` | `auto` | `auto`, `standalone`, `cluster` or `sentinel`. `auto` detects the [topology](topologies.md) with `INFO`. |
| `REDIS_ADDRS` | `127.0.0.1:6379` | Comma-separated addresses: the server, cluster seed nodes or sentinels. |
| `REDIS_USERNAME`, `REDIS_PASSWORD` | | Credentials of data nodes. |
| `REDIS_DB` | `0` | Default database (standalone and sentinel). |
| `REDIS_SENTINEL_MASTER` | | Master name. Detected automatically when sentinels monitor a single master. |
| `REDIS_SENTINEL_USERNAME`, `REDIS_SENTINEL_PASSWORD` | | Credentials of sentinels. |
| `REDIS_READ_FROM_REPLICAS` | `false` | Route read-only commands to replicas (cluster and sentinel). |
| `REDIS_PROTOCOL` | `3` | RESP version, `2` or `3`. |
| `REDIS_DIAL_TIMEOUT` | `5s` | Connection timeout. |
| `REDIS_READ_TIMEOUT`, `REDIS_WRITE_TIMEOUT` | `10s` | Socket timeouts. |
| `REDIS_POOL_SIZE` | `0` | Connections per node, `0` means 10 per CPU. |
| `REDIS_MAX_RETRIES` | `3` | Retries of failed commands. |
| `REDIS_MAX_REDIRECTS` | `8` | Cluster `MOVED`/`ASK` redirects to follow. |
| `REDIS_TLS_ENABLED` | `false` | Use TLS. |
| `REDIS_TLS_CA_FILE`, `REDIS_TLS_CERT_FILE`, `REDIS_TLS_KEY_FILE` | | CA bundle and client certificate. |
| `REDIS_TLS_SERVER_NAME` | | Server name to verify. |
| `REDIS_TLS_INSECURE_SKIP_VERIFY` | `false` | Skip certificate verification. |

### Redis URL

```
redis://[user[:password]@]host:port[,host:port...][/db][?options]
rediss://...                                        TLS
redis+sentinel://[user[:password]@]host:port[,host:port...]/master[/db][?options]
rediss+sentinel://...                               TLS
```

Options: `mode`, `addr` (repeatable), `db`, `master`, `sentinel_username`, `sentinel_password`,
`protocol`, `dial_timeout`, `read_timeout`, `write_timeout`, `pool_size`, `max_retries`,
`max_redirects`, `read_from_replicas`, `tls_server_name`, `tls_insecure_skip_verify`,
`tls_ca_file`, `tls_cert_file`, `tls_key_file`.

```
redis://:secret@localhost:6379/2
rediss://node1:7000,node2:7001?mode=cluster
redis+sentinel://s1:26379,s2:26379/mymaster?sentinel_password=x
```

## HTTP server and security

| Variable | Default | Description |
|---|---|---|
| `HTTP_ADDR` | `127.0.0.1:8080` | API address. The container image uses `:8080`. |
| `API_TOKEN` | | Bearer token with full access. Authentication is disabled when no token is set. |
| `API_READONLY_TOKEN` | | Bearer token with [read-only access](api.md#read-only-access). |
| `READ_ONLY` | `false` | Read-only access for every request. |
| `ALLOW_INSECURE` | `false` | Allow listening on a non-loopback address without a token. |
| `UI_ENABLED` | `true` | Serve the [web UI](web-ui.md) at `/ui/` and Swagger UI at `/api/v1/docs/`. |
| `HTTP_READ_HEADER_TIMEOUT` | `10s` | |
| `HTTP_IDLE_TIMEOUT` | `120s` | |
| `HTTP_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown timeout. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `LOG_FORMAT` | `text` | `text` or `json`. |

redigate refuses to start on a non-loopback address without a token unless `ALLOW_INSECURE=true`:
without authentication anyone who reaches the port has full access to Redis.

## Limits

| Variable | Default | Description |
|---|---|---|
| `REQUEST_TIMEOUT` | `30s` | Timeout of regular requests. |
| `MAX_BLOCK_TIMEOUT` | `60s` | The maximum `?timeout=` of a request, for blocking commands. |
| `MAX_RESPONSE_ITEMS` | `10000` | Collection elements per response, larger replies are truncated. |
| `MAX_PIPELINE_COMMANDS` | `10000` | Commands per pipeline and keys per multi-key request. |
| `MAX_BODY_BYTES` | `33554432` | Request body size. |
| `MAX_IMPORT_BYTES` | `1073741824` | Body size of `POST /api/v1/keys/import`. |
| `MAX_STREAMS` | `64` | Concurrent streams, exports and imports. |
| `MAX_STREAM_DURATION` | `1h` | The maximum duration of a stream, export or import. |

## Metrics

| Variable | Default | Description |
|---|---|---|
| `METRICS_ADDR` | `:9090` | Prometheus metrics address, empty disables the metrics server. |
| `PPROF_ENABLED` | `false` | Serve `/debug/pprof/` on the metrics server. |

The metrics server serves `/metrics`:

- `redigate_http_requests_total{route, code}`, `redigate_http_request_duration_seconds{route}`;
- `redigate_redis_commands_total{command, status}`, `redigate_redis_command_duration_seconds{command}`;
- Go runtime and process metrics.
