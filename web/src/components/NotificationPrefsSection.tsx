import { FormEvent, useEffect, useState } from "react";

import { ApiError, NotificationPrefs, api } from "../api";

// Server-side delivery preferences (not per-browser — see PushSubscribeSection
// for that). Push toggle, Apprise URL + toggle, SMTP email toggle (covers
// the same use case as Apprise's mailto:// for users who don't want to run
// the sidecar), and quiet-hours window. Timezone lives on UserPreferences
// (see /me/preferences) since it applies beyond just notifications.
export function NotificationPrefsSection() {
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Form-local mirrors of the server fields. Initialized from `prefs`
  // once it loads; thereafter the user owns them until they save.
  const [pushEnabled, setPushEnabled]       = useState(true);
  const [appriseEnabled, setAppriseEnabled] = useState(false);
  const [appriseUrl, setAppriseUrl]         = useState("");
  const [emailEnabled, setEmailEnabled]     = useState(false);
  const [quietStart, setQuietStart]         = useState("");
  const [quietEnd, setQuietEnd]             = useState("");

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const [testing, setTesting] = useState(false);
  const [testStatus, setTestStatus] = useState<{ ok: boolean; msg: string } | null>(null);

  const [emailTesting, setEmailTesting] = useState(false);
  const [emailTestStatus, setEmailTestStatus] = useState<{ ok: boolean; msg: string } | null>(null);

  useEffect(() => {
    setLoading(true);
    api
      .get<NotificationPrefs>("/api/notifications/prefs")
      .then((p) => {
        setPushEnabled(p.push_enabled);
        setAppriseEnabled(p.apprise_enabled);
        setAppriseUrl(p.apprise_url ?? "");
        setEmailEnabled(p.email_enabled);
        setQuietStart(p.quiet_hours_start ?? "");
        setQuietEnd(p.quiet_hours_end ?? "");
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
        email_enabled: emailEnabled,
      };
      if (quietStart) body.quiet_hours_start = quietStart;
      if (quietEnd)   body.quiet_hours_end   = quietEnd;
      await api.patch<NotificationPrefs>("/api/notifications/prefs", body);
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

  const onTestEmail = async () => {
    setEmailTestStatus(null);
    setEmailTesting(true);
    try {
      await api.post<{ ok: boolean }>("/api/notifications/prefs/test-email");
      setEmailTestStatus({ ok: true, msg: "Test email sent. Check your inbox." });
    } catch (e) {
      setEmailTestStatus({ ok: false, msg: e instanceof ApiError ? e.message : "test failed" });
    } finally {
      setEmailTesting(false);
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

          <label className="prefs-toggle">
            <input
              type="checkbox"
              checked={emailEnabled}
              onChange={(e) => setEmailEnabled(e.target.checked)}
            />
            <span>
              <strong>Send reminder emails</strong>
              <small className="muted">
                Delivered to your account email via the instance's SMTP relay.
                Requires the operator to configure <code>SMTP_HOST</code>; otherwise this toggle has no effect.
                If you already use Apprise <code>mailto://</code> you don't need this on too.
              </small>
              <div className="form-actions" style={{ marginTop: "0.5rem" }}>
                <button type="button" onClick={onTestEmail} disabled={emailTesting}>
                  {emailTesting ? "Sending…" : "Send test email"}
                </button>
                {emailTestStatus && (
                  <span className={emailTestStatus.ok ? "muted" : "error"} style={{ marginLeft: "0.75rem" }}>
                    {emailTestStatus.msg}
                  </span>
                )}
              </div>
            </span>
          </label>

          <fieldset className="prefs-quiet">
            <legend>Quiet hours</legend>
            <p className="muted prefs-help">
              During these hours (in the timezone set under <a href="/me/preferences">Preferences</a>), in-app notifications still appear but push delivery is suppressed.
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

          {saveError && <p className="error">{saveError}</p>}
          {saved    && <p className="muted">Saved.</p>}

          <div className="form-actions">
            <button type="submit" disabled={saving}>
              {saving ? "Saving…" : "Save preferences"}
            </button>
          </div>
        </form>
      )}
    </section>
  );
}
