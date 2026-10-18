# API

The API is defined here and generated into `internal/gen/` (Go),
`web/gen/` (TypeScript) and `internal/apidesc/descriptor.binpb`. Run
`make buf` (or `make gen`) after changing a proto; `TestUpToDate` in
`internal/apidesc` fails if the descriptor is stale. `make buf-lint` runs buf's
STANDARD lint rules, and `make buf-format` formats the files.

## Files and packages

- One directory per domain, `<domain>/v1/<name>.proto`, in package
  `<domain>.v1`. A domain with several services has a file per service (e.g.
  `races/v1/races.proto`, `race_plans.proto`, `race_reports.proto`).
- Files use `syntax = "proto3";`. Import other domains relative to `api/`,
  e.g. `import "geo/v1/geo.proto";`.

## Services and methods

- Services are named `<Noun>Service`.
- Methods are `Create`, `Get`, `List`, `Update` and `Delete` followed by the
  resource, `Set*` for create-or-replace, and plural names with
  `repeated string ids` for bulk operations.
- Every method has its own `<Method>Request` and `<Method>Response`. Create,
  Get and Update responses wrap the resource in one field; Delete responses are
  empty.
- Method names must be unique across all services, as they're the MCP tool
  names.
- Methods named `Get`, `List`, `Search`, `Find`, `Summarize` or `Preview`
  are marked read-only for MCP clients, so don't use those prefixes for methods
  that change anything.

## Messages and fields

- IDs are `string id`, and references `<thing>_id` or `<thing>_ids`.
- Quantities carry their unit: `distance_meters`, `target_duration_seconds`.
  Durations are integer `*_seconds`, not `google.protobuf.Duration`.
- Instants are `google.protobuf.Timestamp` named `*_time`. Calendar dates are
  strings documented as "(YYYY-MM-DD)", and local times strings documented as
  "HH:MM".
- Unset values are the zero value: say so in the comment, e.g. "0 if not set"
  or "Empty if none".
- Enum values are prefixed with the enum's name, with `<ENUM>_UNSPECIFIED = 0`.
  Retire values with `[deprecated = true]` and a "Deprecated: use X." comment
  rather than removing them.
- Reserve the numbers and names of removed fields.
- Use a `oneof` when exactly one of several inputs must be set.
- There are no validation annotations. Document limits and required fields in
  comments ("Maximum 400."), and check them in the service.
- Updates take the resource and a `google.protobuf.FieldMask update_mask`,
  documented as "The fields to update, e.g. "date,course_route_id". If empty,
  all fields are replaced." The service applies it with `internal/protomask`.
- List methods return everything, in a documented order, unless the list can
  grow large: then page with `page_size`, `page_token` and `next_page_token`,
  like `ListActivities`.

## Comments

Comments are the MCP tool and schema descriptions AI agents read, so write them
for an agent that knows nothing about the app:

- Service comments explain the domain and how it relates to other services
  ("See RaceService").
- Method comments say what the method does, its side effects ("Its race plans
  are deleted too"), and its errors ("fails with FAILED_PRECONDITION if ...").
- Field comments give the meaning, unit, format, defaults and limits, with
  examples in quotes. Comment every field, including each of a pair.
- Tell agents when to do or not do something, e.g. "only set on_calendar when
  the user says they are running the race".

## MCP options

Every method of the services `main.go` exposes over MCP becomes a tool, unless
it's excluded:

- Exclude methods that only make sense in the web app, upload files, manage
  credentials or serve the public with
  `option (mcp.v1.method).exclude = true;`.
- Tools can't take files, and base64 in arguments costs agents a token per
  few bytes, so give agents a link to upload files to over HTTP instead, like
  `CreateTripPhotoUpload` (see `internal/photos`).
- Mark fields the server sets, like `create_time`, `update_time` and computed
  values, with `[(mcp.v1.field).output_only = true]`, which hides them from
  tool inputs.
- Return images agents should look at in a top-level `mcp.v1.Image` field of
  the response. Tools return them as image content, which clients show to the
  model, and leave them out of the JSON. Return URLs instead for images agents
  only link to: they can't see draft photos through them, as those need the
  user's session.
