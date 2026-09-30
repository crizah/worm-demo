package seeddata

import (
	"context"
	"database/sql"
	"fmt"
)

func SeedOrganizations(ctx context.Context, db *sql.DB, n int) error {
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO organizations (name, domain, attendance_enabled)
		SELECT
			'Org ' || substr(md5(random()::text || clock_timestamp()::text), 1, 10),
			'org-' || substr(md5(random()::text || clock_timestamp()::text), 1, 10) || '.example.com',
			(random() < 0.3)
		FROM generate_series(1, %d)
	`, n))
	return err
}

func SeedUsers(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO users (org_id, email, role)
		SELECT
			%s,
			'user-' || substr(md5(random()::text || clock_timestamp()::text), 1, 12) || '@example.com',
			(ARRAY['admin', 'member', 'viewer']::user_role[])[floor(random() * 3 + 1)]
		FROM generate_series(1, %d)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["org"], n, join))
	return err
}

func SeedTeams(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO teams (org_id, name)
		SELECT %s, 'Team ' || substr(md5(random()::text || clock_timestamp()::text), 1, 10)
		FROM generate_series(1, %d)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["org"], n, join))
	return err
}

func SeedTags(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO tags (org_id, name)
		SELECT %s, 'tag-' || substr(md5(random()::text || clock_timestamp()::text), 1, 8)
		FROM generate_series(1, %d)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["org"], n, join))
	return err
}

func SeedRoles(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO roles (org_id, name, permissions)
		SELECT
			%s,
			(ARRAY['owner', 'editor', 'viewer', 'billing']::text[])[floor(random() * 4 + 1)] || '-' || substr(md5(random()::text), 1, 6),
			jsonb_build_object('level', floor(random() * 3 + 1))
		FROM generate_series(1, %d)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["org"], n, join))
	return err
}

func SeedWebhooks(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"org": "organizations"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO webhooks (org_id, url, secret)
		SELECT
			%s,
			'https://example.com/hooks/' || substr(md5(random()::text), 1, 10),
			substr(md5(random()::text || clock_timestamp()::text), 1, 24)
		FROM generate_series(1, %d)
		CROSS JOIN %s
	`, with, pick["org"], n, join))
	return err
}

func SeedTeamMembers(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"team": "teams", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO team_members (team_id, user_id)
		SELECT %s, %s
		FROM generate_series(1, %d)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["team"], pick["user"], n, join))
	return err
}

func SeedProjects(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"team": "teams"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO projects (team_id, name, is_archived)
		SELECT %s, 'Project ' || substr(md5(random()::text || clock_timestamp()::text), 1, 10), (random() < 0.15)
		FROM generate_series(1, %d)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["team"], n, join))
	return err
}

func SeedIntegrations(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"org": "organizations", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO integrations (org_id, provider, connected_by)
		SELECT %s, (ARRAY['github', 'slack', 'jira', 'stripe']::integration_provider[])[floor(random() * 4 + 1)], %s
		FROM generate_series(1, %d)
		CROSS JOIN %s
	`, with, pick["org"], pick["user"], n, join))
	return err
}

func SeedAPIKeys(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"org": "organizations", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO api_keys (org_id, created_by, key_hash, revoked)
		SELECT %s, %s, substr(md5(random()::text || clock_timestamp()::text), 1, 32), (random() < 0.1)
		FROM generate_series(1, %d)
		CROSS JOIN %s
	`, with, pick["org"], pick["user"], n, join))
	return err
}

func SeedUserRoles(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"user": "users", "role": "roles"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO user_roles (user_id, role_id)
		SELECT %s, %s
		FROM generate_series(1, %d)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["user"], pick["role"], n, join))
	return err
}

func SeedAuditLogs(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"org": "organizations", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO audit_logs (org_id, user_id, action, detail)
		SELECT
			%s,
			CASE WHEN random() < 0.9 THEN %s ELSE NULL END,
			(ARRAY['created', 'updated', 'deleted']::audit_action[])[floor(random() * 3 + 1)],
			'seed row'
		FROM generate_series(1, %d)
		CROSS JOIN %s
	`, with, pick["org"], pick["user"], n, join))
	return err
}

func SeedWebhookDeliveries(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"webhook": "webhooks"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO webhook_deliveries (webhook_id, event_type, status_code)
		SELECT
			%s,
			(ARRAY['task.created', 'task.updated', 'comment.created']::text[])[floor(random() * 3 + 1)],
			(ARRAY[200, 200, 200, 429, 500]::int[])[floor(random() * 5 + 1)]
		FROM generate_series(1, %d)
		CROSS JOIN %s
	`, with, pick["webhook"], n, join))
	return err
}

func SeedMilestones(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"project": "projects"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO milestones (project_id, name, due_date)
		SELECT
			%s,
			'Milestone ' || substr(md5(random()::text || clock_timestamp()::text), 1, 8),
			NOW() + (random() * 90 || ' days')::interval
		FROM generate_series(1, %d)
		CROSS JOIN %s
	`, with, pick["project"], n, join))
	return err
}

// SeedTasks leaves parent_task_id NULL - no subtask hierarchy from bulk
// seeding/normalizing, only from real ongoing activity.
func SeedTasks(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"project": "projects", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO tasks (project_id, assignee_id, title, status, metadata)
		SELECT
			%s,
			CASE WHEN random() < 0.8 THEN %s ELSE NULL END,
			'Task ' || substr(md5(random()::text || clock_timestamp()::text), 1, 12),
			(ARRAY['todo', 'in_progress', 'done', 'archived']::task_status[])[floor(random() * 4 + 1)],
			jsonb_build_object('priority', floor(random() * 3 + 1))
		FROM generate_series(1, %d)
		CROSS JOIN %s
		ON CONFLICT (assignee_id) WHERE status = 'in_progress' DO NOTHING
	`, with, pick["project"], pick["user"], n, join))
	return err
}

func SeedTaskTags(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"task": "tasks", "tag": "tags"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO task_tags (task_id, tag_id)
		SELECT %s, %s
		FROM generate_series(1, %d)
		CROSS JOIN %s
		ON CONFLICT DO NOTHING
	`, with, pick["task"], pick["tag"], n, join))
	return err
}

func SeedTaskComments(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"task": "tasks", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO task_comments (task_id, user_id, body)
		SELECT %s, %s, 'Comment ' || substr(md5(random()::text || clock_timestamp()::text), 1, 20)
		FROM generate_series(1, %d)
		CROSS JOIN %s
	`, with, pick["task"], pick["user"], n, join))
	return err
}

// SeedTaskDependencies: task_id and depends_on_task_id both pick from the
// same pool, and need comparing against each other (no self-deps). random()
// is volatile, so referencing a pick expression twice (SELECT + WHERE)
// would re-roll a different value the second time - the inner subquery
// computes each pick once per row and names it, so the outer WHERE
// compares the same materialized values the INSERT actually uses.
func SeedTaskDependencies(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"task": "tasks", "dep": "tasks"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO task_dependencies (task_id, depends_on_task_id)
		SELECT task_id, dep_id FROM (
			SELECT %s AS task_id, %s AS dep_id
			FROM generate_series(1, %d)
			CROSS JOIN %s
		) picked
		WHERE task_id <> dep_id
		ON CONFLICT DO NOTHING
	`, with, pick["task"], pick["dep"], n, join))
	return err
}

func SeedNotifications(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"user": "users", "actor": "users", "task": "tasks"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO notifications (user_id, actor_id, task_id, type, read)
		SELECT
			%s,
			CASE WHEN random() < 0.9 THEN %s ELSE NULL END,
			CASE WHEN random() < 0.7 THEN %s ELSE NULL END,
			(ARRAY['mention', 'assignment', 'comment', 'deadline']::notification_type[])[floor(random() * 4 + 1)],
			(random() < 0.4)
		FROM generate_series(1, %d)
		CROSS JOIN %s
	`, with, pick["user"], pick["actor"], pick["task"], n, join))
	return err
}

func SeedTimeEntries(ctx context.Context, db *sql.DB, n int) error {
	with, join, pick := RandomPools(map[string]string{"task": "tasks", "user": "users"})
	_, err := db.ExecContext(ctx, fmt.Sprintf(`
		WITH %s
		INSERT INTO time_entries (task_id, user_id, minutes)
		SELECT %s, %s, floor(random() * 180 + 5)::int
		FROM generate_series(1, %d)
		CROSS JOIN %s
	`, with, pick["task"], pick["user"], n, join))
	return err
}
