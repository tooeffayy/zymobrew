package jobs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"zymobrew/internal/queries"
	"zymobrew/internal/storage"
)

// AdminBackupScheduleArgs runs on the daily periodic schedule and inserts a
// pending admin_backup row. The per-minute adminBackupDispatchWorker picks it
// up within a minute, keeping HTTP-triggered backups responsive too.
type AdminBackupScheduleArgs struct{}

func (AdminBackupScheduleArgs) Kind() string { return "admin_backup_scheduler" }

type adminBackupScheduleWorker struct {
	river.WorkerDefaults[AdminBackupScheduleArgs]
	queries *queries.Queries
	store   storage.Store
}

func (w *adminBackupScheduleWorker) Work(ctx context.Context, _ *river.Job[AdminBackupScheduleArgs]) error {
	_, err := w.queries.CreateAdminBackup(ctx, w.store.Backend())
	if err != nil {
		return fmt.Errorf("schedule admin backup: %w", err)
	}
	return nil
}

// AdminBackupDispatchArgs claims pending admin backup rows and runs pg_dump.
// Runs every minute so HTTP-triggered backups and scheduled backups are both
// processed promptly.
type AdminBackupDispatchArgs struct{}

func (AdminBackupDispatchArgs) Kind() string { return "admin_backup_dispatcher" }

type adminBackupDispatchWorker struct {
	river.WorkerDefaults[AdminBackupDispatchArgs]
	queries       *queries.Queries
	store         storage.Store
	dbURL         string
	retentionDays int
}

func (w *adminBackupDispatchWorker) Work(ctx context.Context, _ *river.Job[AdminBackupDispatchArgs]) error {
	pending, err := w.queries.ClaimPendingAdminBackups(ctx)
	if err != nil {
		return fmt.Errorf("claim pending admin backups: %w", err)
	}
	for _, row := range pending {
		if err := w.processBackup(ctx, row); err != nil {
			slog.Error("admin backup failed", "backup_id", row.ID, "err", err)
			_ = w.queries.FailAdminBackup(ctx, queries.FailAdminBackupParams{
				ID:    row.ID,
				Error: pgtype.Text{String: fmt.Sprintf("%v", err), Valid: true},
			})
		}
	}
	// Clean up expired backups on the same tick.
	w.pruneExpired(ctx)
	return nil
}

func (w *adminBackupDispatchWorker) processBackup(ctx context.Context, row queries.AdminBackup) error {
	key := fmt.Sprintf("exports/admin/%s.dump", row.ID)

	cmd := buildPgDumpCmd(ctx, w.dbURL)

	pr, pw := io.Pipe()
	cmd.Stdout = pw

	done := make(chan struct{})
	var cmdErr error
	go func() {
		defer close(done)
		cmdErr = cmd.Run()
		if cmdErr != nil {
			_ = pw.CloseWithError(fmt.Errorf("pg_dump: %w", cmdErr))
		} else {
			pw.Close()
		}
	}()

	cr := &countingReader{r: pr}
	putErr := w.store.Put(ctx, key, cr, -1)
	// Close the read end so the goroutine exits if Put returned early due to
	// error — otherwise cmd.Run() blocks on a full pipe forever.
	_ = pr.Close()
	<-done

	if cmdErr != nil {
		return fmt.Errorf("pg_dump: %w", cmdErr)
	}
	if putErr != nil {
		return fmt.Errorf("store put: %w", putErr)
	}

	_, err := w.queries.CompleteAdminBackup(ctx, queries.CompleteAdminBackupParams{
		ID:        row.ID,
		FilePath:  pgtype.Text{String: key, Valid: true},
		SizeBytes: pgtype.Int8{Int64: cr.n, Valid: true},
	})
	return err
}

func (w *adminBackupDispatchWorker) pruneExpired(ctx context.Context) {
	filePaths, err := w.queries.DeleteExpiredAdminBackups(ctx, int32(w.retentionDays))
	if err != nil {
		slog.Error("delete expired admin backups", "err", err)
		return
	}
	for _, p := range filePaths {
		if !p.Valid || p.String == "" {
			continue
		}
		if err := w.store.Delete(ctx, p.String); err != nil {
			slog.Error("delete expired backup file", "path", p.String, "err", err)
		}
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// buildPgDumpCmd constructs a pg_dump command. When the DATABASE_URL is a
// standard postgres:// URI, connection parameters are passed via environment
// variables instead of the command line so credentials don't appear in
// process listings. Key-value DSNs fall back to the --dbname flag.
func buildPgDumpCmd(ctx context.Context, dbURL string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom")

	u, err := url.Parse(dbURL)
	if err == nil && u.Host != "" {
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = "5432"
		}
		dbname := strings.TrimPrefix(u.Path, "/")
		username := u.User.Username()
		password, _ := u.User.Password()

		env := append(os.Environ(),
			"PGHOST="+host,
			"PGPORT="+port,
			"PGDATABASE="+dbname,
		)
		if username != "" {
			env = append(env, "PGUSER="+username)
		}
		if password != "" {
			env = append(env, "PGPASSWORD="+password)
		}
		if sslmode := u.Query().Get("sslmode"); sslmode != "" {
			env = append(env, "PGSSLMODE="+sslmode)
		}
		cmd.Env = env
	} else {
		// Key-value DSN or unparseable URI: fall back to --dbname (credentials
		// may be visible in ps output on this path).
		cmd.Args = append(cmd.Args, "--dbname="+dbURL)
	}
	return cmd
}
