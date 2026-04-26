// Package jobs wires up the River background-job runtime: worker
// registration, periodic schedules, and lifecycle (Start/Stop) tied to the
// HTTP server. River uses Postgres for queue storage, so no Redis or
// separate job-runner process is needed — it runs in the zymo binary.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"zymobrew/internal/queries"
)

// Client owns the River client and exposes lifecycle methods. The River
// client is parameterised on the transaction type (pgx.Tx).
type Client struct {
	river   *river.Client[pgx.Tx]
	pool    *pgxpool.Pool
	queries *queries.Queries
}

// New constructs a River client with all workers registered and the
// canonical periodic schedule applied. It does not start any workers — call
// Start.
func New(pool *pgxpool.Pool) (*Client, error) {
	q := queries.New(pool)

	workers := river.NewWorkers()
	river.AddWorker(workers, &expiredSessionsWorker{queries: q})

	rc, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 5},
		},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return ExpiredSessionsArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
		},
		Logger: slog.Default(),
	})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}
	return &Client{river: rc, pool: pool, queries: q}, nil
}

// Start begins pulling jobs from the queue. Returns once startup is
// complete; cancel ctx (or call Stop) to terminate.
func (c *Client) Start(ctx context.Context) error {
	return c.river.Start(ctx)
}

// Stop gracefully shuts down the worker pool, waiting for in-flight jobs to
// finish. ctx bounds how long Stop will wait.
func (c *Client) Stop(ctx context.Context) error {
	return c.river.Stop(ctx)
}
