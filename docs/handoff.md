# Handoff

Written 2026-10-02 for whoever (human or agent) picks this up next. See
`docs/plan.md` for the full project history/rationale - this doc is just
"what's true right now and what to do next," not a replacement for it.

## TL;DR

- Full pipeline (postgres seed → `worm migrate-schema` → `migrate-data` →
  `migrate-resume` streaming) has been run live on a real AWS box and
  confirmed working, including under load.
- Two real bugs found and fixed in the **Worm repo** (not this one) along
  the way - both already committed there. Details below.
- A frontend (`web/`) exists, vanilla HTML/CSS/JS, confirmed working.
- A second pass added a lot more to the frontend (pipeline status panel,
  x/y-axis chart, row-comparison "proof of sync" panel, SQL editor shell)
  plus matching backend endpoints. The user didn't like it and asked for a
  revert. **That revert has been done** - `web/` and `server/main.go` are
  back to the confirmed-working state. The backend code for the reverted
  pass is gone too (see "What got reverted" below if you want to see what
  was tried).
- **Nothing in this repo is committed yet.** `git status` in `worm-demo`
  will show the working frontend + a couple of unrelated small fixes as
  uncommitted changes. Commit deliberately, don't `git add -A` blindly -
  see "Repo state" below for exactly what's there.

## Repo state right now

`worm-demo` (`git status --short`):
```
 M Makefile                         # adds `make fe` (serves web/, see below)
 M server/cmd/normalize/main.go     # adds godotenv.Load() so .env works for this binary too
 M server/main.go                   # wires GET /api/stats/stream
 M server/sql/restricted_role.sql   # grants all 20 demo tables, not just the original 10
?? server/demo                      # compiled binary, gitignore it, don't commit
?? server/internal/stats/           # new package backing /api/stats/stream
?? server/normalize                 # compiled binary, gitignore it, don't commit
?? web/                             # the frontend
```
`Worm` (separate repo, already clean/committed):
```
d040788 make sqlite not kill us       # WAL + synchronous=NORMAL pragma fix
1bc6fbe fix sqlite update syntax      # batched UPDATE...FROM syntax fix
```

## What's actually been verified live (on the AWS box)

- Provisioned via Terraform (`infra/oci/terraform/` - despite the dir
  name, this is now AWS/EC2, not OCI; the OCI attempt got abandoned after
  repeated "out of host capacity" errors on the free Ampere tier).
  **Correction needed**: that directory should probably be renamed or a
  new `infra/aws/` added - nobody's done that cleanup yet.
- `make up && make seed && make role` on a t4g.small (2 vCPU/2GB).
- `worm migrate-schema` → `migrate-data` → backfill completes → streaming
  starts.
- Traffic generator (`demo-backend`) dialed up to 600 req/s and confirmed
  actually producing that load (verified via `top`/load average, not just
  trusting the dial's reported value).
- **Bug #1 (found + fixed, Worm repo, commit `1bc6fbe`)**: SQLite target
  writer (`data-writer/sqlite.go`) built `UPDATE ... FROM (VALUES ...) AS
  a(col1, col2) WHERE ...` - valid postgres, but sqlite doesn't support
  naming a VALUES-subquery's columns that way. First real `UPDATE` ever
  replicated (not backfill - backfill is pure `INSERT`) hit this and
  crashed `migrate-resume` with `near "(": syntax error`. Fixed by
  referencing sqlite's own implicit `column1`/`column2`/... names instead.
  Verified both via a standalone repro and by rerunning the real pipeline.
- **Bug #2 (found + fixed, Worm repo, commit `d040788`)**: `utils.PingDB`
  opened sqlite with zero pragmas, so every single replicated row did its
  own fsync'd commit (`synchronous=FULL` default). This capped real
  throughput at ~100/sec on the AWS box's EBS volume regardless of what
  the traffic dial was set to - confirmed by watching `top` show the box
  CPU-bound while attempting 600/sec but still only committing ~100/sec,
  then confirming `pg_current_wal_lsn()` vs the replication slot's
  `confirmed_flush_lsn` to rule out a stall. Fixed with `PRAGMA
  journal_mode=WAL` + `PRAGMA synchronous=NORMAL` on every sqlite
  connection `PingDB` opens (state db included). Local benchmark: ~780
  commits/sec default → ~26,500/sec with the fix (~34x). Confirmed live:
  after the fix, a fresh `migrate-resume` caught up a large WAL backlog at
  1300+/sec before settling to track the live dial rate.
- Grant gap: `restricted_role.sql` was only granting the original 10
  tables from an earlier schema version; the seed schema grew to 20
  tables (`roles`, `webhooks`, `webhook_deliveries`, `integrations`,
  `api_keys`, `user_roles`, `milestones`, `task_dependencies`,
  `notifications`, `time_entries` were missing). Fixed - now grants all 20.
  **If the schema grows again, this file needs updating again - nothing
  keeps it in sync automatically.**

## The working frontend (`web/`)

Plain HTML/CSS/JS, **no build step, no framework** (explicit user
preference - tried React/jQuery framing, both rejected as "too heavy" /
"too AI-looking" for this). Font is Lilex (Google Fonts, monospace).
Colors: `#23160E` background, `#9BBCB7` teal accent, `#E8B6B9` pink
accent, grey/white text.

Structure:
- Toolbar: `demo` / `process` tabs, pure JS show/hide, no router.
- `demo` tab: a canvas "pulse" visualization (scrolling waveform of
  writes/sec, glow effect, turns pink when hot) with a live total-rows
  and writes/sec number overlaid, plus a traffic dial (slider → debounced
  POST to `/api/traffic/dial`).
- `process` tab: short static copy explaining the three worm commands.

Data source: `GET /api/stats/stream` (SSE), which reads postgres's own
`pg_stat_user_tables` write counters once a second - no bespoke state to
keep in sync, "how much has postgres actually written" is a real signal.

Config: `web/.env` (gitignored, real value) + `web/.env.example`
(committed placeholder) + `web/gen-env.sh` (writes `web/env.js`, which
`index.html` loads before `app.js` - this is the no-bundler stand-in for
`process.env`, since static JS can't read a `.env` file directly). Run
`make fe` from the repo root - it runs `gen-env.sh` then serves `web/` on
`:3000` via `python3 -m http.server`, matching the CORS allowlist already
in `server/main.go`.

**Not wired for prod yet**: `API_BASE` in `.env` points at
`http://localhost:8080`. The real backend will be plain HTTP; Vercel
serves HTTPS; browsers block that (mixed content). Needs TLS in front of
the Go backend (Caddy was the plan, per the existing CORS comment about
trusting `X-Forwarded-For`) before a real `API_BASE` will actually work
from the deployed frontend. Also update `allowedOrigins` in
`server/main.go` with the real Vercel domain once it exists.

## What got reverted (context, not a todo)

The second pass added, then removed on request:
- A "pipeline" section: postgres box ↔ animated connector (dial moved
  here, plus a live rate readout and a backfill/streaming stage badge) ↔
  sqlite box, each box showing live table/row counts.
- A proper SVG line chart replacing the canvas pulse - real x/y axes,
  gridlines, hover crosshair + tooltip.
- A "proof of sync" panel: pick a table, see postgres's most recent rows
  next to sqlite's rows for those same ids, mismatches highlighted.
- A disabled SQL editor shell (placeholder only, not functional).
- Backend: `server/internal/pipeline` (now deleted) read worm's state db
  (`capture_batch_state` - counts anything not `status='done'` to tell
  backfill from streaming) and worm's sqlite target file directly,
  read-only, off disk (`WORM_STATE_DB_PATH` / `WORM_TARGET_DB_PATH` env
  vars). Needed `github.com/mattn/go-sqlite3` as a new dependency.

None of that code exists anymore (reverted, not just hidden) - if a future
pass wants to rebuild toward that design, it'll need to be written again,
but the approach above (state db for stage, direct sqlite file read for
counts, `encoding/json` sorts map keys alphabetically so use a slice not a
map if you want ordered rows) is worth reusing rather than rediscovering.

## Next up (unchanged from docs/plan.md)

1. `sqlguard` - the AST-validated SQL editor backend. Not started.
   `docs/plan.md` has the full guardrail list (parse via
   `pganalyze/pg_query_go`, statement-shape allowlist, DB-level grants as
   backstop, per-statement timeouts, separate connection pool, reuse
   `internal/middleware` for rate limiting).
2. TLS in front of the Go backend (blocks frontend from being usable in
   prod at all, see above).
3. Rename/clean up `infra/oci/terraform/` now that it's actually AWS, or
   add a proper `infra/aws/` and retire the OCI attempt.
4. `.gitignore` for the two compiled binaries sitting in `server/`
   (`server/demo`, `server/normalize`) before anyone runs a broad `git
   add`.
5. Actually deploy+smoke-test the systemd units (`infra/systemd/`) on the
   real box - so far things have been run manually over SSH, not through
   the units themselves.
