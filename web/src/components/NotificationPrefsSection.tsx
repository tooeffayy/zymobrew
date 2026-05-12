import { FormEvent, useEffect, useState } from "react";

import { ApiError, NotificationPrefs, api } from "../api";

// Server-side delivery preferences (not per-browser — see PushSubscribeSection
// for that). Push toggle, Apprise URL + toggle (delivers email/Discord/
// Telegram/Matrix/ntfy/etc. via an Apprise sidecar), quiet-hours window,
// and IANA timezone for interpreting that window.
export function NotificationPrefsSection() {
  const [prefs, setPrefs] = useState<NotificationPrefs | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Form-local mirrors of the server fields. Initialized from `prefs`
  // once it loads; thereafter the user owns them until they save.
  const [pushEnabled, setPushEnabled]       = useState(true);
  const [appriseEnabled, setAppriseEnabled] = useState(false);
  const [appriseUrl, setAppriseUrl]         = useState("");
  const [quietStart, setQuietStart]         = useState("");
  const [quietEnd, setQuietEnd]             = useState("");
  const [timezone, setTimezone]             = useState("");

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const [testing, setTesting] = useState(false);
  const [testStatus, setTestStatus] = useState<{ ok: boolean; msg: string } | null>(null);

  useEffect(() => {
    setLoading(true);
    api
      .get<NotificationPrefs>("/api/notifications/prefs")
      .then((p) => {
        setPrefs(p);
        setPushEnabled(p.push_enabled);
        setAppriseEnabled(p.apprise_enabled);
        setAppriseUrl(p.apprise_url ?? "");
        setQuietStart(p.quiet_hours_start ?? "");
        setQuietEnd(p.quiet_hours_end ?? "");
        // If the server's value is the bare default ("UTC") and the
        // browser knows a real timezone, prefill that — saves the user
        // typing it on first save. They can still change it.
        const fallback =
          p.timezone === "UTC"
            ? Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"
            : p.timezone;
        setTimezone(fallback);
      })
      .catch((e) => setLoadError(e instanceof ApiError ? e.message : "failed to load preferences"))
      .finally(() => setLoading(false));
  }, []);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setSaveError(null);
    setSaved(false);
    // Quiet-hours pairing: server accepts either set independently,
    // but a half-set window is meaningless. Enforce here.
    if ((quietStart && !quietEnd) || (!quietStart && quietEnd)) {
      setSaveError("Set both quiet-hour times, or clear both.");
      return;
    }
    setSaving(true);
    try {
      // Send everything every time. The server's COALESCE pattern means
      // omitting a field leaves the prior value — but we want the form
      // to be the source of truth on submit.
      const body: Record<string, unknown> = {
        push_enabled: pushEnabled,
        apprise_enabled: appriseEnabled,
        apprise_url: appriseUrl.trim(),
        timezone: timezone || "UTC",
      };
      if (quietStart) body.quiet_hours_start = quietStart;
      if (quietEnd)   body.quiet_hours_end   = quietEnd;
      const updated = await api.patch<NotificationPrefs>("/api/notifications/prefs", body);
      setPrefs(updated);
      setSaved(true);
    } catch (e) {
      setSaveError(e instanceof ApiError ? e.message : "save failed");
    } finally {
      setSaving(false);
    }
  };

  const onTest = async () => {
    setTestStatus(null);
    const url = appriseUrl.trim();
    if (!url) {
      setTestStatus({ ok: false, msg: "Enter an Apprise URL first." });
      return;
    }
    setTesting(true);
    try {
      await api.post<{ ok: boolean }>("/api/notifications/prefs/test", { apprise_url: url });
      setTestStatus({ ok: true, msg: "Test notification sent. Check your destination." });
    } catch (e) {
      setTestStatus({ ok: false, msg: e instanceof ApiError ? e.message : "test failed" });
    } finally {
      setTesting(false);
    }
  };

  return (
    <section className="recipe-section prefs-section">
      <h2>Notification preferences</h2>

      {loading ? (
        <p className="muted">Loading…</p>
      ) : loadError ? (
        <p className="error">{loadError}</p>
      ) : (
        <form onSubmit={onSubmit} className="prefs-form">
          <label className="prefs-toggle">
            <input
              type="checkbox"
              checked={pushEnabled}
              onChange={(e) => setPushEnabled(e.target.checked)}
            />
            <span>
              <strong>Send push notifications</strong>
              <small className="muted">When off, push delivery is paused for every browser you've subscribed (manage subscriptions in <em>This browser</em> below).</small>
            </span>
          </label>

          <label className="prefs-toggle">
            <input
              type="checkbox"
              checked={appriseEnabled}
              onChange={(e) => setAppriseEnabled(e.target.checked)}
            />
            <span>
              <strong>Send via Apprise (email, Discord, Telegram, ntfy, …)</strong>
              <small className="muted">
                Routed through this instance's <a href="https://github.com/caronc/apprise/wiki" target="_blank" rel="noreferrer">Apprise</a> sidecar.
                Requires the operator to set <code>APPRISE_API_URL</code>; otherwise this toggle has no effect.
              </small>
            </span>
          </label>

          <label className="field">
            <span>Apprise URL</span>
            <input
              type="text"
              value={appriseUrl}
              onChange={(e) => setAppriseUrl(e.target.value)}
              placeholder="mailto://user:pass@smtp.example.com  /  discord://webhook_id/webhook_token  /  tgram://bottoken/ChatID"
              spellCheck={false}
              autoCapitalize="off"
              autoCorrect="off"
            />
            <small className="muted">
              See the <a href="https://github.com/caronc/apprise/wiki" target="_blank" rel="noreferrer">Apprise wiki</a> for the URL syntax for each service.
              Leave empty to disable. Saved per-account, never shared.
            </small>
            <div className="form-actions" style={{ marginTop: "0.5rem" }}>
              <button type="button" onClick={onTest} disabled={testing || !appriseUrl.trim()}>
                {testing ? "Sending…" : "Send test"}
              </button>
              {testStatus && (
                <span className={testStatus.ok ? "muted" : "error"} style={{ marginLeft: "0.75rem" }}>
                  {testStatus.msg}
                </span>
              )}
            </div>
          </label>

          <fieldset className="prefs-quiet">
            <legend>Quiet hours</legend>
            <p className="muted prefs-help">
              During these hours, in-app notifications still appear but push delivery is suppressed.
            </p>
            <div className="prefs-quiet-row">
              <label className="field">
                <span>Start</span>
                <input
                  type="time"
                  value={quietStart}
                  onChange={(e) => setQuietStart(e.target.value)}
                />
              </label>
              <label className="field">
                <span>End</span>
                <input
                  type="time"
                  value={quietEnd}
                  onChange={(e) => setQuietEnd(e.target.value)}
                />
              </label>
              {(quietStart || quietEnd) && (
                <button
                  type="button"
                  className="link-button"
                  onClick={() => { setQuietStart(""); setQuietEnd(""); }}
                >
                  Clear
                </button>
              )}
            </div>
          </fieldset>

          <label className="field">
            <span>Timezone</span>
            <input
              type="text"
              value={timezone}
              onChange={(e) => setTimezone(e.target.value)}
              placeholder="America/Los_Angeles"
              spellCheck={false}
              autoCapitalize="off"
              autoCorrect="off"
            />
            <small className="muted">
              IANA name (e.g. <code>Europe/Berlin</code>). Used to interpret quiet hours.
            </small>
          </label>

          {saveError && <p className="error">{saveError}</p>}
          {saved    && <p className="muted">Saved.</p>}

          <div className="form-actions">
            <button type="submit" disabled={saving}>
              {saving ? "Saving…" : "Save preferences"}
            </button>
          </div>

          {prefs && prefs.timezone !== timezone && (
            <p className="muted prefs-help">
              Currently saved as <code>{prefs.timezone}</code>.
            </p>
          )}
        </form>
      )}
    </section>
  );
}
