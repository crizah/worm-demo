package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"golang.org/x/sync/errgroup"
)

// levels groups the 20 tables by FK dependency depth (matches the
// level comments in schema.sql). Tables within a level don't depend on
// each other, so they seed concurrently via errgroup (same pattern
// inspect/postgresql.go already uses in the main Worm repo); the next
// level only starts once every table in this one has committed, since its
// FKs need these rows to already exist.
var levels = [][]func(context.Context, *sql.DB) error{
	{seedOrganizations},
	{seedUsers, seedTeams, seedTags, seedRoles, seedWebhooks},
	{seedTeamMembers, seedProjects, seedIntegrations, seedAPIKeys, seedUserRoles, seedAuditLogs, seedWebhookDeliveries},
	{seedMilestones, seedTasks},
	{seedTaskTags, seedTaskComments, seedTaskDependencies, seedNotifications, seedTimeEntries},
}

func seedAll(ctx context.Context, db *sql.DB) error {
	for i, level := range levels {
		g, gctx := errgroup.WithContext(ctx)
		for _, fn := range level {
			fn := fn
			g.Go(func() error { return fn(gctx, db) })
		}
		if err := g.Wait(); err != nil {
			return fmt.Errorf("level %d: %w", i, err)
		}
		log.Printf("[seed] level %d done (%d tables)", i, len(level))
	}
	return nil
}

// randomPools builds, for a set of {alias: parentTable} pairs, a WITH
// clause materializing each parent's ids into an array once, a CROSS JOIN
// clause to pull them into scope, and the indexing expression to pick one
// at random per output row. random() is called directly in that
// expression (not inside a bare or LATERAL subquery), which is what
// actually forces per-row evaluation - see the note on this exact gotcha
// in scripts/seed_data.sql in the main Worm repo: an uncorrelated
// "ORDER BY random() LIMIT 1" subquery gets planned as a single InitPlan
// and evaluated ONCE for the whole statement, not once per row, even with
// LATERAL if the planner decides nothing actually correlates it.
func randomPools(parents map[string]string) (withClause, joinClause string, pick map[string]string) {
	var ctes, joins []string
	pick = make(map[string]string)
	for alias, table := range parents {
		ctes = append(ctes, fmt.Sprintf("%s_pool AS (SELECT array_agg(id) AS ids FROM %s)", alias, table))
		joins = append(joins, alias+"_pool")
		pick[alias] = fmt.Sprintf("%s_pool.ids[1 + floor(random() * array_length(%s_pool.ids, 1))::int]", alias, alias)
	}
	return strings.Join(ctes, ", "), strings.Join(joins, " CROSS JOIN "), pick
}

// level 0

func seedOrganizations(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO organizations (name, domain, attendance_enabled)
		SELECT
			'Org ' || substr(md5(random()::text || clock_timestamp()::text), 1, 10),
			'org-' || substr(md5(random()::text || clock_timestamp()::text), 1, 10) || '.example.com',
			(random() < 0.3)
		FROM generate_series(1, 30)
	`)
	return err
}

// level 1

func seedUsers(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO users (org_id, email, role)
		SELECT
			%s,
			'user-' || substr(md5(random()::text || clock_timestamp()::text), 1, 12) || '@example.com',
			(ARRAY['admin', 'member', 'viewer']::user_role[])[floor(random() * 3 + 1)]
		FROM generate_series(1, 500)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["org"], join))
	return err
}

func seedTeams(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO teams (org_id, name)
		SELECT %s, 'Team ' || substr(md5(random()::text || clock_timestamp()::text), 1, 10)
		FROM generate_series(1, 100)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["org"], join))
	return err
}

func seedTags(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO tags (org_id, name)
		SELECT %s, 'tag-' || substr(md5(random()::text || clock_timestamp()::text), 1, 8)
		FROM generate_series(1, 150)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["org"], join))
	return err
}

func seedRoles(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO roles (org_id, name, permissions)
		SELECT
			%s,
			(ARRAY['owner', 'editor', 'viewer', 'billing']::text[])[floor(random() * 4 + 1)] || '-' || substr(md5(random()::text), 1, 6),
			jsonb_build_object('level', floor(random() * 3 + 1))
		FROM generate_series(1, 60)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["org"], join))
	return err
}

func seedWebhooks(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO webhooks (org_id, url, secret)
		SELECT
			%s,
			'https://example.com/hooks/' || substr(md5(random()::text), 1, 10),
			substr(md5(random()::text || clock_timestamp()::text), 1, 24)
		FROM generate_series(1, 80)
		CROSS JOIN %s
	`, with, pick["org"], join))
	return err
}

// level 2

func seedTeamMembers(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"team": "teams", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO team_members (team_id, user_id)
		SELECT %s, %s
		FROM generate_series(1, 800)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["team"], pick["user"], join))
	return err
}

func seedProjects(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"team": "teams"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO projects (team_id, name, is_archived)
		SELECT %s, 'Project ' || substr(md5(random()::text || clock_timestamp()::text), 1, 10), (random() < 0.15)
		FROM generate_series(1, 300)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["team"], join))
	return err
}

func seedIntegrations(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"org": "organizations", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO integrations (org_id, provider, connected_by)
		SELECT %s, (ARRAY['github', 'slack', 'jira', 'stripe']::integration_provider[])[floor(random() * 4 + 1)], %s
		FROM generate_series(1, 60)
		CROSS JOIN %s
	`, with, pick["org"], pick["user"], join))
	return err
}

func seedAPIKeys(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"org": "organizations", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO api_keys (org_id, created_by, key_hash, revoked)
		SELECT %s, %s, substr(md5(random()::text || clock_timestamp()::text), 1, 32), (random() < 0.1)
		FROM generate_series(1, 100)
		CROSS JOIN %s
	`, with, pick["org"], pick["user"], join))
	return err
}

func seedUserRoles(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"user": "users", "role": "roles"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO user_roles (user_id, role_id)
		SELECT %s, %s
		FROM generate_series(1, 700)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["user"], pick["role"], join))
	return err
}

func seedAuditLogs(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"org": "organizations", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO audit_logs (org_id, user_id, action, detail)
		SELECT
			%s,
			CASE WHEN random() < 0.9 THEN %s ELSE NULL END,
			(ARRAY['created', 'updated', 'deleted']::audit_action[])[floor(random() * 3 + 1)],
			'seed row'
		FROM generate_series(1, 2000)
		CROSS JOIN %s
	`, with, pick["org"], pick["user"], join))
	return err
}

func seedWebhookDeliveries(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"webhook": "webhooks"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO webhook_deliveries (webhook_id, event_type, status_code)
		SELECT
			%s,
			(ARRAY['task.created', 'task.updated', 'comment.created']::text[])[floor(random() * 3 + 1)],
			(ARRAY[200, 200, 200, 429, 500]::int[])[floor(random() * 5 + 1)]
		FROM generate_series(1, 1500)
		CROSS JOIN %s
	`, with, pick["webhook"], join))
	return err
}

// level 3

func seedMilestones(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"project": "projects"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO milestones (project_id, name, due_date)
		SELECT
			%s,
			'Milestone ' || substr(md5(random()::text || clock_timestamp()::text), 1, 8),
			NOW() + (random() * 90 || ' days')::interval
		FROM generate_series(1, 500)
		CROSS JOIN %s
	`, with, pick["project"], join))
	return err
}

// parent_task_id is always left NULL here - no subtask hierarchy at seed
// time. The traffic generator (or a visitor via the SQL editor later) is
// what creates the occasional subtask referencing an already-existing task.
func seedTasks(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"project": "projects", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO tasks (project_id, assignee_id, title, status, metadata)
		SELECT
			%s,
			CASE WHEN random() < 0.8 THEN %s ELSE NULL END,
			'Task ' || substr(md5(random()::text || clock_timestamp()::text), 1, 12),
			(ARRAY['todo', 'in_progress', 'done', 'archived']::task_status[])[floor(random() * 4 + 1)],
			jsonb_build_object('priority', floor(random() * 3 + 1))
		FROM generate_series(1, 8000)
		CROSS JOIN %s
		ON CONFLICT (assignee_id) WHERE status = 'in_progress' DO NOTHING
	`, with, pick["project"], pick["user"], join))
	return err
}

// level 4

func seedTaskTags(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"task": "tasks", "tag": "tags"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO task_tags (task_id, tag_id)
		SELECT %s, %s
		FROM generate_series(1, 12000)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["task"], pick["tag"], join))
	return err
}

func seedTaskComments(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"task": "tasks", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO task_comments (task_id, user_id, body)
		SELECT %s, %s, 'Comment ' || substr(md5(random()::text || clock_timestamp()::text), 1, 20)
		FROM generate_series(1, 10000)
		CROSS JOIN %s
	`, with, pick["task"], pick["user"], join))
	return err
}

// task_id and depends_on_task_id both pick from the SAME pool (tasks), and
// unlike every other seed function here, this one needs to compare the two
// picks against each other (task_id <> depends_on_task_id). random() is
// volatile, so referencing pick["task"]/pick["dep"] a second time in a
// WHERE clause would re-roll a DIFFERENT value than what actually landed
// in the SELECT list - the inner subquery computes each pick exactly once
// per row and names it, so the outer WHERE compares the same materialized
// values the INSERT actually uses.
func seedTaskDependencies(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"task": "tasks", "dep": "tasks"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO task_dependencies (task_id, depends_on_task_id)
		SELECT task_id, dep_id FROM (
			SELECT %s AS task_id, %s AS dep_id
			FROM generate_series(1, 3000)
			CROSS JOIN %s
		) picked
		WHERE task_id <> dep_id
		ON CONFLICT DO NOTHING
	`, with, pick["task"], pick["dep"], join))
	return err
}

func seedNotifications(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"user": "users", "actor": "users", "task": "tasks"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO notifications (user_id, actor_id, task_id, type, read)
		SELECT
			%s,
			CASE WHEN random() < 0.9 THEN %s ELSE NULL END,
			CASE WHEN random() < 0.7 THEN %s ELSE NULL END,
			(ARRAY['mention', 'assignment', 'comment', 'deadline']::notification_type[])[floor(random() * 4 + 1)],
			(random() < 0.4)
		FROM generate_series(1, 6000)
		CROSS JOIN %s
	`, with, pick["user"], pick["actor"], pick["task"], join))
	return err
}

func seedTimeEntries(ctx context.Context, db *sql.DB) error {
	with, join, pick := randomPools(map[string]string{"task": "tasks", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO time_entries (task_id, user_id, minutes)
		SELECT %s, %s, floor(random() * 180 + 5)::int
		FROM generate_series(1, 9000)
		CROSS JOIN %s
	`, with, pick["task"], pick["user"], join))
	return err
}
