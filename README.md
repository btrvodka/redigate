# redigate

An HTTP API for Redis and Valkey that works the same way with a **single server**, a
**Redis Cluster** and a **Sentinel** deployment: point it at any of them and the topology is
detected automatically.

redigate is built for development and test environments. It exposes **everything** a Redis
server can do over HTTP, including dangerous operations such as `FLUSHALL`, `CONFIG SET`,
`CLUSTER FAILOVER`, `SHUTDOWN` and `DEBUG`. Do not expose it to untrusted networks and do
not point it at production data.

## Features

- **Standalone, cluster and sentinel** with the same API: commands are routed by key, keys of
  every master are scanned, multi-key requests work across cluster slots, and any command can be
  sent to a specific node or to all masters, replicas or sentinels. See [topologies](docs/topologies.md).
- **Any command** through `/api/v1/command` and `/api/v1/pipeline`, including transactions and
  connection-scoped commands, plus typed endpoints for keys, data types, server and cluster
  administration, scripts, functions and ACL.
- **Modules**: JSON, Search, TimeSeries, probabilistic structures and vector sets (built into Redis 8).
- **Streaming** over server-sent events: Pub/Sub, `MONITOR` and stream tailing.
- **Export and import** of keys, exact or portable between Redis and Valkey versions.
- **Binary safe**, bearer token authentication with an optional read-only token.
- **Web UI** with a key browser and a command console, and **Swagger UI** for the whole API.
- RESP2 and RESP3, TLS, Prometheus metrics, a single static binary and a multi-arch image.

Tested against Redis 7.4, Redis 8, Valkey 8 and Valkey 9 in every topology.

## Quick start

Try redigate against a Redis or Valkey server running on your machine:

```sh
docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -e ALLOW_INSECURE=true \
  -e REDIS_URL=redis://host.docker.internal:6379 \
  ghcr.io/btrvodka/redigate:latest
```

```sh
curl -d 'SET greeting "hello world"' localhost:8080/api/v1/command
curl -d 'GET greeting' localhost:8080/api/v1/command
```

Then open the web UI at <http://localhost:8080/ui/> and Swagger UI at <http://localhost:8080/api/v1/docs/>.

- `ALLOW_INSECURE=true` turns authentication off: anyone who reaches the port has full access to
  Redis. Keep the port bound to `127.0.0.1` as above, or set a token instead (below).
- `host.docker.internal` is the host machine in Docker Desktop. On Linux add
  `--add-host=host.docker.internal:host-gateway`, or use any other address in `REDIS_URL`.

### With a token

```sh
docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -e API_TOKEN=change-me \
  -e REDIS_URL=redis://host.docker.internal:6379 \
  ghcr.io/btrvodka/redigate:latest

curl -H 'Authorization: Bearer change-me' localhost:8080/api/v1/ping
```

### Cluster and sentinel

Pass a few node addresses, the rest is detected:

```sh
-e REDIS_URL=redis://node1:6379,node2:6379,node3:6379                     # cluster
-e REDIS_URL=redis+sentinel://sentinel1:26379,sentinel2:26379/mymaster    # sentinel
```

Cluster nodes and sentinels announce their own addresses, so run redigate in the same Docker
network as the nodes. To try it locally, `make up` starts a Redis cluster (3 masters, 3 replicas)
with redigate on `127.0.0.1:8080` from this repository.

### Docker Compose

```yaml
services:
  redis:
    image: redis:8

  redigate:
    image: ghcr.io/btrvodka/redigate:latest
    environment:
      REDIS_URL: redis://redis:6379
      API_TOKEN: change-me
    ports: ["127.0.0.1:8080:8080"]
    depends_on: [redis]
```

### From source

```sh
go install github.com/btrvodka/redigate/cmd/redigate@latest
REDIS_URL=redis://localhost:6379 redigate
```

## A taste of the API

```sh
curl -d 'INFO memory' 'localhost:8080/api/v1/command?target=masters'      # every master
curl -d $'SET n 1\nINCR n\nGET n' localhost:8080/api/v1/pipeline           # one round trip
curl 'localhost:8080/api/v1/keys?match=user:*&meta'                        # SCAN over the cluster
curl 'localhost:8080/api/v1/keys/value?key=user:1'                         # a value of any type
curl -N 'localhost:8080/api/v1/pubsub/subscribe?channel=news'              # server-sent events
```

## Documentation

- [Standalone, cluster and sentinel](docs/topologies.md): connecting, routing, nodes and targets.
- [Configuration](docs/configuration.md): environment variables, `REDIS_URL`, security, limits, metrics.
- [API conventions](docs/api.md): responses, errors, binary data, common parameters, read-only access.
- [Usage](docs/usage.md): commands, pipelines and transactions, keys and data types, scripts, modules.
- [Streaming](docs/streaming.md): Pub/Sub, `MONITOR` and stream tailing.
- [Export and import](docs/export-import.md): moving keys between servers.
- [Web UI and Swagger UI](docs/web-ui.md).
- Every endpoint: Swagger UI at `/api/v1/docs/` or the specification in
  [`internal/httpapi/openapi`](internal/httpapi/openapi/swagger.yaml).
- [Contributing](CONTRIBUTING.md): development, tests, OpenAPI generation, publishing the image.

## License

[MIT](LICENSE)
