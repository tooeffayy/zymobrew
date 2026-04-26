package jobs

import (
	"context"

	"github.com/riverqueue/river"

	"zymobrew/internal/queries"
)

// ExpiredSessionsArgs is the (empty) argument set for the periodic
// expired-session GC job. Exported so tests can construct the job.
type ExpiredSessionsArgs struct{}

// Kind is the queue kind River uses to route the job to its worker.
func (ExpiredSessionsArgs) Kind() string { return "expired_sessions_gc" }

// expiredSessionsWorker deletes session rows whose expires_at is in the
// past. Scheduled hourly; cheap and idempotent.
type expiredSessionsWorker struct {
	river.WorkerDefaults[ExpiredSessionsArgs]
	queries *queries.Queries
}

func (w *expiredSessionsWorker) Work(ctx context.Context, _ *river.Job[ExpiredSessionsArgs]) error {
	return w.queries.DeleteExpiredSessions(ctx)
}
