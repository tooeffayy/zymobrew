import { useCallback, useEffect, useState } from "react";

import { AdminBackup, AdminBackupStatus, api } from "../api";
import { AdminLayout } from "../components/AdminLayout";

// Admin backup management. Lists past pg_dump backups, lets the operator
// trigger a fresh one (the dispatcher picks it up within ~1 minute), and
// links to download for completed ones.
//
// Polling: while at least one backup is pending or running, refresh
// every 5s so the operator can watch the status flip without F5. When
// everything is settled, no polling.

const POLL_INTERVAL_MS = 5_000;

export function AdminBackups() {
  const [backups, setBackups] = useState<AdminBackup[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const data = await api.get<AdminBackup[]>("/api/admin/backups");
      setBackups(data);
      setLoadError(null);
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : "failed to load backups");
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Poll while any backup is in flight. Effect re-subscribes whenever
  // `backups` changes — when the last running one flips to complete,
  // the next render's effect sees no in-flight rows and skips the
  // setInterval entirely.
  useEffect(() => {
    if (!backups) return;
    const inFlight = backups.some((b) => b.status === "pending" || b.status === "running");
    if (!inFlight) return;
    const id = setInterval(() => void refresh(), POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [backups, refresh]);

  const onRunNow = async () => {
    setActionError(null);
    setBusy(true);
    try {
      await api.post<AdminBackup>("/api/admin/backups");
      await refresh();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : "trigger failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <AdminLayout>
      <section className="recipe-section">
        <div className="admin-section-head">
          <div>
            <h2>Backups</h2>
            <p className="muted">
              Full <code>pg_dump --format=custom</code> snapshots. The instance also runs one
              automatically every 24h; older backups are pruned on the configured retention
              window. Backups stream from the configured backup store — local disk, or S3 via
              a presigned 15-minute URL.
            </p>
          </div>
          <button type="button" onClick={onRunNow} disabled={busy}>
            {busy ? "Queuing…" : "Run backup now"}
          </button>
        </div>
        {actionError && <p className="error">{actionError}</p>}
        {loadError && <p className="error">{loadError}</p>}
        {backups === null && !loadError && <p className="muted">Loading…</p>}
        {backups !== null && backups.length === 0 && !loadError && (
          <p className="muted">
            No backups yet. The first scheduled run happens within 24 hours of server start, or
            click <strong>Run backup now</strong> above.
          </p>
        )}
        {backups !== null && backups.length > 0 && <BackupList backups={backups} />}
      </section>
    </AdminLayout>
  );
}

function BackupList({ backups }: { backups: AdminBackup[] }) {
  return (
    <ul className="backup-list">
      {backups.map((b) => (
        <li key={b.id} className="backup-row">
          <div className="backup-main">
            <div className="backup-title-row">
              <StatusPill status={b.status} />
              <span className="backup-when">{formatDateTime(b.created_at)}</span>
              <span className="backup-store muted">{b.storage_backend}</span>
            </div>
            <div className="backup-meta muted">
              {b.size_bytes !== undefined && <span>{formatBytes(b.size_bytes)}</span>}
              {b.completed_at && (
                <span>completed {formatDateTime(b.completed_at)}</span>
              )}
              {b.sha256 && <span className="mono backup-sha">sha256 {b.sha256.slice(0, 12)}…</span>}
            </div>
            {b.error && <p className="error backup-err">{b.error}</p>}
          </div>
          {b.status === "complete" && (
            <a
              className="action-button action-primary"
              href={`/api/admin/backups/${b.id}/download`}
            >
              Download
            </a>
          )}
        </li>
      ))}
    </ul>
  );
}

function StatusPill({ status }: { status: AdminBackupStatus }) {
  return <span className={`backup-status backup-status-${status}`}>{status}</span>;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GiB`;
}

function formatDateTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString();
}
