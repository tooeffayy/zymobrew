import { FormEvent, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { ApiError, PublicProfile, api } from "../api";
import { useAuth } from "../auth";
import { TempUnit, useTemperatureUnit } from "../units";

// Authenticated user's profile + account controls. Three sections:
//
//   1. Profile — editable display name / bio / avatar URL, read-only
//      identifiers. Saves via PATCH /api/users/me.
//   2. Password — current + new + confirm. POST /api/users/me/password
//      rotates every other session.
//   3. Danger zone — account deletion, password-confirmed.
//      DELETE /api/users/me anonymizes in place; the server clears the
//      cookie and we flip the auth context to anon and redirect.
//
// We only fetch the editable fields (bio, avatar_url, created_at) here —
// the AuthContext's PublicUser carries id/username/email/display_name
// already, so nothing in the layout flickers while this loads.

export function Me() {
  const { state, updateUser, setAnon } = useAuth();
  const navigate = useNavigate();
  const [profile, setProfile] = useState<PublicProfile | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    if (state.status !== "authed") return;
    api
      .get<PublicProfile>(`/api/users/${state.user.username}`)
      .then(setProfile)
      .catch((e) => setLoadError(e instanceof Error ? e.message : "failed to load profile"));
  }, [state]);

  if (state.status !== "authed") return null;

  return (
    <div className="page profile-page">
      <h1>Profile</h1>
      {loadError && <p className="error">{loadError}</p>}
      {!loadError && !profile && <p className="muted">Loading…</p>}
      {profile && (
        <>
          <ProfileSection
            profile={profile}
            email={state.user.email}
            onUpdated={(p) => {
              setProfile(p);
              updateUser({
                ...state.user,
                display_name: p.display_name ?? "",
              });
            }}
          />
          <PreferencesSection />
          <PasswordSection />
          <DangerSection
            username={state.user.username}
            onDeleted={() => {
              setAnon();
              navigate("/login", { replace: true });
            }}
          />
        </>
      )}
    </div>
  );
}

// --- Profile --------------------------------------------------------------

function ProfileSection({
  profile,
  email,
  onUpdated,
}: {
  profile: PublicProfile;
  email: string;
  onUpdated: (p: PublicProfile) => void;
}) {
  const [displayName, setDisplayName] = useState(profile.display_name ?? "");
  const [bio, setBio] = useState(profile.bio ?? "");
  const [avatarUrl, setAvatarUrl] = useState(profile.avatar_url ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  // Re-baseline the form when the upstream profile is replaced (post-save).
  useEffect(() => {
    setDisplayName(profile.display_name ?? "");
    setBio(profile.bio ?? "");
    setAvatarUrl(profile.avatar_url ?? "");
  }, [profile]);

  const dirty =
    displayName !== (profile.display_name ?? "") ||
    bio !== (profile.bio ?? "") ||
    avatarUrl !== (profile.avatar_url ?? "");

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setSaved(false);
    setBusy(true);
    try {
      const updated = await api.patch<PublicProfile>("/api/users/me", {
        display_name: displayName,
        bio,
        avatar_url: avatarUrl.trim(),
      });
      onUpdated(updated);
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "save failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="recipe-section">
      <h2>About you</h2>
      <form className="profile-form" onSubmit={onSubmit}>
        <dl className="profile-readonly">
          <dt>Username</dt><dd>{profile.username}</dd>
          <dt>Email</dt><dd>{email}</dd>
          <dt>User ID</dt><dd className="mono">{profile.id}</dd>
          <dt>Member since</dt>
          <dd>{new Date(profile.created_at).toLocaleDateString()}</dd>
        </dl>
        <label className="field">
          <span>Display name</span>
          <input
            type="text"
            maxLength={64}
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder={profile.username}
          />
          <span className="field-help muted">
            Shown on recipes and feeds. Falls back to your username if blank.
          </span>
        </label>
        <label className="field">
          <span>Bio</span>
          <textarea
            maxLength={2048}
            rows={4}
            value={bio}
            onChange={(e) => setBio(e.target.value)}
          />
        </label>
        <label className="field">
          <span>Avatar URL</span>
          <input
            type="url"
            maxLength={512}
            value={avatarUrl}
            onChange={(e) => setAvatarUrl(e.target.value)}
            placeholder="https://…"
          />
        </label>
        {error && <p className="error">{error}</p>}
        {saved && !error && !dirty && <p className="muted">Saved.</p>}
        <div className="form-actions">
          <button type="submit" disabled={busy || !dirty}>
            {busy ? "Saving…" : "Save profile"}
          </button>
        </div>
      </form>
    </section>
  );
}

// --- Preferences ----------------------------------------------------------

// Display-only preferences kept in localStorage. The server stores
// canonical units (Celsius); the toggle just swaps how readings are
// rendered + interpreted on input. Per-device by design — same brewer
// might run metric on their phone and imperial on their laptop.
function PreferencesSection() {
  const [tempUnit, setTempUnit] = useTemperatureUnit();

  const choose = (u: TempUnit) => () => setTempUnit(u);

  return (
    <section className="recipe-section">
      <h2>Preferences</h2>
      <div className="profile-form">
        <fieldset className="field">
          <legend>Temperature unit</legend>
          <div className="radio-group">
            <label className="radio">
              <input
                type="radio"
                name="temp-unit"
                value="C"
                checked={tempUnit === "C"}
                onChange={choose("C")}
              />
              <span>Celsius (°C)</span>
            </label>
            <label className="radio">
              <input
                type="radio"
                name="temp-unit"
                value="F"
                checked={tempUnit === "F"}
                onChange={choose("F")}
              />
              <span>Fahrenheit (°F)</span>
            </label>
          </div>
          <span className="field-help muted">
            Affects how temperatures are shown and entered. Stored values are
            always Celsius — flipping back and forth is lossless.
          </span>
        </fieldset>
      </div>
    </section>
  );
}

// --- Password -------------------------------------------------------------

function PasswordSection() {
  const [current, setCurrent] = useState("");
  const [next1, setNext1] = useState("");
  const [next2, setNext2] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [okMsg, setOkMsg] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setOkMsg(null);
    if (next1 !== next2) {
      setError("New passwords don't match.");
      return;
    }
    if (next1.length < 8) {
      setError("New password must be at least 8 characters.");
      return;
    }
    setBusy(true);
    try {
      await api.post("/api/users/me/password", {
        current_password: current,
        new_password: next1,
      });
      setCurrent("");
      setNext1("");
      setNext2("");
      setOkMsg("Password changed. Other sessions were signed out.");
    } catch (e) {
      setError(e instanceof Error ? e.message : "change failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="recipe-section">
      <h2>Password</h2>
      <p className="muted">
        Changing your password signs you out of every other browser and device. This one stays signed in.
      </p>
      <form className="profile-form" onSubmit={onSubmit}>
        <label className="field">
          <span>Current password</span>
          <input
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            required
          />
        </label>
        <label className="field">
          <span>New password</span>
          <input
            type="password"
            autoComplete="new-password"
            minLength={8}
            value={next1}
            onChange={(e) => setNext1(e.target.value)}
            required
          />
        </label>
        <label className="field">
          <span>Confirm new password</span>
          <input
            type="password"
            autoComplete="new-password"
            minLength={8}
            value={next2}
            onChange={(e) => setNext2(e.target.value)}
            required
          />
        </label>
        {error && <p className="error">{error}</p>}
        {okMsg && !error && <p className="muted">{okMsg}</p>}
        <div className="form-actions">
          <button type="submit" disabled={busy || !current || !next1 || !next2}>
            {busy ? "Changing…" : "Change password"}
          </button>
        </div>
      </form>
    </section>
  );
}

// --- Danger zone ----------------------------------------------------------

function DangerSection({
  username,
  onDeleted,
}: {
  username: string;
  onDeleted: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [phrase, setPhrase] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Typed-confirmation phrase. The username being part of the phrase is
  // intentional — copy-paste from another app's "delete account" flow
  // doesn't accidentally match.
  const expectedPhrase = `delete ${username}`;
  const ready = password.length > 0 && phrase === expectedPhrase;

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!ready) {
      setError(`Type "${expectedPhrase}" and your password to confirm.`);
      return;
    }
    setBusy(true);
    try {
      await api.delete("/api/users/me", { password });
      onDeleted();
    } catch (e) {
      // 403 here is the admin-handoff guard — surface the message as-is
      // since the server's wording is already clear.
      const msg =
        e instanceof ApiError ? e.message : e instanceof Error ? e.message : "delete failed";
      setError(msg);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="recipe-section danger-zone">
      <h2>Danger zone</h2>
      {!open ? (
        <>
          <p className="muted">
            Permanently anonymize your account. Your recipes, batches, and comments stay on the
            instance attributed to a placeholder; everything else (sessions, notification prefs,
            push subscriptions, exports) is wiped. This cannot be undone from inside the app.
          </p>
          <div className="form-actions">
            <button type="button" className="danger" onClick={() => setOpen(true)}>
              Delete my account…
            </button>
          </div>
        </>
      ) : (
        <form className="profile-form" onSubmit={onSubmit}>
          <p className="muted">
            Type <code>{expectedPhrase}</code> below and enter your password to confirm.
            Admin accounts must hand off the role first.
          </p>
          <label className="field">
            <span>Confirmation phrase</span>
            <input
              type="text"
              value={phrase}
              onChange={(e) => setPhrase(e.target.value)}
              placeholder={expectedPhrase}
              autoComplete="off"
            />
          </label>
          <label className="field">
            <span>Password</span>
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          </label>
          {error && <p className="error">{error}</p>}
          <div className="form-actions">
            <button type="submit" className="danger" disabled={busy || !ready}>
              {busy ? "Deleting…" : "Delete my account permanently"}
            </button>
            <button
              type="button"
              className="cancel-link"
              onClick={() => {
                setOpen(false);
                setPassword("");
                setPhrase("");
                setError(null);
              }}
            >
              Cancel
            </button>
          </div>
        </form>
      )}
    </section>
  );
}
