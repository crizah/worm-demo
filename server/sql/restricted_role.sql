-- restricted role used by: the traffic generator, the periodic normalizer,
-- and (later) user-submitted SQL from the web UI. Same role for all three
-- on purpose - one permission boundary to get right, not several.
--
-- Run this against the demo Postgres instance once, as a superuser, AFTER
-- scripts/dev_schema.sql (from the main Worm repo) has been applied.

CREATE ROLE demo_writer LOGIN PASSWORD 'pqpq';

GRANT USAGE ON SCHEMA public TO demo_writer;

GRANT SELECT, INSERT, UPDATE, DELETE ON
    organizations,
    users,
    teams,
    team_members,
    projects,
    tasks,
    tags,
    task_tags,
    task_comments,
    audit_logs,
    roles,
    webhooks,
    webhook_deliveries,
    integrations,
    api_keys,
    user_roles,
    milestones,
    task_dependencies,
    notifications,
    time_entries
TO demo_writer;

-- explicitly NOT granted: DROP, TRUNCATE, ALTER, CREATE, REFERENCES on
-- anything - and nothing at all on capture_* (worm's own state tables).
-- demo_writer shouldn't even be able to see those exist.
--
-- also worth doing once the AST-based statement validator (sqlguard) is in
-- place for user-submitted SQL, so a bug in that validator can't matter:
--
-- REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM demo_writer;
--
-- and only re-grant EXECUTE on specific functions if some allowed
-- statement shape genuinely needs one - if INSERT/UPDATE/DELETE stay
-- restricted to literal VALUES/WHERE (no function calls), none should be
-- needed at all.

-- worm's own connection (migrate-schema/migrate-data/migrate-resume) must
-- be a DIFFERENT, more privileged role - it needs REPLICATION and DDL
-- rights this role deliberately does not have. Never point
-- DEMO_WRITER_CONN_STR at that role, and never point worm's
-- SOURCE_CONN_STR at demo_writer.
