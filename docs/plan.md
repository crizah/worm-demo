# Worm demo application — plan

Moved here from the main Worm repo (`docs/demo-app-plan.md`) and updated to
match what's actually been built. A public, always-on demo of Worm: a
seeded Postgres source with continuous simulated traffic, migrated live
into a SQLite target via the real `migrate-schema` → `migrate-data` →
`migrate-resume` pipeline, with a web UI to watch it and (later) cause
writes directly.

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
numbers below comfortable on one core).

## Repo layout (this repo)

```
server/                  Go module
  main.go                 wires dial service, traffic generator, normalizer, HTTP
  cmd/seed/                one-shot: wipe + apply schema.sql + concurrent seed
  internal/
    cron/                  generic interval task runner (+ RunImmediately)
    dbconn/                 opens the one restricted-role connection
    dial/                   shared traffic-dial state + idle decay + HTTP handlers
    traffic/                rate-limited continuous write generator
    normalizer/              hourly per-table baseline correction
    seeddata/                the 20 SeedX(ctx, db, n) funcs, shared by cmd/seed and normalizer
    reset/, wormproc/        migrate-reset orchestration - built, NOT auto-scheduled (see below)
  sql/restricted_role.sql   demo_writer role + grants
web/                      frontend (Vercel)
```

Worm itself (`migrate-schema`/`migrate-data`/`migrate-resume`) runs as a
plain subprocess the backend can start/stop/supervise via `wormproc`, not
as its own separate systemd unit - see "why we dropped the reset cron"
below for why that mattered.

## Traffic simulation

`internal/traffic` - **not** a `cron.Scheduler` task. A fixed-interval
batch job is the wrong shape for "N req/sec, smoothly, precisely tunable
by a live dial." Instead: a pool of 6 worker goroutines sharing one
`golang.org/x/time/rate.Limiter`, each doing one INSERT/UPDATE/DELETE per
limiter tick (insert task, update task status, insert comment, delete
oldest comment - real mix, not insert-only).

Rate mapping: linear interpolation anchored at `(dial.Baseline=10 →
100/sec)` and `(dial.Max=100 → 600/sec)`, re-applied to the limiter every
second. **The 600/sec ceiling is already enforced two ways**: the dial's
own `Set()` clamps any requested value into `[0, Max=100]` regardless of
what's posted, and `targetRate()`'s anchor point means even `dial=100`
can't produce more than 600/sec - this isn't something still to build, the
guard already exists in `internal/dial` + `internal/traffic`.

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

## Why we dropped the scheduled full reset

Original plan had a 2-hour cron doing stop-stream → `migrate-reset` (drops
the replication slot + `worm_pub`, wipes target, clears worm's own state
db - all real, all built and working in the main Worm repo) →
`migrate-schema` → restart `migrate-data`. Reconsidered and dropped the
*schedule*, kept the *tool*:

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

`migrate-reset`/`wormproc`/`reset` packages are still there and working -
kept as a manual/emergency tool, just not wired into the scheduler.

## Normalization (replaces the reset cron)

`internal/normalizer`, hourly, `RunImmediately: true`. For each of the 20
tables: over its seeded baseline → delete the oldest excess (`DELETE ...
WHERE ctid IN (... ORDER BY <col> LIMIT n)` - ctid works uniformly for
both single-PK and composite-PK junction tables); under baseline → top up
via the matching `seeddata.SeedX`. All 20 tables run concurrently
(`errgroup`) - no dependency ordering needed here since every parent
already exists from the initial seed. Pure DML through `demo_writer`,
never touches the slot/publication/stream, so the pipeline just keeps
running underneath it. This is also what will absorb SQL-editor-caused
drift later, same mechanism, no special-casing needed - that was the
whole point of keeping it DML-only.

Known imprecision, accepted not engineered around: a parent-table delete
cascades to children, so a child table's count can drift slightly within
the same tick if it raced a concurrent parent delete. Self-corrects next
hour.

## Seed data (`cmd/seed`, one-time/manual now, not a schedule)

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
8. Rate limit per visitor (IP/session token).

## Next up

**Traffic dialer's remaining backend surface** - `internal/dial`'s HTTP
handlers and the 600/sec ceiling already exist and are wired into
`main.go`; what's still open:
- Rate limiting on `POST /api/traffic/dial` itself (marked as a TODO in
  `handlers.go` - someone scripting rapid POSTs bypasses the frontend
  entirely and this isn't guarded yet).
- CORS for the Vercel frontend origin.
- Basic visitor identification (IP or a session token issued on page
  load) - whatever the rate limiter above keys off of.

After that: the SSE event plumbing for the live dashboard, then the SQL
editor (`sqlguard` + the AST allowlist), in that order - matches the
build-order reasoning from the original plan (data layer before graphics,
highest-risk piece last and tested in isolation).
