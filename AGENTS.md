# trailrunn.ing

A web app for trail runners: training plans, races and race plans, routes,
activities synced from Strava, and trip and race reports. A Go server serves a
React single-page app and a Connect API, which it also exposes to AI agents as
an MCP server. Data is in Postgres with PostGIS and pgRouting. It runs on Cloud
Run, with Neon Postgres, deployed with Terraform.

Directories have their own AGENTS.md with the rules for their code: `api/`,
`internal/`, `internal/services/`, `internal/store/`, `web/` and `infra/`.

## Layout

- `api/<domain>/v1/*.proto`: the API, in Protocol Buffers.
- `main.go`: the server, its flags and wiring. `import_osm.go`: the
  `import-osm` subcommand, which imports OpenStreetMap data for routing.
- `internal/<domain>/` (e.g. `races`, `plans`, `trips`): repositories over the
  database, with sqlc queries in `queries/`.
- `internal/services/<name>/`: the Connect services implementing the API.
- `internal/store/`: the database connection and migrations.
- `internal/` also has clients for other services (`strava`, `livetrack`,
  `mapdata`, `elevation`), routing (`routing`), geometry (`geo`), and
  telemetry and error reporting (`telemetry`, `errreport`).
- `web/`: the React app. Its bundle in `web/dist/` is embedded in the server.
- `infra/`: Terraform for Google Cloud.
- `images/Dockerfile`: the container images the Makefile and CI run, like
  Node's and the server's base image, and the Terraform version, kept there
  for Dependabot to update. `images/image.sh` reads them.

## Commands

```bash
make run      # Start Postgres and the server on http://localhost:8080
make watch    # The same, rebuilding the web app and restarting the server on changes
make test     # Start Postgres and run all Go tests with the race detector
make gen      # Regenerate code from the protos and SQL, and rebuild the web app
make format   # Format Go, TypeScript and protos
make lint     # Lint Go, TypeScript and protos
```

`make run` and `make watch` pass `-local-sign-in`, so agents and tests can
sign in to the local server without a passkey: open
http://localhost:8080/local-sign-in, optionally with `?next=/some/page`, to
sign in as the local administrator. The server only allows the flag on
localhost.

Node tools run in containers, and Go tools with `go tool` (buf, sqlc,
esbuild, gofumpt, staticcheck, ko). `make` with no target lists the rest.

CI fails if `make format` or `make gen` change anything, if `go mod tidy`
changes `go.mod`, or if lint or tests fail. Before finishing a change, run
the targets that cover it.

## Generated code

Generated code is committed: `internal/gen/` and `web/gen/` from the protos,
`internal/*/internal/gen/` from the SQL, `internal/apidesc/descriptor.binpb`
and the bundle in `web/dist/`. Never edit it by hand; change the source and run
`make gen` (or `make buf`, `make sqlc`). `make buf` needs network access for
the TypeScript plugins.

## Conventions everywhere

- Comments and documentation use American spelling, like the code.
- Comments are full sentences, and explain why as well as what: the
  constraint, the cost, what would break otherwise.
- Quantities carry their unit in their name, in full words where they read
  well: `distance_meters`, `DistanceMeters`, `cutoff_seconds`. Units are
  metric only.
- Calendar dates are `YYYY-MM-DD` strings in the API and `date` in the
  database. Instants are timestamps, named `*_time` (`create_time`,
  `update_time`).
- The API's comments are the documentation AI agents read through MCP. Error
  messages reach agents and users too, so make them say what to do.
- Keep the free tier in mind: the server runs as at most one small Cloud Run
  instance (512 MiB, 1 CPU) that scales to zero, and every database round trip
  costs about 28 ms in production.
