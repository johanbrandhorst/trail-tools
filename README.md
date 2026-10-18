# Trail tools

A trail running planner: sync your activities from Strava, plan your training
for up to a year towards your goal races, design routes on a topo map, check
the weather along a route, build an opinionated race day plan you can
share with your crew, and write up your adventures as public trip reports.

Everything is also exposed as an [MCP](https://modelcontextprotocol.io) server,
so you can bring your own AI agent to do the planning.

## Features

- **Training plans**: plans of up to a year with goal races, planned at the
  month (phase and volume), week (focus and targets) and day (workouts) level,
  compared with what you actually ran.
- **Routes**: a Strava style route editor where waypoints snap to trails or
  roads (routed with [pgRouting](https://pgrouting.org) on
  [OpenStreetMap](https://www.openstreetmap.org) data), prefer the paths you've run before
  (your heatmap), or are joined by straight lines for off-trail sections, mixed
  within one route. Drag the route to reshape it, undo and redo, close the loop
  or turn it into an out and back. You can also import GPX files or turn a past
  activity into a route. Each route has an elevation profile, distance, gain,
  grades and a terrain breakdown, and can be downloaded as GPX for your watch.
  Overlays show your personal heatmap, OpenStreetMap trails and points of
  interest.
- **Weather**: an hourly forecast for where you'll be along a route, given a
  start time and pace, with warnings for thunderstorms, freezing levels, wind
  and darkness ([Open-Meteo](https://open-meteo.com)). Works for saved routes
  and uploaded GPX files.
- **Race plans**: goals, checkpoints with cutoffs, pacing, fueling, gear, crew,
  contingencies, race morning and mental strategy. The plan is checked against
  a set of rules (e.g. cutoff buffers, carbohydrate per segment, a headlamp if
  you'll be out in the dark) and can be shared read-only with a link.
- **Activities**: your full Strava history including GPS, elevation and heart
  rate streams, stored locally.
- **Trip reports**: turn an activity into a blog style story with photos,
  each placed on the activity's track from its GPS position or the time it
  was taken. Published reports are public at `/u/<handle>/<slug>` and listed
  on your public page at `/u/<handle>`, with the metadata search engines and
  link previews need.

## Prerequisites

- Go
- Docker or [Apple `container`](https://github.com/apple/container)

## Running

To start Postgres and run the server locally, use:

```sh
$ make run
```

On a database without an administrator, opening http://localhost:8080 on the
same machine goes to the invitation to create the administrator account with a
passkey (`make watch` works the same way). It's
http://localhost:8080/invite#local-admin, if you need the link. Trail Tools is invite only: invite
other users, or yourself to add a passkey on another device, from the settings
page.

### Strava

Without Strava credentials, `make run` and `make watch` use a fake Strava
(`-fake-strava`), so every feature can be tried locally. Connecting Strava
under Settings shows a page that says it's fake, and syncs a made-up athlete
from Squamish, BC with about four months of activities up to today: runs,
trail runs and the odd race, hikes, ski tours, and road and mountain bike
rides. They have GPS tracks with elevation, heart rate and cadence, and the
longer outings have photos, for trip reports.

The tracks follow real trails and roads. The fake Strava comes with the map
data of Squamish embedded (`internal/demo`): its trails and points of
interest from OpenStreetMap, loaded into the routing graph at startup unless
an imported region covers Squamish already, and its elevations. So routing,
snapping activities to trails, the trails layer and elevation profiles work
around Squamish without importing or downloading map data.
`go generate ./internal/demo` updates the data from the Geofabrik extract
covering Squamish and the elevation tiles.

To sync real activities, create a Strava API application at
https://www.strava.com/settings/api with the "Authorization Callback Domain"
set to `localhost`, then run the server with its credentials:

```sh
$ STRAVA_CLIENT_ID=12345 STRAVA_CLIENT_SECRET=... make run
```

Connect your account under Settings. The first sync fetches all activities,
then the details, photos and detailed streams of the activities from the
year before the latest one, most recent first. Older activities' details
are fetched when you open them or create a report from them. Photos stay on
Strava: only their captions, locations and links are stored, and they're
only shown to you. Strava's API rate limits, by default 100 requests per 15
minutes and 1000 per day, are shared by all users of the app, so a year of
activities can take a day to sync; the sync pauses and resumes
automatically, users take turns so one user's history doesn't hold up the
others' new activities, and the app syncs new activities every few hours.
The activities page checks for new activities every minute while it's
open.

The activities' detailed streams and Strava's raw responses are stored as
files, not in the database: locally in the `activity-data` directory of the
working directory, which is ignored by git, or elsewhere with
`-activity-dir`, or in a Google Cloud Storage bucket with
`-activity-bucket` (`TRAIL_TOOLS_ACTIVITY_BUCKET`).

When the server is reachable from the internet, Strava can also push new,
changed and deleted activities to it as they happen, so they appear within
seconds: set `-strava-webhook-verify-token` to a random secret, and the
server subscribes to Strava's webhook events at `/strava/webhook` when it
starts. Strava allows one subscription per API application, so this replaces
a subscription to another server. Strava can't reach a local server, so leave
it unset locally: new activities arrive with the periodic sync, or when you
sync from Settings.

### Trip reports

Write a trip report from an activity's page. Before publishing, choose a
public handle under Settings: published reports are at
`/u/<handle>/<slug>`, and listed at `/u/<handle>`, for anyone to read
without signing in. Drafts are only visible to you.

Photos are prepared in the browser before they're uploaded: their GPS
position and capture time are read from their EXIF data, and they're
downscaled to a JPEG of at most 2048 pixels, which also converts HEIC photos
in browsers that can display them. A photo without a GPS position is placed
on the activity's track at the time it was taken; otherwise drag it onto the
map. The server re-encodes each photo, dropping its metadata, and stores it
with a thumbnail.

A report starts with the activity's description from Strava as its story.
"Import photos from Strava" adds the photos of the activity on Strava to the
report: the server downloads them and stores them like uploads, placed from
their location or the time they were taken, with their captions, skipping
photos imported before. Publishing a report makes its photos public, so
they're only imported when you ask.

Locally, photos are stored in the `photos` directory of the working
directory, which is ignored by git. Set `-photo-dir` to store them
elsewhere, or `-photo-bucket` (`TRAIL_TOOLS_PHOTO_BUCKET`) to store them in a
Google Cloud Storage bucket with the application default credentials. The
server serves them at `/photos/<id>/<size>.jpg`: photos of published
reports to anyone, with long cache lifetimes, and photos of drafts only to
their author.

### AI agents (MCP)

The MCP server is at `<public URL>/mcp`, e.g. `http://localhost:8080/mcp`
(streamable HTTP). Clients connect with OAuth or an API token.

#### claude.ai, Claude Desktop and other OAuth clients

Custom connectors in claude.ai and Claude Desktop, ChatGPT and other remote MCP
clients sign in with OAuth:

1. In claude.ai, open Settings → Connectors → Add custom connector, and enter
   `https://<your domain>/mcp` as the URL. Leave the OAuth client ID and
   secret empty: clients register themselves.
2. Connect. You're sent to Trail Tools, where you sign in with your passkey
   and approve the connection.
3. The connector is now available in claude.ai, and in the Claude apps signed
   in to the same account.

Connected apps are listed under Settings, where you can disconnect them. Their
access tokens last 30 days and are refreshed automatically; an app that isn't
used for 90 days needs to be connected again.

claude.ai connects from Anthropic's servers, so the server must be reachable
from the internet over HTTPS, and `-public-url` (`TRAIL_TOOLS_PUBLIC_URL`)
must be the URL you enter, as it's the OAuth issuer and resource. The Cloud Run
deployment below sets it.

How it works: `/mcp` answers unauthenticated requests with a
`WWW-Authenticate` header pointing at the protected resource metadata
(`/.well-known/oauth-protected-resource/mcp`), which points at the
authorization server metadata (`/.well-known/oauth-authorization-server`).
Clients register dynamically at `/oauth/register`, without anything being
stored: the registration is encoded in the client ID. Only redirect URIs of
known clients are accepted: loopback addresses, HTTPS on an allowlist
(claude.ai, claude.com, chatgpt.com, vscode.dev) and a few desktop app schemes
(see `internal/oauth`). `/oauth/authorize` requires PKCE (S256) and shows a
consent page, and `/oauth/token` issues opaque access tokens and rotating
refresh tokens, stored hashed.

#### API tokens

Clients that support custom headers can use an API token instead. Create one
under Settings, then for example in Claude Code:

```sh
$ claude mcp add --transport http trail-tools http://localhost:8080/mcp --header "Authorization: Bearer <token>"
```

Without the header, Claude Code connects with OAuth instead.

#### Tools

The MCP tools are generated from the Connect API at startup: every RPC is a
tool with the same name, its input schema comes from the request message and
its description from the proto comments. Methods annotated with
`option (mcp.v1.method).exclude = true` (e.g. API token management) are not
exposed. Service comments are sent to the agent as the server's instructions,
so the proto comments are the agent's documentation; write them for an agent.

## Development

To run the server with auto-rebuild of the backend, frontend and CSS, run:

```sh
$ make watch
```

To run the tests (starts Postgres; each test gets its own database):

```sh
$ make test
```

To view available make targets, run:

```sh
$ make help
```

### Tracing

The server is traced with [OpenTelemetry](https://opentelemetry.io):

- **Requests it serves:** RPCs are named by their procedure, e.g.
  `/trips.v1.TripReportService/GetTripReport`, and other requests by
  their route, e.g. `GET /routes/`. Static assets aren't traced.
- **Database queries:** named by their sqlc query, e.g.
  `ListActivityTracksInBounds`.
- **Requests it makes:** to Strava, Open-Meteo, Nominatim, the elevation
  tiles, Geofabrik, LiveTrack and Google Cloud, named by their host, e.g.
  `GET api.open-meteo.com`.

Traces carry the W3C trace context, so a request's spans form one trace.

Locally, the server sends traces over OTLP to `http://localhost:4317`, and
quietly drops them if nothing is listening. To look at them, start Grafana
with [Tempo](https://grafana.com/oss/tempo/) using the
[`grafana/otel-lgtm`](https://github.com/grafana/docker-otel-lgtm) container,
then run the server as usual:

```sh
$ make grafana
$ make watch
```

`make grafana` runs the container like this, with 4 GB of memory, as it
bundles several services and stalls under the default 1 GB after a while:

```sh
$ container run --rm --name otel-lgtm -d --memory 4g -p 3000:3000 -p 4317:4317 grafana/otel-lgtm:latest
```

Open Grafana on http://localhost:3000; there's no need to sign in.
Locally, every trace is sampled. To find the trace of something you just
did in the app:

- **Drilldown → Traces** in the sidebar breaks traces down by span name,
  with their rate, errors and duration, and links to each trace's
  waterfall.
- **Explore**, with the Tempo data source, searches with
  [TraceQL](https://grafana.com/docs/tempo/latest/traceql/), e.g.:

  ```
  { resource.service.name = "trail-tools" }
  { name = "/geo.v1.GeoService/ListHeatmapTracks" && duration > 500ms }
  { name = "ListActivityTracksInBounds" }
  { name = "GET api.open-meteo.com" }
  { kind = client && status = error }
  ```

  The second query finds slow heatmap requests. Each trace shows the
  queries and outgoing requests of the request.

Requests made with Go's default HTTP client are traced too. Locally they're
also logged as warnings, with where they came from. Give that code a client
from `telemetry.NewClient` instead, as everything else gets one from
`main.go`.

To send traces elsewhere, set the OTLP gRPC endpoint. To turn tracing off,
set it to nothing:

```sh
$ TRAIL_TOOLS_OTLP_ENDPOINT=http://collector.example:4317 make run
$ TRAIL_TOOLS_OTLP_ENDPOINT= make run
```

`make clean` stops Grafana along with Postgres.

### Adding an RPC

1. Add the RPC to the service in `api/<domain>/v1/*.proto`, with comments
   describing it for both humans and agents.
2. Run `make gen`. This generates the Go and TypeScript code and the embedded
   descriptor set used for the MCP tool descriptions.
3. Implement the method in `internal/services/<domain>`. It's automatically
   exposed over MCP.

### Routing data

Routes are routed in Postgres with [PostGIS](https://postgis.net) and
[pgRouting](https://pgrouting.org), on the foot accessible ways of
OpenStreetMap data imported into the database. `make db` builds a Postgres
image with both extensions.

Administrators manage the imported areas on the map data page, at
`/map-data` or under the user menu. It shows each area with the space it
takes and the size of the database, deletes areas to free space, and imports
the area shown in the middle of the map. The server picks the
[Geofabrik](https://download.geofabrik.de) extracts to read from their
outlines in Geofabrik's index: at points across the area, the smallest
extract containing each, so an area across a border reads the extracts on
both sides, up to 4. It reads only the paths and points of interest (peaks,
water, parking, viewpoints, shelters, toilets, saddles and trailheads) inside
the area, and looks up elevations from the SRTM derived tiles of the
[AWS Terrain Tiles](https://registry.opendata.aws/terrain-tiles/) dataset.
Locally, imports run in the server. On Cloud Run they run as executions of the
`trail-tools-import` job, set with `-import-job`, as they need more memory than
the service has.

Finding trails and points of interest, in the API and the map's trails layer,
uses the same imported data, with PostGIS queries. They only work in imported
areas, like routing, and report that the area needs importing elsewhere.
Regions imported before trails were found this way have no trail names or
points of interest until they are imported again.

Imports can also be run from the command line, e.g. to import a whole
extract:

```sh
$ make import-osm REGION=liechtenstein PBF=https://download.geofabrik.de/europe/liechtenstein-latest.osm.pbf
```

Use `BOUNDS=south,west,north,east` to only import part of an extract, and
separate the extracts of neighboring countries with commas in `PBF` to import
an area across a border. Importing a region again replaces it.

Storage grows with the area and density of paths: all of Switzerland takes
about 715 MB, a 100 by 100 km box of it about 230 MB and Liechtenstein about
4.5 MB, plus about 15% for trail names and points of interest. Neon's free
plan has 0.5 GB of storage. Memory grows with the area
imported, as only the paths inside it are kept: a 100 km² area of the
Switzerland extract peaks at under 400 MB, plus the largest downloaded
extract, which Cloud Run keeps in memory.

### Map data services

Place search ([Nominatim](https://nominatim.org)) and weather
([Open-Meteo](https://open-meteo.com)) come from public services that are
fine for personal use. Trails and points of interest come from the imported
OpenStreetMap data (see [Routing data](#routing-data)). Elevations, e.g. of
straight legs and routes from GPX files, come from the same elevation tiles
as imports, downloaded as needed and cached, so they don't depend on a rate
limited API.

Elevation tiles are cached in a directory under the system's temporary
directory. Set `-dem-bucket` (`TRAIL_TOOLS_DEM_BUCKET`) to cache them in a
Google Cloud Storage bucket with the application default credentials
instead, for servers whose disk doesn't outlive them, like Cloud Run, where
the disk is also memory, so a cache directory would grow until the instance
runs out of it. Tiles are read from the bucket before downloading them,
downloaded tiles are added to it, and tiles that don't exist, e.g. over the
sea, are recorded in it as empty objects. If the bucket can't be read or
written, the tiles are downloaded instead. Either way, the few most recently
used tiles are kept in memory. The `import-osm` command takes the same flag.

## Deploying

Trail Tools runs on [Cloud Run](https://cloud.google.com/run) with a
[Neon](https://neon.com) serverless Postgres database, sized to stay within
the free tiers for personal use: Cloud Run scales to zero, runs at most one
instance and only uses CPU during requests, and Neon suspends the database
when it's idle.

The Google Cloud configuration is managed with Terraform in
[`infra/`](infra), with its state in a Cloud Storage bucket. Pushes to `main`
are deployed by the `deploy` job in [CI](.github/workflows/ci.yml) once all
checks pass: it builds the image with [ko](https://ko.build) and runs
`terraform apply` with it.

GitHub Actions authenticates to Google Cloud with Workload Identity
Federation, so there are no stored credentials: pushes to `main` as the
`github-terraform` service account, which can apply changes, and pull
requests as the read-only `github-terraform-plan` service account.

Pull requests that change `infra/` get a comment with the Terraform plan,
from the [Terraform plan](.github/workflows/terraform.yml) workflow. It
doesn't lock the state.

To apply changes locally, with your gcloud credentials:

```sh
$ make infra-apply
```

`make infra-plan` only plans them. Both use the Terraform version CI applies
with, the `terraform` stage in [`images/Dockerfile`](images/Dockerfile),
which Dependabot updates, and download it into `.bin` the first time:
Terraform refuses to read state written by a newer version, so a local
apply with another version than CI's breaks one or the other.

### Traces and errors

On Cloud Run, the server sends traces with OTLP to Google Cloud's Telemetry
API (`telemetry.googleapis.com`), to view in
[Cloud Trace](https://console.cloud.google.com/traces). Cloud Trace is free
for the first 2.5 million spans a month, which the app stays well within, so
every trace is sampled. Set `trace_sample_rate` in Terraform to sample fewer.

Errors are reported to
[Error Reporting](https://console.cloud.google.com/errors), which groups
them by where they happened and can email about new ones. It's free.

There are no custom metrics, as they're billed by the sample. Cloud Run's
own metrics show the requests, latencies and instances.

### Secrets

Secrets are created by Terraform, but their values are added by hand so they
aren't stored in the Terraform state:

- `database-url`: the direct (not pooled) Neon connection string, as
  migrations need session advisory locks.

  ```sh
  $ pbpaste | gcloud secrets versions add database-url --data-file=- --project trail-tools-444904
  ```

- `admin-bootstrap-token`: works as an invitation to create the first
  administrator at `/invite#<token>`, until there is one.

  ```sh
  $ openssl rand -hex 24 | gcloud secrets versions add admin-bootstrap-token --data-file=- --project trail-tools-444904
  $ gcloud secrets versions access latest --secret admin-bootstrap-token --project trail-tools-444904
  ```

  Passkeys are bound to the domain, so create the administrator on the
  domain you'll use.

- `strava-client-secret`: the Strava API application's client secret. Also
  set `strava_client_id` in [`terraform.tfvars`](infra/terraform.tfvars),
  which enables Strava and an hourly Cloud Scheduler job that syncs
  activities, and set the Strava app's callback domain. Cloud Run only runs
  the server during requests, so syncs run within the job's requests: when
  you sync from Settings, the service runs the job right away (`-strava-sync-job`),
  and the hourly runs continue syncs that stop at Strava's rate limit.
- `strava-webhook-verify-token`: a random secret Strava sends back when the
  server subscribes to its webhook events, at startup. Strava then sends new,
  changed and deleted activities to `/strava/webhook`, and each is synced
  within the request, as Cloud Run only runs the server during requests.
  Strava expects an answer within two seconds, so a sync that takes longer is
  left to the hourly sync. The service needs it when Strava is enabled, so
  add it with the client secret:

  ```sh
  $ openssl rand -hex 24 | gcloud secrets versions add strava-webhook-verify-token --data-file=- --project trail-tools-444904
  ```

### Photos

Trip report photos are stored in the `<project>-trip-photos` Cloud Storage
bucket, as the file system of a Cloud Run instance doesn't outlive it. The
bucket is in `us-west1`, within Cloud Storage's free tier, with uniform
bucket-level access and public access prevention: the app serves the photos
itself, so only the Cloud Run service account can read and write them
(`roles/storage.objectUser`). Terraform passes the bucket to the service as
`TRAIL_TOOLS_PHOTO_BUCKET`, and the server authenticates with the service
account's application default credentials. Photos of published reports
are served with long cache lifetimes, so they're mostly fetched once per
visitor.

### Activity data

Activities' streams and Strava's raw responses about them are stored in the
`<project>-activity-data` Cloud Storage bucket, where they cost much less
than in the database: they're written once, read whole and never queried.
It's set up like the photos bucket, private and in `us-west1`, and Terraform
passes it to the service as `TRAIL_TOOLS_ACTIVITY_BUCKET`. Streams are
stored compactly, at Strava's precision, in about a seventh of the space of
the API's message.

### Elevation tiles

Elevation tiles are cached in the `<project>-dem-tiles` Cloud Storage
bucket, so they're downloaded from AWS once rather than on every cold start
of the service and every import. It's set up like the photos bucket, private
and in `us-west1`, and Terraform passes it to the service and the import job
as `TRAIL_TOOLS_DEM_BUCKET`. Their service accounts can read and write its
objects (`roles/storage.objectUser`). The import job has its own service
account, which can only read the database URL among the secrets. Tiles are
stored gzipped as downloaded, at the same paths as on AWS, e.g.
`skadi/N46/N46E007.hgt.gz`.

### Custom domain

The app is served on `trailrunn.ing`, set as `domain` in
[`terraform.tfvars`](infra/terraform.tfvars). To set up a domain:

1. Verify the domain with `gcloud domains verify <domain>`.
2. Set `domain_enabled = true` in [`terraform.tfvars`](infra/terraform.tfvars)
   and apply it locally with `make infra-apply`. Only a verified
   owner of the domain can map it, and the GitHub service account is not one,
   so the first apply runs with your own credentials. Later applies from CI
   leave the existing mapping alone.
3. Create the DNS records from `make infra-output NAME=domain_records`.
   Cloud Run provisions and renews the TLS certificate, which takes up to
   about 15 minutes after the records resolve.

### Bootstrapping a new project

The state bucket and the billing permission are created by hand once:

```sh
$ gcloud storage buckets create gs://<project>-tfstate --location us-west1 --uniform-bucket-level-access --public-access-prevention
$ gcloud storage buckets update gs://<project>-tfstate --versioning
```

Then update the bucket in [`versions.tf`](infra/versions.tf) and the
variables in [`terraform.tfvars`](infra/terraform.tfvars), apply locally, and
let the Terraform service accounts manage and read the budget:

```sh
$ gcloud billing accounts add-iam-policy-binding <billing account> --member serviceAccount:github-terraform@<project>.iam.gserviceaccount.com --role roles/billing.costsManager
$ gcloud billing accounts add-iam-policy-binding <billing account> --member serviceAccount:github-terraform-plan@<project>.iam.gserviceaccount.com --role roles/billing.viewer
```
