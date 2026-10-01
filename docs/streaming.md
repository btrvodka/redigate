# Streaming

Pub/Sub subscriptions, `MONITOR` and stream tailing are served as
[server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html): a long
HTTP response where every event has a type and JSON data.

```sh
curl -N 'localhost:8080/api/v1/pubsub/subscribe?channel=news&pattern=events.*&shard_channel=orders'
curl -N 'localhost:8080/api/v1/monitor?duration=30s'
curl -N 'localhost:8080/api/v1/streams/tail?key=orders&group=billing&consumer=c1'
```

```
event: message
data: {"channel":"news","payload":"hello"}
```

| Endpoint | Events |
|---|---|
| `GET /api/v1/pubsub/subscribe` | `subscription`, `message` (channel, pattern, payload, sharded) |
| `GET /api/v1/monitor` | `command` (node, time, db, client, args) |
| `GET /api/v1/streams/tail` | `entry` (stream, id, fields) |

- The last event is always `end` with the reason: `duration`, `canceled` or `closed`.
- Recoverable problems arrive as `error` events; heartbeats are sent as `: ping` comments.
- A stream lasts up to `duration` (at most `MAX_STREAM_DURATION`); at most `MAX_STREAMS` streams,
  exports and imports run at once.
- Subscriptions take `channel`, `pattern` and `shard_channel`, each repeatable. In a cluster
  shard channels of different slots are subscribed through separate connections.
- `MONITOR` watches the masters by default, or the nodes given by `node` or `target`.
- Stream tailing starts after the last entry, from the given `id`, or reads through a consumer
  group with `group` and `consumer`.

Messages are published with `POST /api/v1/pubsub/publish`:
`{"channel": "news", "message": "hello", "sharded": false}`.
