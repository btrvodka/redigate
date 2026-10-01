# Standalone, cluster and sentinel

redigate works with a single Redis or Valkey server, a Redis Cluster and a Sentinel deployment.
The same API serves all of them: the topology is detected when redigate starts, and requests are
routed to the right nodes.

## Connecting

Pass one or more addresses in `REDIS_URL` (or `REDIS_ADDRS`), see [configuration](configuration.md):

```sh
REDIS_URL=redis://localhost:6379                                          # a single server
REDIS_URL=redis://node1:6379,node2:6379,node3:6379                        # cluster seed nodes
REDIS_URL=redis+sentinel://sentinel1:26379,sentinel2:26379/mymaster       # sentinels and the master name
```

With `REDIS_MODE=auto` (the default) redigate asks the first reachable address with `INFO`:
`redis_mode:sentinel` means sentinel, `cluster_enabled:1` means cluster, anything else is a
standalone server. Set `REDIS_MODE` to `standalone`, `cluster` or `sentinel` to skip detection.
When sentinels monitor a single master, its name is detected too.

`GET /api/v1/topology` shows the detected topology and the nodes, `GET /api/v1/ping` pings each of them.

## Nodes and targets

Every node has a role: `master`, `replica` or `sentinel`. Requests go to:

- `target=auto` (default): routed by key in a cluster, to the master otherwise;
- `node=<address>`: a specific node, e.g. `?node=10.0.0.5:6379`;
- `target=masters`, `replicas`, `all` (masters and replicas) or `sentinels`: every node of the
  group concurrently, with a result per node and `"ok": false` if any of them failed.

```sh
curl -d 'INFO memory' 'localhost:8080/api/v1/command?target=masters'
curl -d 'CONFIG SET maxmemory 100mb' 'localhost:8080/api/v1/command?target=all'
```

## Standalone

- The configured server and its replicas from `INFO replication` are the nodes.
- Every database is available with `?db=N`; `SELECT` itself runs on a dedicated connection and
  never affects other requests.

## Cluster

- Commands are routed by the slot of their keys and follow `MOVED`/`ASK` redirects.
- SCAN (`GET /api/v1/keys`), `DBSIZE`, `FLUSHALL`, export and pattern deletion walk every master.
- Multi-key endpoints that take `?key=a&key=b` (exists, delete, `strings/mget`, `json/mget`, ...)
  and plain pipelines work with keys of different slots.
- Transactions (`?atomic`), sessions (`?session`) and server-side multi-key commands (`SINTER`,
  `RENAME`, multi-key `EVAL`, ...) need keys of one slot: use hash tags like `{user:1}:name`.
  redigate checks this itself and answers `400 CROSS_SLOT`.
- `SCRIPT LOAD`, `FUNCTION LOAD` and `ACL SETUSER` go to every relevant node, so `EVALSHA`,
  `FCALL` and authentication work for keys of any slot.
- Cluster administration: `/api/v1/cluster/...` (nodes, slots, failover, meet, forget, ...).
- Only db 0 exists in a cluster.
- Redis 8 coordinates Search and TimeSeries queries across the cluster; for clusters without a
  coordinator pass `?target=masters` to get per-shard results.

## Sentinel

- Commands follow the current master, also after a failover.
- Replicas and sentinels are nodes too; sentinels accept `SENTINEL ...` commands with `?node=`:

```sh
curl -d 'SENTINEL failover mymaster' 'localhost:8080/api/v1/command?node=10.0.0.20:26379'
```

## Networking

Cluster nodes and sentinels tell clients their own addresses. redigate must be able to reach
those addresses: run it in the same Docker network as the nodes (or with `--network host` on
Linux), not only with a port published to the host.
