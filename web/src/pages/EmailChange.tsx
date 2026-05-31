import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { api, ApiError } from "../api";

// Standalone, unauthenticated landing pages for the email-change links.
// They're reachable without a session because the link may be opened on a
// device where the user isn't signed in (and the cancel link must work for
// the legitimate owner of a hijacked session). The token in the query string
// is the sole capability.
//
// The mutation runs on an explicit button click rather than on mount, so an
// email client / link scanner that prefetches the URL can't silently confirm
// or cancel the change.

type Outcome = "idle" | "busy" | "done" | "error";

function TokenAction({
  title,
  intro,
  actionLabel,
  endpoint,
  doneMessage,
}: {
  title: string;
  intro: string;
  actionLabel: string;
  endpoint: string;
  doneMessage: string;
}) {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [state, setState] = useState<Outcome>("idle");
  const [error, setError] = useState<string | null>(null);

  const run = async () => {
    setState("busy");
    setError(null);
    try {
      await api.post(endpoint, { token });
      setState("done");
    } catch (e) {
      if (e instanceof ApiError && e.status === 400) {
        setError("This link is invalid or has expired.");
      } else if (e instanceof ApiError && e.status === 409) {
        setError("That email address has since been taken by another account.");
      } else {
        setError(e instanceof Error ? e.message : "request failed");
      }
      setState("error");
    }
  };

  return (
    <div className="auth-shell">
      <div className="card">
        <h1>{title}</h1>
        {!token && <p className="error">This link is missing its token.</p>}
        {state === "done" ? (
          <p>{doneMessage}</p>
        ) : (
          <>
            <p className="muted">{intro}</p>
            {error && <p className="error">{error}</p>}
            <button type="button" onClick={run} disabled={!token || state === "busy"}>
              {state === "busy" ? "Working…" : actionLabel}
            </button>
          </>
        )}
        <p className="muted">
          <Link to="/">Back to Zymo</Link>
        </p>
      </div>
    </div>
  );
}

export function EmailConfirm() {
  return (
    <TokenAction
      title="Confirm your new email"
      intro="Confirm that you want to use this address for your Zymo account. It becomes your sign-in and notification address."
      actionLabel="Confirm email change"
      endpoint="/api/auth/email/confirm"
      doneMessage="Your email has been updated. You can now sign in with the new address."
    />
  );
}

export function EmailCancel() {
  return (
    <TokenAction
      title="Cancel email change"
      intro="Cancel the pending change to your Zymo account email and keep your current address."
      actionLabel="Cancel the change"
      endpoint="/api/auth/email/cancel"
      doneMessage="The email change has been cancelled. Your address is unchanged."
    />
  );
}
