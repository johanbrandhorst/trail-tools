# Connect services

Each package here implements one or more services of `api/`, in a `Service`
struct built with `NewService`, and asserts it with
`var _ racesv1connect.RaceServiceHandler = (*Service)(nil)`. `main.go`
registers it on the Connect server, and on the MCP server if agents should
have it.

## Handlers

- Use the simple signature, `func (s *Service) GetRace(ctx context.Context,
  req *racesv1.GetRaceRequest) (*racesv1.GetRaceResponse, error)`. The MCP
  server relies on it.
- The protos use the opaque API: read requests with getters
  (`req.GetId()`) and build messages with builders
  (`racesv1.GetRaceResponse_builder{...}.Build()`).
- Handlers are documented in the proto, so they don't need doc comments.
- Start with authentication: `userID, ok := authn.GetUserID(ctx)`, returning
  `connect.NewError(connect.CodeUnauthenticated, "")` if not. The middleware
  only identifies the user; each handler enforces it. Check administrators
  with `user.IsAdmin()`, returning `CodePermissionDenied`.
- Handlers that manage the account's credentials (passkeys, invitations, API
  tokens, OAuth connections) start with `user, err :=
  authn.GetSessionUser(ctx)` instead, returning `err`. It only accepts the web
  app's session cookie, as a bearer token held by an API client or AI agent
  could otherwise sign in as the user or create more tokens. Exclude them from
  MCP too.
- Pass the user's ID to the repository, which scopes its queries by it.

## Validation

- Validate requests in Go while converting them, in `xFromProto` functions,
  and return `connect.Errorf(connect.CodeInvalidArgument, ...)`.
- Messages are lowercase and name the proto field in snake_case: `"start_time
  must be HH:MM"`. Parse IDs with `store.StringToUUID`, returning `"invalid
  race ID: %v"` with `.WithCause(err)`.
- Trim strings with `strings.TrimSpace` before checking that they're set.
- Apply update masks by converting the stored resource to proto, applying the
  mask with `protomask.Apply`, keeping the fields the server owns (`"id",
  "create_time", "update_time"`), and validating the result like a new
  resource.

## Errors

- Map errors with the service's `toConnectError(ctx, err, msg)`:
  - pass `*connect.Error`s through;
  - `store.ErrNotFound` becomes `CodeNotFound`;
  - known domain errors become `InvalidArgument`, `AlreadyExists` or
    `FailedPrecondition` with a message for users;
  - anything else is reported to Error Reporting and becomes `CodeInternal`
    with `.WithCause(err)`.
- Connect error messages reach users and AI agents, so say what to do: "no
  map data has been imported for this area; import it on the map data page
  first".

## Other HTTP handlers

Handlers outside Connect, like downloads and photos, are methods returning an
`http.Handler` (`DownloadHandler`, `PhotoHandler`), with their paths in
exported constants. They respond with `http.Error` and lowercase messages, and
report internal errors.

## Tests

The integration tests in this directory (`package services_test`) run the
real services and repositories behind the real authentication, over HTTP:

- `newEnv(t)` in `services_test.go` gives typed Connect clients for a user
  (`e.races`), a second user (`e.otherRaces`) and an anonymous one, plus fakes
  for map data, routing, Strava and LiveTrack.
- Write a test as a scenario through the API, with comments for each step.
  Check that other users can't see or change the data.
- Check error codes with `wantCode(t, err, connect.CodeNotFound)`.
- Fakes keep their state behind a `sync.Mutex`, and their doc comments say how
  they behave.
