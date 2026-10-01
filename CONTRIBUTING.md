# Contributing

## Development

Requires Go 1.27+ and Docker for integration tests.

```sh
make build              # bin/redigate
make test               # unit tests
make lint               # golangci-lint
make up / make down     # local cluster and redigate from compose.yaml
make test-integration   # integration tests inside the compose network
make test-matrix        # Redis 7.4, Redis 8, Valkey 8, Valkey 9 and RESP2
make swagger            # regenerate the OpenAPI specification
make docker             # local image redigate:<version>
```

Unit tests run against [miniredis](https://github.com/alicebob/miniredis). Integration tests
(build tag `integration`) run inside the compose network, because cluster and sentinel announce
container addresses; every run recreates the deployments, the tests trigger failovers. Use
`REDIS_IMAGE=valkey/valkey:9 make test-integration` for another server.

## Adding an endpoint

1. Add the route to the matching `internal/httpapi/*_routes.go`; typed endpoints build a command
   and run it through the service layer.
2. Declare the request body as a package-level type and comment its fields: the comments become
   descriptions in the specification.
3. Write the swag annotation block right above the route registration.
4. Run `make swagger`. Tests fail when a route has no annotation, CI fails when the generated
   files are outdated.

## OpenAPI

The specification is generated with [swag](https://github.com/swaggo/swag) from the
annotations, field descriptions come from comments of request types and of result types in
`internal/service`. It is embedded into the binary, served at `/api/v1/openapi.json` and
`/api/v1/openapi.yaml` and rendered by Swagger UI ([swaggest/swgui](https://github.com/swaggest/swgui))
at `/api/v1/docs/`.

## Publishing the image

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
