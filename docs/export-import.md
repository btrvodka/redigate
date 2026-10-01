# Export and import

`GET /api/v1/keys/export` streams keys as NDJSON, one key per line, from every master.
`POST /api/v1/keys/import` restores such a file in batches; keys may belong to different
cluster slots.

```sh
# Exact copy between compatible servers
curl 'localhost:8080/api/v1/keys/export?match=user:*' > users.ndjson
curl --data-binary @users.ndjson 'localhost:8080/api/v1/keys/import?replace'

# Portable copy, e.g. from Redis 8 to Valkey
curl 'host-a:8080/api/v1/keys/export?format=value' \
  | curl --data-binary @- 'host-b:8080/api/v1/keys/import'
```

## Formats

- `dump` (default) keeps every type, including module types, with `DUMP`/`RESTORE`. `RESTORE`
  accepts dumps only from servers with a compatible RDB version: a Redis 8 dump can't be restored
  on Valkey 8, Valkey 9 or Redis 7.4, and Valkey 9 dumps can't be restored on Redis 8.
- `value` stores plain values of strings, lists, sets, hashes, sorted sets, streams (without
  consumer groups) and JSON, and works between any versions and forks. Keys of other types are
  marked in the file and skipped by the import.

TTLs are kept in both formats.

## Options

- Export: `match`, `type`, `format`, `db`. The last line is
  `{"summary": {"keys": n, "complete": bool}}`.
- Import: `replace` overwrites existing keys (otherwise they are skipped), `db`. The result
  counts imported, skipped and failed keys and lists up to 100 errors with line numbers; a
  broken line does not stop the import.
- The import body may be up to `MAX_IMPORT_BYTES`; exports and imports share the `MAX_STREAMS`
  slots and are limited by `MAX_STREAM_DURATION`.
