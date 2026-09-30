# Worm demo application — plan

A public, always-on demo of Worm: a seeded Postgres source with continuous
simulated traffic, migrated live into a SQLite target via the real
`migrate-schema` → `migrate-data` → `migrate-resume` pipeline, with a web
UI to watch it and (later) cause writes directly.

## Hosting

Oracle Cloud, Ampere A1 (ARM64) — **1 OCPU / 6GB**, the actual free-tier
allowance available (not the theoretical 4 OCPU/24GB pool — that requires
a limit increase Oracle auto-declines on newer accounts). Confirmed
sufficient for this workload: nothing here is compute-bound at steady
state, only backfill (concurrent-by-design, still to build in worm itself)
produces a real but brief single-core spike. Add swap as cheap insurance.
Ubuntu ARM64. `postgres:16-alpine` (multi-arch), Caddy (multi-arch), and
`mattn/go-sqlite3` (CGO, but built natively on the ARM box - no
cross-compile needed) all confirmed fine on this arch.

Postgres config: `wal_level=logical` (required for the replication slot)
and `synchronous_commit=off` (safe here specifically - data dir is
`tmpfs`, so there was never a durability guarantee to begin with; this
cuts per-write latency and is most of what makes the higher traffic
numbers below comfortable on one core). Both set in `docker-compose.yaml`.

## Repo layout (this repo)

```
docker-compose.yaml       postgres: wal_level=logical, synchronous_commit=off, tmpfs
Makefile                   up / down / seed / role
infra/
  systemd/                 worm-provision, worm-resume, worm-demo-backend units + run-stream.sh
  cron/                     normalize.cron - hourly, runs on the machine's own cron, not in-process
server/                   Go module
  main.go                  wires dial service, traffic generator, HTTP + middleware
  cmd/
    seed/                   one-shot: wipe + apply schema.sql + concurrent seed
    normalize/               one-shot: single normalizer pass, invoked by infra/cron
  internal/
    dbconn/                  opens the one restricted-role connection
    dial/                    shared traffic-dial state (req/sec directly) + idle decay + HTTP handlers
    traffic/                 rate-limited continuous write generator
    normalizer/               per-table baseline correction (logic only - cmd/normalize drives it)
    seeddata/                 the 20 SeedX(ctx, db, n) funcs, shared by cmd/seed and normalizer
    middleware/               CORS + rate limiting (net/http only, no framework)
  sql/restricted_role.sql    demo_writer role + grants
web/                       frontend (Vercel)
```

`internal/wormproc`, `internal/reset`, and `internal/cron` from earlier
iterations are gone - deleted, not just unused. See "process supervision"
below for what replaced them.

## Traffic simulation

`internal/traffic` - its own continuous worker pool, not a scheduled task.
A fixed-interval batch job is the wrong shape for "N req/sec, smoothly,
precisely tunable by a live dial." Instead: a pool of 6 worker goroutines
sharing one `golang.org/x/time/rate.Limiter`, each doing one
INSERT/UPDATE/DELETE per limiter tick (insert task, update task status,
insert comment, delete oldest comment - real mix, not insert-only).

The dial's own units are requests/sec directly now (`Baseline=100`,
`Max=600` in `internal/dial`) - originally an abstract 0-100 scale
separately interpolated into a rate range, collapsed once the UI-facing
number and the enforced number needed to be the same thing. `syncRate`
just copies `dial.Get()` into the limiter every second, no mapping step.

Random FK-parent picks use an in-memory id cache (200 ids per table,
refreshed every 5s), not a fresh `ORDER BY random() LIMIT 1` query per
write - at up to 600 writes/sec, that would itself become real load.

Numbers reasoned through explicitly with the 1 OCPU box in mind: 100
baseline is trivial; 600 max is real but achievable specifically *because*
it's spread across a small worker pool (round-trip-latency-bound
otherwise) and streaming stays single-connection throughout, so there's no
sustained multi-connection contention outside the pool itself. 1000/sec
was considered and deliberately not shipped - no real data on headroom
once Caddy/backend/worm's own process share the core, worth trying
empirically later rather than promising now.

## Traffic dialer HTTP surface - done

`internal/dial/handlers.go`: `GET /api/traffic/dial` (current state),
`POST /api/traffic/dial` (set). POST validates before applying, doesn't
silently clamp: negative → `400`; over `Max` → **`404`**, body
`"unfortunately our hardware doesn't support safely going above this
limit"` (deliberately non-standard status for this - a validation failure
would more conventionally be 400/409/422, chose 404 anyway).

`internal/middleware` (new, plain `net/http`, no framework):
- `CORS(allowedOrigins map[string]bool)` - exact Origin match, editable
  map in `main.go`.
- `RateLimit(ctx, cfg)` - three layered guards, not one:
  - per-IP `rate.Limiter` (identification is just an IP, not a real
    identity - the honest limit of what's available without visitor
    accounts)
  - a **global** `rate.Limiter` shared across all visitors, specifically
    because per-IP alone doesn't hold up against someone cycling IPs to
    dodge it
  - a semaphore capping max concurrent in-flight requests - a different
    axis (concurrency, not rate) from the two above, complementary rather
    than redundant
  - plus an IP allowlist that bypasses all of it (dev/admin), and a
    periodic sweep evicting per-IP entries not seen in 30m so the map
    doesn't grow forever on a long-running public demo.

Client IP resolution trusts `X-Forwarded-For` over `RemoteAddr`, correct
specifically because Caddy sits in front of this in the real deployment -
would need revisiting if this ever runs with nothing in front of it.

## Process supervision (replaces wormproc)

`worm-resume.service` (systemd, `Restart=always`) runs
`infra/systemd/run-stream.sh`, not `worm migrate-resume` directly. The
problem it solves: `Restart=always` always re-runs the *same* `ExecStart`,
but the first-ever run needs `migrate-data` (backfill) and every run after
that needs `migrate-resume` - exactly what the deleted `wormproc.Supervisor`
existed to decide in Go. The script does the same job with no state
inspection needed: try `migrate-resume` first; if its stage claim fails
(fresh state), it exits non-zero and the script falls through to
`migrate-data`; once resume ever succeeds it just runs (forever, until it
crashes or is stopped), and any restart re-runs this same logic fresh.
`worm-provision.service` is the one-time schema step (`migrate-schema`
only - `migrate-data` can't be a oneshot unit, it never returns).

## Why we dropped the scheduled full reset

Original plan had a 2-hour cron doing stop-stream → `migrate-reset` (drops
the replication slot + `worm_pub`, wipes target, clears worm's own state
db - all real, all still built and working as a CLI command in the main
Worm repo) → `migrate-schema` → restart `migrate-data`. Reconsidered and
dropped the *schedule* entirely, not just paused it:

- Row growth is already handled by the normalizer (below) - doesn't need
  a full reset.
- The slot/WAL bloat problem the reset was partly justified by is
  self-inflicted by doing repeated resets in the first place - one
  continuously-consumed slot doesn't bloat.
- It never actually reset the **source** data (only target + worm's own
  state), so it wasn't providing the "known clean baseline" guarantee it
  was sold as.
- The SQL editor abuse case it was meant to defend against doesn't exist
  yet - premature to keep paying the cost now.

`migrate-reset` itself is unaffected (still in the main Worm repo, still a
real command) - only the demo-side orchestration around it
(`wormproc`/`reset` packages) was removed, since nothing calls it anymore.

## Normalization (replaces the reset cron)

Moved out of the Go backend entirely - it doesn't share any in-process
state (unlike the dial/traffic generator, which need live access to
`dial.Service`'s in-memory value), so there's no reason for it to live in
the same long-running process. `server/cmd/normalize` runs one pass and
exits; `infra/cron/normalize.cron` invokes it hourly via the machine's own
cron. Isolation benefit: a bug here can't take the HTTP server down with
it, and it's independently testable (`go run ./cmd/normalize`) without
booting anything else.

Logic (`internal/normalizer`), unchanged: for each of the 20 tables, over
its seeded baseline → delete the oldest excess (`DELETE ... WHERE ctid IN
(... ORDER BY <col> LIMIT n)` - ctid works uniformly for single-PK and
composite-PK junction tables); under baseline → top up via the matching
`seeddata.SeedX`. All 20 run concurrently (`errgroup`) within one pass -
no dependency ordering needed since every parent already exists from the
initial seed. Pure DML through `demo_writer`, never touches the
slot/publication/stream. This is also what will absorb SQL-editor-caused
drift later, same mechanism, no special-casing needed.

Known imprecision, accepted not engineered around: a parent-table delete
cascades to children, so a child table's count can drift slightly within
the same run if it raced a concurrent parent delete. Self-corrects next
hour.

## Seed data (`cmd/seed`, one-time/manual, not a schedule)

20 tables (up from 10), grouped into 5 FK-dependency levels (see
`schema.sql` comments), seeded with real concurrency: each level's tables
run in parallel via `errgroup` (same pattern as `inspect/postgresql.go` in
the main Worm repo), a barrier between levels since the next level's FKs
need the previous one's rows. New tables beyond the original 10:
`roles`, `webhooks`, `webhook_deliveries`, `integrations`, `api_keys`,
`user_roles`, `milestones`, `task_dependencies`, `notifications`,
`time_entries` - plus a self-referential FK on `tasks`
(`parent_task_id`, always NULL at seed/normalize time; only real ongoing
activity creates actual subtasks). ~57,000 rows total across all 20
tables at initial seed.

Correctness note worth keeping in mind if this pattern gets copied
elsewhere: `internal/seeddata.RandomPools` is the one piece doing real
work here - materializes each parent's ids into an array once via a CTE,
then indexes into it with `random()` called directly in the target list.
An uncorrelated `ORDER BY random() LIMIT 1` subquery (or even a LATERAL
one that doesn't actually reference the outer row) can get planned as a
single InitPlan and evaluated once for the whole statement - this bit the
very first version of the main repo's `scripts/seed_data.sql` (every
`team_members` row got the same "random" pair). `task_dependencies` needed
one more layer of care on top of that: it picks two ids from the *same*
pool and has to compare them (no self-dependencies) - referencing the pick
expression twice (once to select it, once to compare it) would re-roll a
different random value the second time, so it computes both picks once in
an inner subquery, names them, and compares the named columns instead.

## Infra - done

`docker-compose.yaml` at repo root (postgres only - `worm`/backend/
normalize run as native processes via systemd/cron, not containers).
`Makefile`: `up` (bring up postgres, wait healthy), `down`, `seed` (runs
`cmd/seed` against the superuser connection - needs DDL rights, never
`demo_writer`), `role` (applies `sql/restricted_role.sql`, needs a real
password set first). `infra/systemd/` has the three unit files (see
"process supervision" above), paths inside them are placeholders
(`/opt/worm-demo/...`) marked to adjust for the real deploy layout.
`infra/cron/normalize.cron` is the crontab line for the normalizer.

Not done: actually provisioning the VM and running any of this for real -
these files exist and build/`go vet` clean, but haven't been deployed or
smoke-tested against a live Oracle instance yet.

## SQL editor (not built yet - still the plan, unchanged)

Visitor-submitted SQL, restricted to single-row INSERT / WHERE-scoped
UPDATE / WHERE-scoped DELETE, executed through the same `demo_writer` role
everything else already uses. Guardrails, layered, none of this built yet:

1. Real parse via `pganalyze/pg_query_go`, not string/regex matching.
2. Exactly one top-level statement (`InsertStmt`/`UpdateStmt`/`DeleteStmt`).
3. Table allowlist checked across the whole AST (CTEs, `USING`,
   `RETURNING`) - `capture_*` tables must never be reachable.
4. Narrow allowed shapes: single-row `VALUES` only for INSERT, no
   `INSERT ... SELECT`; single-table, no-join, `WHERE`-required for
   UPDATE/DELETE, and the predicate must be checked structurally (reject
   `WHERE true`/`WHERE 1=1`-shaped conditions), not by string-matching for
   the literal word `WHERE`.
5. DB-level enforcement independent of the app - `demo_writer`'s grants
   are the real backstop if the AST check ever has a bug.
6. Per-statement `SET LOCAL statement_timeout`/`lock_timeout`.
7. Separate connection pool from the traffic generator/normalizer.
8. Rate limiting - the `internal/middleware` package built for the dial
   endpoint is directly reusable here, same per-IP/global/semaphore shape.

## Next up

Backend-side, what's actually left is the SQL editor's `sqlguard` (the AST
allowlist above) - everything else backend-side (traffic sim, dialer +
its guards, seed, normalize, process supervision, infra files) is built.

Otherwise: the SSE event plumbing for the live dashboard, the frontend
itself, and deploying/smoke-testing the infra above against a real Oracle
instance for the first time.
