import { ReactNode, createContext, useContext, useEffect, useState } from "react";

import { ApiError, UserPreferences, api } from "./api";
import { useAuth } from "./auth";

// Server-backed user preferences (degree_units + timezone). The hook below
// preserves the `[unit, setUnit]` shape the rest of the app already imports —
// callers that only read the unit (`const [tempUnit] = useTemperatureUnit()`)
// don't change. Setting goes through the provider, which optimistically
// updates context state then PATCHes /api/me/prefs.

export type TempUnit = "C" | "F";

interface PreferencesCtx {
  tempUnit: TempUnit;
  timezone: string;
  setTempUnit: (u: TempUnit) => Promise<void>;
  setTimezone: (tz: string) => Promise<void>;
}

const DEFAULTS: UserPreferences = { degree_units: "C", timezone: "UTC" };

const Ctx = createContext<PreferencesCtx | null>(null);

export function PreferencesProvider({ children }: { children: ReactNode }) {
  const { state } = useAuth();
  const [prefs, setPrefs] = useState<UserPreferences>(DEFAULTS);

  // Fetch when auth flips to authed; reset to defaults on logout so the next
  // user lands on a clean slate rather than the previous account's prefs.
  useEffect(() => {
    if (state.status !== "authed") {
      setPrefs(DEFAULTS);
      return;
    }
    api
      .get<UserPreferences>("/api/me/prefs")
      .then((p) => setPrefs(p))
      .catch((e) => {
        // 401 is benign — auth provider will flip us to "anon" shortly.
        // Other errors: keep defaults, user can still toggle and resave.
        if (e instanceof ApiError && e.status === 401) return;
      });
  }, [state.status]);

  const setTempUnit = async (u: TempUnit) => {
    const prev = prefs.degree_units;
    setPrefs((p) => ({ ...p, degree_units: u }));
    try {
      const updated = await api.patch<UserPreferences>("/api/me/prefs", { degree_units: u });
      setPrefs(updated);
    } catch (e) {
      setPrefs((p) => ({ ...p, degree_units: prev }));
      throw e;
    }
  };

  const setTimezone = async (tz: string) => {
    const prev = prefs.timezone;
    setPrefs((p) => ({ ...p, timezone: tz }));
    try {
      const updated = await api.patch<UserPreferences>("/api/me/prefs", { timezone: tz });
      setPrefs(updated);
    } catch (e) {
      setPrefs((p) => ({ ...p, timezone: prev }));
      throw e;
    }
  };

  return (
    <Ctx.Provider value={{ tempUnit: prefs.degree_units, timezone: prefs.timezone, setTempUnit, setTimezone }}>
      {children}
    </Ctx.Provider>
  );
}

// Outside a provider (e.g. /login, /register chrome) the hook returns the
// default unit and a no-op setter so components stay renderable.
export function useTemperatureUnit(): [TempUnit, (u: TempUnit) => Promise<void>] {
  const ctx = useContext(Ctx);
  if (!ctx) return ["C", async () => {}];
  return [ctx.tempUnit, ctx.setTempUnit];
}

export function usePreferences(): PreferencesCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("usePreferences must be used within PreferencesProvider");
  return ctx;
}

export const cToF = (c: number) => c * 9 / 5 + 32;
export const fToC = (f: number) => (f - 32) * 5 / 9;

export function tempLabel(unit: TempUnit): string {
  return unit === "F" ? "°F" : "°C";
}

export function fromCelsius(c: number, unit: TempUnit): number {
  return unit === "F" ? cToF(c) : c;
}

export function toCelsius(value: number, unit: TempUnit): number {
  return unit === "F" ? fToC(value) : value;
}
