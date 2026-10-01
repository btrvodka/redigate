// Package httpapi implements the redigate HTTP API.
//
// The OpenAPI specification is generated from the annotations above route
// registrations with `make swagger` and served at /api/v1/openapi.json.
//
//	@title			redigate API
//	@version		1.0
//	@description	HTTP API for Redis and Valkey: standalone, cluster and sentinel.
//	@description
//	@description	Every JSON response is wrapped into Response; errors use ErrorResponse with a stable error_code.
//	@description
//	@description	Common query parameters (where they make sense):
//	@description	db - database (standalone and sentinel);
//	@description	node - send the request to the node with this address;
//	@description	target - auto (by key), node, masters, replicas, all or sentinels; fan-out targets return a result per node;
//	@description	encoding - auto (binary strings become {"base64": "..."}), utf8 or base64;
//	@description	key_encoding=base64 - keys, fields and members in the query string are base64;
//	@description	timeout - request timeout for blocking commands, e.g. 30s;
//	@description	duration - duration of streams, exports and imports;
//	@description	pretty - indented JSON.
//	@description
//	@description				In request bodies keys and values are strings, numbers or {"base64": "..."} objects.
//	@license.name				MIT
//	@BasePath					/api/v1
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				"Bearer <API_TOKEN>" or "Bearer <API_READONLY_TOKEN>". Not required when authentication is disabled.
package httpapi
