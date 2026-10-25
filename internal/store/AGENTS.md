# Store and migrations

`store.New` connects to Postgres, applies the migrations in `migrations/` on
every start (the import job's too), and registers the enum types. Production
uses Neon's direct connection, as migrations take advisory locks that its pooler
doesn't support.

## Migrations

- Add a new file for each change, named with the next number:
  `NNN_<what_it_does>.up.sql`. There are no down migrations, and applied
  migrations never change.
- Explain tables, columns and non-obvious changes in `--` comments.
- Write lowercase SQL, indented by two spaces.
- After changing the schema, run `make sqlc` to regenerate the queries.
- A migration must run in a single transaction, so a failed one leaves
  nothing behind: no `commit` before the end of the file, and no `create index
  concurrently` or `vacuum` (`TestMigrationsAreAtomic` checks). golang-migrate
  marks a failed migration's version dirty; `store.New` sets it back to the
  previous version and retries it, so fixing the migration and deploying
  again is enough.
- Keep the previous revision working with the new schema: Cloud Run serves
  with it while the new one starts, and rolling back to it runs it without
  migrating (`store.New` skips migrating a database newer than its
  migrations). Add columns and tables in one release and stop using old
  ones before dropping them in a later one, and give new `not null` columns
  defaults.
- Migrations wait at most 10 seconds for a lock, and a migration runs for at
  most 2 minutes, as a migration waiting for a lock would hold up every query
  after it. Starting retries a few times when waiting for a lock times out.
  Long backfills belong in the server or a job, in batches.

## Tables

- IDs are `id uuid primary key default gen_random_uuid()`.
- Record times as `create_time timestamptz not null default
  current_timestamp` and `update_time` the same. Queries set `update_time =
  now()`; there are no triggers.
- Use `text not null` with `''` for unset text, as in the API. Leave optional
  references and values nullable.
- Calendar dates are `date`. Quantities have the unit in the name, like the
  API: `distance_meters double precision`, `cutoff_seconds integer`.
- A user's data references `users(id) on delete cascade`, so deleting a user
  deletes their data. Optional references use `on delete set null`.
- Enums are `create type <name> as enum ('lowercase_value', ...)`. An API
  enum's `UNSPECIFIED` is `null` in the column. To change an enum's values,
  rename the old type, create the new one, convert the columns with `alter
  column ... using (case ...)`, then drop the old type.
- State invariants as `check` constraints, and use composite foreign keys when
  a reference must belong to the same parent.
- Name indexes `<table>_<columns>`. Geometry is `geometry(<Type>, 4326)` with a
  GiST index.

## Tests

The test database image in `test/postgres` has PostGIS and pgRouting.
`storetest.New(t)` gives each test its own migrated database. Use
`storetest.New`, not the dev database, in tests.
