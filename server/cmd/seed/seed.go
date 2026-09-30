package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"golang.org/x/sync/errgroup"

	"server/internal/seeddata"
)

type seedFn func(context.Context, *sql.DB, int) error

type tableSeed struct {
	fn seedFn
	n  int
}

// levels groups the 20 tables by FK dependency depth (matches schema.sql).
// Tables within a level don't depend on each other, so they seed
// concurrently via errgroup (same pattern inspect/postgresql.go uses in
// the main Worm repo); the next level only starts once every table in
// this one has committed, since its FKs need these rows to exist.
var levels = [][]tableSeed{
	{{seeddata.SeedOrganizations, 30}},
	{
		{seeddata.SeedUsers, 500},
		{seeddata.SeedTeams, 100},
		{seeddata.SeedTags, 150},
		{seeddata.SeedRoles, 60},
		{seeddata.SeedWebhooks, 80},
	},
	{
		{seeddata.SeedTeamMembers, 800},
		{seeddata.SeedProjects, 300},
		{seeddata.SeedIntegrations, 60},
		{seeddata.SeedAPIKeys, 100},
		{seeddata.SeedUserRoles, 700},
		{seeddata.SeedAuditLogs, 2000},
		{seeddata.SeedWebhookDeliveries, 1500},
	},
	{
		{seeddata.SeedMilestones, 500},
		{seeddata.SeedTasks, 8000},
	},
	{
		{seeddata.SeedTaskTags, 12000},
		{seeddata.SeedTaskComments, 10000},
		{seeddata.SeedTaskDependencies, 3000},
		{seeddata.SeedNotifications, 6000},
		{seeddata.SeedTimeEntries, 9000},
	},
}

func seedAll(ctx context.Context, db *sql.DB) error {
	for i, level := range levels {
		g, gctx := errgroup.WithContext(ctx)
		for _, ts := range level {
			ts := ts
			g.Go(func() error { return ts.fn(gctx, db, ts.n) })
		}
		if err := g.Wait(); err != nil {
			return fmt.Errorf("level %d: %w", i, err)
		}
		log.Printf("[seed] level %d done (%d tables)", i, len(level))
	}
	return nil
}
