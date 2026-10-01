# Usage

Examples assume redigate on `localhost:8080` without authentication; add
`-H 'Authorization: Bearer <token>'` when a token is set. All endpoints are described in
Swagger UI at `/api/v1/docs/`.

## Commands

`POST /api/v1/command` accepts a redis-cli style command line or JSON:

```sh
curl -d 'SET "my key" "a value" EX 60' localhost:8080/api/v1/command
curl -H 'Content-Type: application/json' \
  -d '{"args": ["SET", "bin", {"base64": "/wA="}]}' localhost:8080/api/v1/command
```

Commands that change the state of a connection (`SELECT`, `AUTH`, `HELLO`, `RESET`,
`CLIENT REPLY`, `CLIENT SETNAME`, `MULTI`, `WATCH`, `READONLY`, ...) run on a dedicated
connection, so they never leak into the connection pool. To send a command to a specific node or
to a group of nodes see [nodes and targets](topologies.md#nodes-and-targets).

## Pipelines, transactions and sessions

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

## Keys and data types

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

## Scripts and functions

```sh
curl -d '{"script": "return redis.call(\"GET\", KEYS[1])", "keys": ["greeting"]}' localhost:8080/api/v1/scripts/eval
curl -d '{"script": "return 1"}' localhost:8080/api/v1/scripts/load
```

`SCRIPT LOAD` and `FUNCTION LOAD` go to every relevant node and return the result per node, so
`EVALSHA` and `FCALL` work for keys of any cluster slot.

## Modules

JSON, Search, TimeSeries, Bloom, Cuckoo, Count-Min Sketch, Top-K, t-digest and vector sets have
typed endpoints. They are built into Redis 8; on servers without a module the endpoints answer
`501 NOT_SUPPORTED`, and `GET /api/v1/server/modules` shows what is available. Module commands
can always be sent through `/api/v1/command` as well.

```sh
curl -X PUT -d '{"key": "doc", "value": {"title": "Hello", "tags": ["a"]}}' localhost:8080/api/v1/json
curl 'localhost:8080/api/v1/json?key=doc&path=$.title'
curl -d '{"key": "vs", "element": "a", "vector": [1, 0]}' localhost:8080/api/v1/vectorsets/add
curl -d '{"key": "vs", "vector": [0.9, 0.1], "count": 5}' localhost:8080/api/v1/vectorsets/search
```
