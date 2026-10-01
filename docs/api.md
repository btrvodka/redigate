# API conventions

Every endpoint, its parameters and its schemas are described in the OpenAPI specification:
browse it in Swagger UI at `/api/v1/docs/` or download `/api/v1/openapi.json` and
`/api/v1/openapi.yaml`. This page explains what is common to all of them.

## Endpoint groups

| Prefix | What it covers |
|---|---|
| `/api/v1/command`, `/api/v1/pipeline` | Any command, pipelines, transactions and sessions, see [usage](usage.md#commands). |
| `/api/v1/keys` | SCAN, values of any type, TTL, rename, copy, dump/restore, pattern deletion, [export and import](export-import.md). |
| `/api/v1/strings`, `hashes`, `lists`, `sets`, `zsets`, `streams`, `bitmaps`, `hll`, `geo` | Typed operations on data types. |
| `/api/v1/pubsub`, `/api/v1/monitor`, `/api/v1/streams/tail` | Publishing and [streaming](streaming.md). |
| `/api/v1/server` | INFO, CONFIG, clients, slowlog, memory, latency, flushes, persistence, shutdown, replication, command table, modules. |
| `/api/v1/cluster` | Cluster info, nodes, slots, failover, meet, forget, slot assignment. |
| `/api/v1/scripts`, `/api/v1/functions` | Lua scripts and functions, loaded on every relevant node. |
| `/api/v1/acl` | Users, rules, ACL log, dry runs. |
| `/api/v1/json`, `search`, `timeseries`, `bloom`, `cuckoo`, `cms`, `topk`, `tdigest`, `vectorsets` | [Modules](usage.md#modules). |
| `/api/v1/ping`, `/api/v1/topology` | Nodes and their health, see [topologies](topologies.md). |

`GET /healthz` (liveness) and `GET /readyz` (redis is reachable) are outside `/api/v1` and need no token.

## Authentication

With `API_TOKEN` set, requests need `Authorization: Bearer <token>`. Without tokens
authentication is off, see [configuration](configuration.md#http-server-and-security).

### Read-only access

Requests with `API_READONLY_TOKEN` (or with `READ_ONLY=true`) may run only commands that do not
modify data or server state. The decision uses the command table of the server (`COMMAND`):
commands with the `write` or `may_replicate` flag or the `@write` ACL category are denied,
`admin` commands are denied except reading ones (`CONFIG GET`, `CLIENT LIST`, `SLOWLOG GET`,
`CLUSTER NODES`, `ACL GETUSER`, ...), as well as `PUBLISH`, `EVAL`, `FCALL`, `SCRIPT LOAD`
and `MONITOR`. Unknown commands are denied.

## Responses

Every endpoint except streams and exports returns a JSON envelope:

```json
{"error_code": "OK", "error_description": "", "request_id": "6f1c...", "result": {"value": "hello"}}
```

Add `?pretty` to any request for indented JSON. The request id is taken from `X-Request-Id`
or generated, and is returned in the same header.

A missing value (nil reply) is a successful `null`, except for typed key endpoints such as
`GET /api/v1/keys/value`, which return `404`.

### Errors

| HTTP | `error_code` | Meaning |
|---|---|---|
| 400 | `BAD_REQUEST` | Invalid parameters. |
| 400 | `REDIS_ERROR`, `WRONG_TYPE` | The server rejected the command. |
| 400 | `CROSS_SLOT` | Keys of a transaction or session hash to different cluster slots. |
| 400 | `UNSUPPORTED_COMMAND` | `SUBSCRIBE`, `MONITOR` and `SYNC` belong to [streaming endpoints](streaming.md). |
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

## Binary data

Replies use the `encoding` parameter:

- `auto` (default): valid UTF-8 strings are JSON strings, other strings are `{"base64": "..."}`;
- `utf8`: always strings, invalid bytes are replaced with U+FFFD;
- `base64`: every string is base64-encoded.

Request bodies accept a string, a number or `{"base64": "..."}` wherever a key or a value is
expected. Keys, fields and members in the query string are base64 with `key_encoding=base64`.

## Common parameters

| Parameter | Description |
|---|---|
| `db` | Database (standalone and sentinel). |
| `node` | Send the request to the node with this address, e.g. `10.0.0.5:6379`. |
| `target` | `auto` (default, by key), `node`, `masters`, `replicas`, `all` (data nodes) or `sentinels`, see [topologies](topologies.md#nodes-and-targets). |
| `encoding` | Reply encoding, see [binary data](#binary-data). |
| `timeout` | Request timeout for blocking commands, up to `MAX_BLOCK_TIMEOUT`. |
| `duration` | Duration of streams, exports and imports, up to `MAX_STREAM_DURATION`. |
