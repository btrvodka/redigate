# redigate

An HTTP API for Redis and Valkey: standalone servers, clusters and sentinel deployments.

redigate is built for development and test environments. It exposes **everything** a Redis
server can do over HTTP, including dangerous operations such as `FLUSHALL`, `CONFIG SET`,
`CLUSTER FAILOVER`, `SHUTDOWN` and `DEBUG`. Do not expose it to untrusted networks and do
not point it at production data.

```sh
curl -d 'SET greeting "hello world"' localhost:8080/api/v1/command
curl 'localhost:8080/api/v1/keys?match=user:*&meta'
curl -N 'localhost:8080/api/v1/pubsub/subscribe?channel=news'
```

## Features

- **Any command** through `POST /api/v1/command` and `POST /api/v1/pipeline`, routed by key,
  sent to a given node or fanned out to masters, replicas, all nodes or sentinels.
- **All topologies** detected automatically: standalone (with replicas), cluster and sentinel.
  Cluster-aware SCAN, multi-key operations across slots, slot-checked transactions.
- **Typed endpoints** for keys, strings, hashes, lists, sets, sorted sets, streams, bitmaps,
  HyperLogLog, geo, server and cluster administration, scripts, functions and ACL.
- **Modules**: JSON, Search, TimeSeries, Bloom, Cuckoo, Count-Min Sketch, Top-K, t-digest and
  vector sets (built into Redis 8). Endpoints answer `501` when the server lacks a module.
- **Streaming** over server-sent events: Pub/Sub (including sharded channels), `MONITOR`
  and stream tailing.
- **Export and import** of keys as NDJSON, exact (`DUMP`/`RESTORE`) or portable between
  Redis and Valkey versions.
- **Binary safe**: keys and values that are not valid UTF-8 travel as base64.
- **Bearer token authentication** with an optional read-only token.
- **A small web UI** (htmx) with an overview of nodes, a key browser and a command console,
  and **Swagger UI** for the whole API.
- RESP2 and RESP3, TLS, Prometheus metrics, a single static binary and a 40 MB Alpine-based container image.

Tested against Redis 7.4, Redis 8 and Valkey 8 in every topology.

## Quick start

### Docker Compose

The repository ships a compose file with a Redis cluster (3 masters, 3 replicas) and redigate
connected to it:

```sh
make up      # redigate on 127.0.0.1:8080
curl localhost:8080/api/v1/topology?pretty
open http://localhost:8080/ui/            # web UI
open http://localhost:8080/api/v1/docs/   # Swagger UI
make down
```

Use `REDIS_IMAGE=valkey/valkey:8 make up` to run Valkey instead of Redis.

### Docker

```sh
docker build -t redigate .
docker run --rm -p 127.0.0.1:8080:8080 \
  -e REDIS_URL=redis://host.docker.internal:6379 \
  -e API_TOKEN=secret \
  redigate
```

### From source

Requires Go 1.27+.

```sh
go install github.com/btrvodka/redigate/cmd/redigate@latest
REDIS_URL=redis://localhost:6379 redigate
```

## Configuration

redigate is configured with environment variables.

### Redis connection

| Variable | Default | Description |
|---|---|---|
| `REDIS_URL` | | Connection URL, overrides the variables below, see [URL format](#redis-url). |
| `REDIS_MODE` | `auto` | `auto`, `standalone`, `cluster` or `sentinel`. `auto` detects the topology with `INFO`. |
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

#### Redis URL

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

### HTTP server and security

| Variable | Default | Description |
|---|---|---|
| `HTTP_ADDR` | `127.0.0.1:8080` | API address. The container image uses `:8080`. |
| `API_TOKEN` | | Bearer token with full access. Authentication is disabled when no token is set. |
| `API_READONLY_TOKEN` | | Bearer token with [read-only access](#read-only-access). |
| `READ_ONLY` | `false` | Read-only access for every request. |
| `ALLOW_INSECURE` | `false` | Allow listening on a non-loopback address without a token. |
| `HTTP_READ_HEADER_TIMEOUT` | `10s` | |
| `HTTP_IDLE_TIMEOUT` | `120s` | |
| `HTTP_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown timeout. |
| `METRICS_ADDR` | `:9090` | Prometheus metrics address, empty disables the metrics server. |
| `PPROF_ENABLED` | `false` | Serve `/debug/pprof/` on the metrics server. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `LOG_FORMAT` | `text` | `text` or `json`. |

redigate refuses to start on a non-loopback address without a token unless `ALLOW_INSECURE=true`.

| Variable | Default | Description |
|---|---|---|
| `UI_ENABLED` | `true` | Serve the [web UI](#web-ui) at `/ui/` and Swagger UI at `/api/v1/docs/` on the API address. |

### Limits

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

## Using the API

### Responses

Every endpoint except streams and exports returns a JSON envelope:

```json
{"error_code": "OK", "error_description": "", "request_id": "6f1c...", "result": {"value": "hello"}}
```

Add `?pretty` to any request for indented JSON. The request id is taken from `X-Request-Id`
or generated, and is returned in the same header.

| HTTP | `error_code` | Meaning |
|---|---|---|
| 400 | `BAD_REQUEST` | Invalid parameters. |
| 400 | `REDIS_ERROR`, `WRONG_TYPE` | The server rejected the command. |
| 400 | `CROSS_SLOT` | Keys of a transaction or session hash to different cluster slots. |
| 400 | `UNSUPPORTED_COMMAND` | `SUBSCRIBE`, `MONITOR` and `SYNC` belong to [streaming endpoints](#streaming). |
| 401 | `UNAUTHORIZED` | Missing or invalid token. |
| 403 | `FORBIDDEN` | The command needs full access. |
| 403 | `REDIS_NO_PERMISSION` | The Redis user lacks an ACL permission. |
| 404 | `NOT_FOUND` | Missing key or unknown node. |
| 404 | `ROUTE_NOT_FOUND` | Unknown endpoint. |
| 409 | `REDIS_READONLY`, `REDIS_BUSY` | Write to a replica, a busy script. |
| 413 | `PAYLOAD_TOO_LARGE` | The body exceeds the limit. |
| 421 | `REDIS_REDIRECT` | A cluster node answered `MOVED`/`ASK` to a request sent to a specific node. |
| 429 | `TOO_MANY_STREAMS` | `MAX_STREAMS` is reached. |
| 501 | `NOT_SUPPORTED` | The topology or the server does not support the operation. |
| 502 | `REDIS_UNAVAILABLE`, `REDIS_AUTH_FAILED` | Connection or authentication problems. |
| 503 | `REDIS_UNAVAILABLE` | The server is loading, the cluster is down. |
| 504 | `TIMEOUT` | The request timed out. |

A missing value (nil reply) is a successful `null`, except for typed key endpoints such as
`GET /api/v1/keys/value`, which return `404`.

### Binary data

Replies use the `encoding` parameter:

- `auto` (default): valid UTF-8 strings are JSON strings, other strings are `{"base64": "..."}`;
- `utf8`: always strings, invalid bytes are replaced with U+FFFD;
- `base64`: every string is base64-encoded.

Request bodies accept a string, a number or `{"base64": "..."}` wherever a key or a value is
expected. Keys, fields and members in the query string are base64 with `key_encoding=base64`.

### Common parameters

| Parameter | Description |
|---|---|
| `db` | Database (standalone and sentinel). |
| `node` | Send the request to the node with this address, e.g. `10.0.0.5:6379`. |
| `target` | `auto` (default, by key), `node`, `masters`, `replicas`, `all` (data nodes) or `sentinels`. Fan-out targets return a result per node and `"ok": false` if any node failed. |
| `encoding` | Reply encoding, see above. |
| `timeout` | Request timeout for blocking commands, up to `MAX_BLOCK_TIMEOUT`. |
| `duration` | Duration of streams, exports and imports, up to `MAX_STREAM_DURATION`. |

### Commands

`POST /api/v1/command` accepts a redis-cli style command line or JSON:

```sh
curl -d 'SET "my key" "a value" EX 60' localhost:8080/api/v1/command
curl -H 'Content-Type: application/json' \
  -d '{"args": ["SET", "bin", {"base64": "/wA="}]}' localhost:8080/api/v1/command
curl -d 'INFO memory' 'localhost:8080/api/v1/command?target=masters'
curl -d 'CONFIG SET maxmemory 100mb' 'localhost:8080/api/v1/command?target=all'
```

Commands that change the state of a connection (`SELECT`, `AUTH`, `HELLO`, `RESET`,
`CLIENT REPLY`, `CLIENT SETNAME`, `MULTI`, `WATCH`, `READONLY`, ...) run on a dedicated
connection, so they never leak into the connection pool.

`POST /api/v1/pipeline` sends several commands in one round trip and returns a result or an
error per command:

```sh
curl --data-binary @- localhost:8080/api/v1/pipeline <<'EOF'
SET counter 1
INCR counter
GET counter
EOF

# MULTI/EXEC
curl -d $'SET {order}:1 new\nINCR {order}:count' 'localhost:8080/api/v1/pipeline?atomic'

# A session: one dedicated connection, connection-scoped commands are allowed
curl -d $'WATCH balance\nMULTI\nINCRBY balance 10\nEXEC' 'localhost:8080/api/v1/pipeline?session'
```

JSON form: `{"commands": [["SET", "a", "1"], "GET a"], "atomic": false, "session": false}`.

In a cluster plain pipelines are split by slot. Atomic pipelines and sessions run on the master
that owns the slot of their keys and fail with `CROSS_SLOT` when keys hash to different slots.

### Keys and data types

```sh
# Page through keys of every master: repeat with the returned cursor until it is "0"
curl 'localhost:8080/api/v1/keys?match=user:*&count=100&meta'

# The value of any type with metadata
curl 'localhost:8080/api/v1/keys/value?key=user:1'

# Delete keys by pattern on every master
curl -d '{"match": "tmp:*", "dry_run": true}' localhost:8080/api/v1/keys/delete

curl -X PUT -d '{"key": "s", "value": "v", "ttl_ms": 60000}' localhost:8080/api/v1/strings
curl -X PUT -d '{"key": "h", "fields": [{"field": "name", "value": "Ann"}]}' localhost:8080/api/v1/hashes
curl -d '{"key": "z", "members": [{"member": "a", "score": 1.5}]}' localhost:8080/api/v1/zsets/add
curl 'localhost:8080/api/v1/zsets?key=z&by=score&start=0&stop=10'
```

Multi-key endpoints that take `?key=a&key=b` (`exists`, `DELETE /keys`, `strings/mget`,
`json/mget`, ...) work across cluster slots.

### Streaming

Streams are [server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html).
Every event has a type and JSON data; the last event is `end` with the reason (`duration`,
`canceled` or `closed`). Heartbeats are sent as `: ping` comments.

```sh
curl -N 'localhost:8080/api/v1/pubsub/subscribe?channel=news&pattern=events.*&shard_channel=orders'
curl -N 'localhost:8080/api/v1/monitor?duration=30s'
curl -N 'localhost:8080/api/v1/streams/tail?key=orders&group=billing&consumer=c1'
```

```
event: message
data: {"channel":"news","payload":"hello"}
```

### Export and import

```sh
# Exact copy between compatible servers
curl 'localhost:8080/api/v1/keys/export?match=user:*' > users.ndjson
curl --data-binary @users.ndjson 'localhost:8080/api/v1/keys/import?replace'

# Portable copy, e.g. from Redis 8 to Valkey
curl 'host-a:8080/api/v1/keys/export?format=value' \
  | curl --data-binary @- 'host-b:8080/api/v1/keys/import'
```

The `dump` format (default) keeps every type, including module types, but `RESTORE` accepts
dumps only from servers with a compatible RDB version: a Redis 8 dump can't be restored on
Valkey 8 or Redis 7.4. The `value` format supports strings, lists, sets, hashes, sorted sets,
streams (without consumer groups) and JSON, and works between any versions. TTLs are kept.

### Web UI

A small interface at `http://<HTTP_ADDR>/ui/` (`/` redirects to it) is built with server-rendered
HTML and [htmx](https://htmx.org), embedded into the binary: no build step and no CDN.

- **Overview**: topology and every node with its role, latency, version, memory, clients and keys.
- **Keys**: SCAN with a pattern and a type filter loaded page by page while scrolling, the value of
  any type, TTL changes and deletion. Binary keys and values are shown as base64.
- **Console**: redis-cli style command line sent by key, to all masters, replicas or nodes, or to a
  given node; string replies are shown as they are, other replies as JSON.

The UI uses the same service layer and access rules as the API. When a token is configured the
UI asks for it once and keeps it in an HttpOnly `SameSite=Strict` cookie; with the read-only token
destructive actions are hidden and denied. Requests changing data are accepted only from htmx
(`HX-Request` header). Set `UI_ENABLED=false` to serve the HTTP API only.

### Topologies

- **Standalone**: the server and its replicas from `INFO replication` are nodes.
- **Cluster**: SCAN, `DBSIZE`, `FLUSHALL`, `SCRIPT LOAD`, `FUNCTION LOAD` and `ACL SETUSER`
  go to every relevant node; the cluster supports only db 0.
- **Sentinel**: commands follow the current master; sentinels are nodes with the `sentinel` role
  and accept `SENTINEL ...` commands with `?node=`.

Redis 8 coordinates Search and TimeSeries queries across the cluster, so these endpoints use a
single node. For clusters without a coordinator pass `?target=masters` for per-shard results.

### Read-only access

Requests with `API_READONLY_TOKEN` (or with `READ_ONLY=true`) may run only commands that do not
modify data or server state. The decision uses the command table of the server (`COMMAND`):
commands with the `write` or `may_replicate` flag or the `@write` ACL category are denied,
`admin` commands are denied except reading ones (`CONFIG GET`, `CLIENT LIST`, `SLOWLOG GET`,
`CLUSTER NODES`, `ACL GETUSER`, ...), as well as `PUBLISH`, `EVAL`, `FCALL`, `SCRIPT LOAD`
and `MONITOR`. Unknown commands are denied.

## Endpoint reference

All endpoints are under `/api/v1`, take the [common parameters](#common-parameters) where they
make sense and are described in more detail in the code (`internal/httpapi/*_routes.go`).

### General

| Endpoint | Description |
|---|---|
| `GET /api/v1/ping` | Ping every node with latency. |
| `GET /api/v1/topology` | Detected topology and nodes. |
| `POST /api/v1/command` | Run any command. |
| `POST /api/v1/pipeline` | Run commands in one round trip, `?atomic`, `?session`. |

`GET /healthz` (liveness) and `GET /readyz` (redis is reachable) need no token.

| Endpoint | Description |
|---|---|
| `GET /api/v1/openapi.json` | OpenAPI 2.0 specification, no token required. |
| `GET /api/v1/openapi.yaml` | The same in YAML. |

### Keys

| Endpoint | Description |
|---|---|
| `GET /api/v1/keys` | SCAN: `match`, `type`, `count`, `cursor`, `meta`. |
| `GET /api/v1/keys/value` | Value of any type with type, TTL, length, encoding and memory. |
| `GET /api/v1/keys/meta` | Metadata only. |
| `GET /api/v1/keys/type` | TYPE. |
| `GET /api/v1/keys/exists` | EXISTS per key. |
| `DELETE /api/v1/keys` | DEL or UNLINK (`?unlink`) per key. |
| `POST /api/v1/keys/delete` | Delete by pattern: `{"match", "type", "dry_run", "cursor"}`. |
| `POST /api/v1/keys/touch` | TOUCH per key. |
| `GET /api/v1/keys/random` | RANDOMKEY. |
| `GET /api/v1/keys/ttl` | PTTL. |
| `PUT /api/v1/keys/ttl` | `{"key", "ttl_ms" \| "expire_at_ms", "condition"}`. |
| `DELETE /api/v1/keys/ttl` | PERSIST. |
| `POST /api/v1/keys/rename` | `{"key", "new_key", "nx"}`. |
| `POST /api/v1/keys/copy` | `{"key", "destination", "destination_db", "replace"}`. |
| `GET /api/v1/keys/dump` | DUMP, base64. |
| `POST /api/v1/keys/restore` | `{"key", "dump", "ttl_ms", "replace", "absttl", "idle_seconds", "frequency"}`. |
| `GET /api/v1/keys/export` | NDJSON export: `match`, `type`, `format=dump\|value`. |
| `POST /api/v1/keys/import` | NDJSON import: `replace`. |

### Strings

| Endpoint | Description |
|---|---|
| `GET /api/v1/strings` | GET. |
| `PUT /api/v1/strings` | SET: `{"key", "value", "ttl_ms", "expire_at_ms", "nx", "xx", "keepttl", "get"}`. |
| `GET /api/v1/strings/mget` | GET per key. |
| `POST /api/v1/strings/mset` | `{"items": [{"key", "value"}], "nx"}`. |
| `POST /api/v1/strings/incr` | INCRBY or INCRBYFLOAT: `{"key", "by"}`. |
| `POST /api/v1/strings/append` | APPEND. |
| `GET /api/v1/strings/len` | STRLEN. |
| `GET /api/v1/strings/range` | GETRANGE: `start`, `end`. |
| `PUT /api/v1/strings/range` | SETRANGE: `{"key", "offset", "value"}`. |
| `POST /api/v1/strings/getex` | GETEX or GETDEL: `{"key", "ttl_ms", "persist", "delete"}`. |

### Hashes

| Endpoint | Description |
|---|---|
| `GET /api/v1/hashes` | HGETALL. |
| `PUT /api/v1/hashes` | HSET or HSETNX: `{"key", "fields": [{"field", "value"}], "nx"}`. |
| `GET /api/v1/hashes/scan` | HSCAN: `cursor`, `match`, `count`. |
| `GET /api/v1/hashes/fields` | HMGET: `field` (repeatable). |
| `DELETE /api/v1/hashes/fields` | HDEL. |
| `POST /api/v1/hashes/incr` | HINCRBY or HINCRBYFLOAT. |
| `GET /api/v1/hashes/len` | HLEN. |
| `GET /api/v1/hashes/exists` | HEXISTS. |
| `GET /api/v1/hashes/ttl` | HPTTL (Redis 7.4+). |
| `PUT /api/v1/hashes/ttl` | HPEXPIRE: `{"key", "fields", "ttl_ms", "condition"}`. |

### Lists

| Endpoint | Description |
|---|---|
| `GET /api/v1/lists` | LRANGE: `start`, `stop`. |
| `GET /api/v1/lists/len` | LLEN. |
| `GET /api/v1/lists/index` | LINDEX. |
| `PUT /api/v1/lists/index` | LSET. |
| `GET /api/v1/lists/pos` | LPOS: `element`, `rank`, `count`, `maxlen`. |
| `POST /api/v1/lists/push` | `{"key", "values", "side": "left\|right", "only_existing"}`. |
| `POST /api/v1/lists/pop` | `{"key", "side", "count", "block_ms"}`. |
| `POST /api/v1/lists/remove` | LREM: `{"key", "value", "count"}`. |
| `POST /api/v1/lists/trim` | LTRIM. |
| `POST /api/v1/lists/insert` | LINSERT: `{"key", "pivot", "value", "position"}`. |
| `POST /api/v1/lists/move` | LMOVE or BLMOVE. |

### Sets

| Endpoint | Description |
|---|---|
| `GET /api/v1/sets` | SMEMBERS. |
| `GET /api/v1/sets/scan` | SSCAN. |
| `GET /api/v1/sets/len` | SCARD. |
| `GET /api/v1/sets/contains` | SMISMEMBER: `member` (repeatable). |
| `GET /api/v1/sets/random` | SRANDMEMBER. |
| `POST /api/v1/sets/add` | SADD. |
| `POST /api/v1/sets/remove` | SREM. |
| `POST /api/v1/sets/pop` | SPOP. |
| `POST /api/v1/sets/move` | SMOVE. |
| `POST /api/v1/sets/inter` | SINTER or SINTERSTORE with `destination`. |
| `POST /api/v1/sets/union` | SUNION or SUNIONSTORE. |
| `POST /api/v1/sets/diff` | SDIFF or SDIFFSTORE. |

### Sorted sets

| Endpoint | Description |
|---|---|
| `GET /api/v1/zsets` | ZRANGE: `start`, `stop`, `by=rank\|score\|lex`, `rev`, `offset`, `count`, `with_scores`. |
| `GET /api/v1/zsets/scan` | ZSCAN. |
| `GET /api/v1/zsets/len` | ZCARD or ZCOUNT with `min`/`max`. |
| `GET /api/v1/zsets/score` | ZMSCORE. |
| `GET /api/v1/zsets/rank` | ZRANK or ZREVRANK. |
| `POST /api/v1/zsets/add` | `{"key", "members": [{"member", "score"}], "nx", "xx", "gt", "lt", "ch"}`. |
| `POST /api/v1/zsets/remove` | ZREM. |
| `POST /api/v1/zsets/incr` | ZINCRBY. |
| `POST /api/v1/zsets/pop` | ZPOPMIN/ZPOPMAX or blocking variants. |
| `POST /api/v1/zsets/remove-range` | ZREMRANGEBY{RANK,SCORE,LEX}. |
| `POST /api/v1/zsets/inter` | ZINTER or ZINTERSTORE with weights and aggregate. |
| `POST /api/v1/zsets/union` | ZUNION or ZUNIONSTORE. |
| `POST /api/v1/zsets/diff` | ZDIFF or ZDIFFSTORE. |

### Streams

| Endpoint | Description |
|---|---|
| `GET /api/v1/streams` | XRANGE or XREVRANGE: `start`, `end`, `count`, `rev`. |
| `GET /api/v1/streams/len` | XLEN. |
| `GET /api/v1/streams/info` | XINFO STREAM, `full`. |
| `POST /api/v1/streams/add` | XADD: `{"key", "id", "fields", "maxlen", "minid", "approx", "limit", "nomkstream"}`. |
| `POST /api/v1/streams/delete` | XDEL. |
| `POST /api/v1/streams/trim` | XTRIM. |
| `POST /api/v1/streams/read` | XREAD: `{"keys", "ids", "count", "block_ms"}`. |
| `POST /api/v1/streams/read-group` | XREADGROUP. |
| `GET /api/v1/streams/tail` | SSE: new entries, optionally through a group. |
| `GET /api/v1/streams/groups` | XINFO GROUPS. |
| `POST /api/v1/streams/groups` | XGROUP CREATE. |
| `PUT /api/v1/streams/groups` | XGROUP SETID. |
| `DELETE /api/v1/streams/groups` | XGROUP DESTROY. |
| `GET /api/v1/streams/consumers` | XINFO CONSUMERS. |
| `POST /api/v1/streams/consumers` | XGROUP CREATECONSUMER. |
| `DELETE /api/v1/streams/consumers` | XGROUP DELCONSUMER. |
| `POST /api/v1/streams/ack` | XACK. |
| `GET /api/v1/streams/pending` | XPENDING summary or entries. |
| `POST /api/v1/streams/claim` | XCLAIM. |
| `POST /api/v1/streams/autoclaim` | XAUTOCLAIM. |

### Bitmaps, HyperLogLog, geo

| Endpoint | Description |
|---|---|
| `GET /api/v1/bitmaps/bit` | GETBIT. |
| `PUT /api/v1/bitmaps/bit` | SETBIT. |
| `GET /api/v1/bitmaps/count` | BITCOUNT. |
| `GET /api/v1/bitmaps/pos` | BITPOS. |
| `POST /api/v1/bitmaps/op` | BITOP. |
| `POST /api/v1/hll/add` | PFADD. |
| `GET /api/v1/hll/count` | PFCOUNT. |
| `POST /api/v1/hll/merge` | PFMERGE. |
| `POST /api/v1/geo/add` | GEOADD. |
| `GET /api/v1/geo/pos` | GEOPOS. |
| `GET /api/v1/geo/hash` | GEOHASH. |
| `GET /api/v1/geo/dist` | GEODIST. |
| `POST /api/v1/geo/search` | GEOSEARCH or GEOSEARCHSTORE. |

### Pub/Sub and monitoring

| Endpoint | Description |
|---|---|
| `GET /api/v1/pubsub/subscribe` | SSE: `channel`, `pattern`, `shard_channel` (repeatable). |
| `POST /api/v1/pubsub/publish` | PUBLISH or SPUBLISH: `{"channel", "message", "sharded"}`. |
| `GET /api/v1/pubsub/channels` | Active channels of all nodes, `pattern`, `sharded`. |
| `GET /api/v1/pubsub/numsub` | Subscribers per channel summed over nodes. |
| `GET /api/v1/pubsub/numpat` | Pattern subscriptions summed over nodes. |
| `GET /api/v1/monitor` | SSE: MONITOR of the selected nodes (masters by default). |

### Server

Per-node endpoints use every data node by default (masters for `dbsize`, flushes, persistence
and pauses) and accept `node` or `target`.

| Endpoint | Description |
|---|---|
| `GET /api/v1/server/info` | INFO parsed into sections: `section` (comma-separated). |
| `GET /api/v1/server/dbsize` | DBSIZE per node and the total over masters. |
| `GET /api/v1/server/role` | ROLE. |
| `GET /api/v1/server/config` | CONFIG GET: `pattern` (comma-separated). |
| `PUT /api/v1/server/config` | CONFIG SET: `{"params": {"maxmemory": "100mb"}}`. |
| `POST /api/v1/server/config/rewrite` | CONFIG REWRITE. |
| `POST /api/v1/server/config/resetstat` | CONFIG RESETSTAT. |
| `GET /api/v1/server/slowlog` | SLOWLOG GET, parsed. |
| `DELETE /api/v1/server/slowlog` | SLOWLOG RESET. |
| `GET /api/v1/server/clients` | CLIENT LIST, parsed: `type`. |
| `POST /api/v1/server/clients/kill` | CLIENT KILL: `{"filters": {"id": "42"}}`. |
| `POST /api/v1/server/clients/pause` | CLIENT PAUSE: `{"timeout_ms", "mode"}`. |
| `POST /api/v1/server/clients/unpause` | CLIENT UNPAUSE. |
| `GET /api/v1/server/memory` | MEMORY STATS. |
| `GET /api/v1/server/memory/doctor` | MEMORY DOCTOR. |
| `GET /api/v1/server/latency` | LATENCY LATEST. |
| `DELETE /api/v1/server/latency` | LATENCY RESET. |
| `POST /api/v1/server/flushall` | FLUSHALL on masters, `async`. |
| `POST /api/v1/server/flushdb` | FLUSHDB on masters, `async`, `db`. |
| `POST /api/v1/server/save` | SAVE. |
| `POST /api/v1/server/bgsave` | BGSAVE. |
| `POST /api/v1/server/bgrewriteaof` | BGREWRITEAOF. |
| `POST /api/v1/server/shutdown` | SHUTDOWN, requires `node` or `target`: `{"save", "now", "force", "abort"}`. |
| `POST /api/v1/server/replicaof` | REPLICAOF, requires `node`: `{"host", "port"}` or `{"no_one": true}`. |
| `GET /api/v1/server/commands` | Command table with subcommands, flags and ACL categories: `prefix`. |
| `GET /api/v1/server/commands/docs` | COMMAND DOCS: `name` (comma-separated). |
| `GET /api/v1/server/modules` | MODULE LIST and detected capabilities. |

### Cluster

Cluster endpoints answer `501` on other topologies.

| Endpoint | Description |
|---|---|
| `GET /api/v1/cluster/info` | CLUSTER INFO, parsed. |
| `GET /api/v1/cluster/nodes` | CLUSTER NODES, parsed. |
| `GET /api/v1/cluster/shards` | CLUSTER SHARDS. |
| `GET /api/v1/cluster/slots` | CLUSTER SLOTS. |
| `GET /api/v1/cluster/keyslot` | Slot and owner of a key. |
| `GET /api/v1/cluster/slots/{slot}/keys` | Number of keys in a slot and up to `count` of them. |
| `POST /api/v1/cluster/failover` | CLUSTER FAILOVER on the replica `node`: `{"mode": "force\|takeover"}`. |
| `POST /api/v1/cluster/meet` | CLUSTER MEET: `{"ip", "port", "bus_port"}`. |
| `POST /api/v1/cluster/forget` | CLUSTER FORGET on every node: `{"node_id"}`. |
| `POST /api/v1/cluster/replicate` | CLUSTER REPLICATE on `node`: `{"master_id"}`. |
| `POST /api/v1/cluster/reset` | CLUSTER RESET on `node`: `{"hard"}`. |
| `POST /api/v1/cluster/addslots` | `{"slots": [...]}` or `{"ranges": [[0, 100]]}` on `node`. |
| `POST /api/v1/cluster/delslots` | The same for deleting slots. |
| `POST /api/v1/cluster/setslot` | CLUSTER SETSLOT on `node`: `{"slot", "state", "node_id"}`. |

### Scripts and functions

Scripts are loaded on every data node and functions on every master, so `EVALSHA` and `FCALL`
work for keys of any slot.

| Endpoint | Description |
|---|---|
| `POST /api/v1/scripts/eval` | EVAL or EVALSHA: `{"script" \| "sha", "keys", "args", "read_only"}`. |
| `POST /api/v1/scripts/load` | SCRIPT LOAD on every data node. |
| `GET /api/v1/scripts/exists` | SCRIPT EXISTS: `sha` (repeatable). |
| `POST /api/v1/scripts/flush` | SCRIPT FLUSH. |
| `POST /api/v1/scripts/kill` | SCRIPT KILL. |
| `GET /api/v1/functions` | FUNCTION LIST: `library`, `with_code`. |
| `POST /api/v1/functions` | FUNCTION LOAD: `{"code", "replace"}`. |
| `DELETE /api/v1/functions` | FUNCTION DELETE: `library`. |
| `POST /api/v1/functions/call` | FCALL or FCALL_RO: `{"function", "keys", "args", "read_only"}`. |
| `GET /api/v1/functions/stats` | FUNCTION STATS. |
| `GET /api/v1/functions/dump` | FUNCTION DUMP, base64. |
| `POST /api/v1/functions/restore` | FUNCTION RESTORE: `{"dump", "policy"}`. |
| `POST /api/v1/functions/flush` | FUNCTION FLUSH. |
| `POST /api/v1/functions/kill` | FUNCTION KILL. |

### ACL

ACL changes go to every data node.

| Endpoint | Description |
|---|---|
| `GET /api/v1/acl/users` | ACL USERS. |
| `GET /api/v1/acl/list` | ACL LIST. |
| `GET /api/v1/acl/users/{user}` | ACL GETUSER. |
| `PUT /api/v1/acl/users/{user}` | ACL SETUSER: `{"rules": ["on", ">secret", "~app:*", "+@read"], "reset"}`. |
| `DELETE /api/v1/acl/users/{user}` | ACL DELUSER. |
| `GET /api/v1/acl/log` | ACL LOG: `count`. |
| `DELETE /api/v1/acl/log` | ACL LOG RESET. |
| `POST /api/v1/acl/save` | ACL SAVE. |
| `POST /api/v1/acl/load` | ACL LOAD. |
| `POST /api/v1/acl/dryrun` | ACL DRYRUN: `{"user", "command": ["SET", "k", "v"]}`. |
| `GET /api/v1/acl/whoami` | ACL WHOAMI. |
| `GET /api/v1/acl/categories` | ACL CAT: `category`. |
| `GET /api/v1/acl/genpass` | ACL GENPASS: `bits`. |

### JSON

| Endpoint | Description |
|---|---|
| `GET /api/v1/json` | JSON.GET: `key`, `path` (repeatable). |
| `PUT /api/v1/json` | JSON.SET: `{"key", "path", "value", "nx", "xx"}`. |
| `DELETE /api/v1/json` | JSON.DEL. |
| `GET /api/v1/json/mget` | JSON.GET per key. |
| `POST /api/v1/json/merge` | JSON.MERGE. |
| `GET /api/v1/json/type` | JSON.TYPE. |
| `POST /api/v1/json/numincr` | JSON.NUMINCRBY. |
| `POST /api/v1/json/arrappend` | JSON.ARRAPPEND: `{"key", "path", "values"}`. |
| `POST /api/v1/json/arrinsert` | JSON.ARRINSERT. |
| `POST /api/v1/json/arrpop` | JSON.ARRPOP. |
| `GET /api/v1/json/arrlen` | JSON.ARRLEN. |
| `GET /api/v1/json/objkeys` | JSON.OBJKEYS. |
| `POST /api/v1/json/toggle` | JSON.TOGGLE. |
| `POST /api/v1/json/clear` | JSON.CLEAR. |

### Search

| Endpoint | Description |
|---|---|
| `GET /api/v1/search/indexes` | FT._LIST. |
| `POST /api/v1/search/indexes` | FT.CREATE: `{"index", "on", "prefixes", "filter", "language", "stopwords", "args", "schema": [{"field", "as", "type", "options"}]}`. |
| `GET /api/v1/search/indexes/{index}` | FT.INFO. |
| `DELETE /api/v1/search/indexes/{index}` | FT.DROPINDEX, `delete_documents`. |
| `POST /api/v1/search/indexes/{index}/fields` | FT.ALTER SCHEMA ADD. |
| `POST /api/v1/search/indexes/{index}/search` | FT.SEARCH: `{"query", "return", "sort_by", "sort_desc", "offset", "limit", "params", "dialect", "no_content", "with_scores", "args"}`. |
| `POST /api/v1/search/indexes/{index}/aggregate` | FT.AGGREGATE: `{"query", "args", "params", "dialect"}`. |
| `GET /api/v1/search/indexes/{index}/explain` | FT.EXPLAIN: `query`. |
| `GET /api/v1/search/indexes/{index}/tagvals` | FT.TAGVALS: `field`. |
| `PUT /api/v1/search/aliases` | FT.ALIASUPDATE: `{"alias", "index"}`. |
| `DELETE /api/v1/search/aliases/{alias}` | FT.ALIASDEL. |

### TimeSeries

| Endpoint | Description |
|---|---|
| `POST /api/v1/timeseries` | TS.CREATE: `{"key", "retention_ms", "encoding", "chunk_size", "duplicate_policy", "labels"}`. |
| `PUT /api/v1/timeseries` | TS.ALTER. |
| `POST /api/v1/timeseries/add` | TS.ADD: `{"key", "timestamp", "value", ...}`. |
| `POST /api/v1/timeseries/madd` | TS.ADD per sample across slots: `{"samples": [{"key", "timestamp", "value"}]}`. |
| `POST /api/v1/timeseries/incr` | TS.INCRBY or TS.DECRBY. |
| `GET /api/v1/timeseries/get` | TS.GET. |
| `GET /api/v1/timeseries/info` | TS.INFO. |
| `GET /api/v1/timeseries/range` | TS.RANGE or TS.REVRANGE: `from`, `to`, `count`, `aggregation`, `bucket_ms`, `rev`. |
| `GET /api/v1/timeseries/mrange` | TS.MRANGE: `filter` (repeatable), `with_labels`. |
| `GET /api/v1/timeseries/mget` | TS.MGET. |
| `GET /api/v1/timeseries/queryindex` | TS.QUERYINDEX. |
| `POST /api/v1/timeseries/delete` | TS.DEL: `{"key", "from", "to"}`. |
| `POST /api/v1/timeseries/rules` | TS.CREATERULE. |
| `DELETE /api/v1/timeseries/rules` | TS.DELETERULE. |

### Probabilistic structures

| Endpoint | Description |
|---|---|
| `POST /api/v1/bloom` | BF.RESERVE. |
| `POST /api/v1/bloom/add` | BF.MADD. |
| `GET /api/v1/bloom/exists` | BF.MEXISTS. |
| `GET /api/v1/bloom/card` | BF.CARD. |
| `GET /api/v1/bloom/info` | BF.INFO. |
| `POST /api/v1/cuckoo` | CF.RESERVE. |
| `POST /api/v1/cuckoo/add` | CF.INSERT or CF.INSERTNX. |
| `GET /api/v1/cuckoo/exists` | CF.MEXISTS. |
| `GET /api/v1/cuckoo/count` | CF.COUNT. |
| `POST /api/v1/cuckoo/delete` | CF.DEL. |
| `GET /api/v1/cuckoo/info` | CF.INFO. |
| `POST /api/v1/cms` | CMS.INITBYDIM or CMS.INITBYPROB. |
| `POST /api/v1/cms/incr` | CMS.INCRBY. |
| `GET /api/v1/cms/query` | CMS.QUERY. |
| `POST /api/v1/cms/merge` | CMS.MERGE. |
| `GET /api/v1/cms/info` | CMS.INFO. |
| `POST /api/v1/topk` | TOPK.RESERVE. |
| `POST /api/v1/topk/add` | TOPK.ADD. |
| `POST /api/v1/topk/incr` | TOPK.INCRBY. |
| `GET /api/v1/topk/list` | TOPK.LIST, `with_count`. |
| `GET /api/v1/topk/query` | TOPK.QUERY. |
| `GET /api/v1/topk/info` | TOPK.INFO. |
| `POST /api/v1/tdigest` | TDIGEST.CREATE. |
| `POST /api/v1/tdigest/add` | TDIGEST.ADD. |
| `GET /api/v1/tdigest/quantile` | TDIGEST.QUANTILE: `q` (repeatable). |
| `GET /api/v1/tdigest/cdf` | TDIGEST.CDF: `value` (repeatable). |
| `GET /api/v1/tdigest/rank` | TDIGEST.RANK. |
| `GET /api/v1/tdigest/revrank` | TDIGEST.REVRANK. |
| `GET /api/v1/tdigest/byrank` | TDIGEST.BYRANK: `rank` (repeatable). |
| `GET /api/v1/tdigest/byrevrank` | TDIGEST.BYREVRANK. |
| `GET /api/v1/tdigest/min` | TDIGEST.MIN. |
| `GET /api/v1/tdigest/max` | TDIGEST.MAX. |
| `GET /api/v1/tdigest/trimmed-mean` | TDIGEST.TRIMMED_MEAN: `low`, `high`. |
| `POST /api/v1/tdigest/reset` | TDIGEST.RESET. |
| `POST /api/v1/tdigest/merge` | TDIGEST.MERGE. |
| `GET /api/v1/tdigest/info` | TDIGEST.INFO. |

### Vector sets

| Endpoint | Description |
|---|---|
| `POST /api/v1/vectorsets/add` | VADD: `{"key", "element", "vector", "reduce", "quantization", "ef", "m", "attributes", "cas"}`. |
| `POST /api/v1/vectorsets/search` | VSIM: `{"key", "vector" \| "element", "count", "ef", "filter", "with_scores"}`. |
| `DELETE /api/v1/vectorsets/elements` | VREM. |
| `GET /api/v1/vectorsets/card` | VCARD. |
| `GET /api/v1/vectorsets/dim` | VDIM. |
| `GET /api/v1/vectorsets/info` | VINFO. |
| `GET /api/v1/vectorsets/embedding` | VEMB. |
| `GET /api/v1/vectorsets/attributes` | VGETATTR. |
| `PUT /api/v1/vectorsets/attributes` | VSETATTR. |
| `GET /api/v1/vectorsets/random` | VRANDMEMBER. |
| `GET /api/v1/vectorsets/links` | VLINKS. |

## Metrics

The metrics server (`METRICS_ADDR`) serves Prometheus metrics at `/metrics`:

- `redigate_http_requests_total{route, code}`, `redigate_http_request_duration_seconds{route}`;
- `redigate_redis_commands_total{command, status}`, `redigate_redis_command_duration_seconds{command}`;
- Go runtime and process metrics.

## Development

```sh
make build              # bin/redigate
make test               # unit tests
make lint               # golangci-lint
make up / make down     # local cluster and redigate from compose.yaml
make test-integration   # integration tests inside the compose network
make test-matrix        # Redis 7.4, Redis 8, Valkey 8 and RESP2
make swagger            # regenerate the OpenAPI specification
make docker             # local image redigate:<version>
```

### Publishing the image

Images are published to the GitHub Container Registry. Create a personal access token with the
`write:packages` scope and put it into `.env` (ignored by git and excluded from the Docker build
context; `.env.example` lists the variables):

```sh
cp .env.example .env                    # then set GHCR_TOKEN in .env
make docker-login                       # once
git tag v1.0.0                          # a clean version: dirty trees are not published
make docker-push                        # ghcr.io/btrvodka/redigate:v1.0.0 and :latest, amd64 and arm64
make docker-push LATEST=false           # without moving :latest
```

`IMAGE`, `GHCR_USER` and `PLATFORMS` override the defaults. Pushing a `v*` tag to GitHub publishes
the image from CI as well (`.github/workflows/release.yml`). The first published package is
private: make it public in the package settings on GitHub.

### OpenAPI

The specification is generated with [swag](https://github.com/swaggo/swag) from annotations
written right above route registrations in `internal/httpapi/*_routes.go`; field descriptions
come from comments of request types and of result types in `internal/service`. After changing
routes, annotations or request types run:

```sh
make swagger            # internal/httpapi/openapi/swagger.{json,yaml}
```

The specification is embedded into the binary and served at `/api/v1/openapi.json` and
`/api/v1/openapi.yaml`; Swagger UI ([swaggest/swgui](https://github.com/swaggest/swgui))
renders it at `/api/v1/docs/` when `UI_ENABLED=true`. "Try it out" sends requests to the same
server: with authentication enabled press Authorize and enter `Bearer <token>`. Tests fail when a route has no annotation or the README misses it,
CI fails when the generated files are outdated.

### Integration tests

Integration tests run inside the compose network because cluster and sentinel announce
container addresses. Every run recreates the deployments: the tests trigger failovers.

## License

[MIT](LICENSE)
