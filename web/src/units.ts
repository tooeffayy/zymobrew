import { useEffect, useState } from "react";

// Display-only preference. The server always stores Celsius; conversion
// happens at the UI surface (input form sends C, display + chart convert
// out of C). Kept in localStorage so it's per-device — a brewer might
// log in metric on their phone and imperial on their laptop.

export type TempUnit = "C" | "F";

const STORAGE_KEY = "zymo:temp_unit";
// Custom event for same-tab sync. The native `storage` event only fires
// in *other* tabs, so the toggle on /me wouldn't update an open BatchDetail
// in the same tab without this.
const CHANGE_EVENT = "zymo:temp_unit_change";

function read(): TempUnit {
  if (typeof window === "undefined") return "C";
  return window.localStorage.getItem(STORAGE_KEY) === "F" ? "F" : "C";
}

export function useTemperatureUnit(): [TempUnit, (u: TempUnit) => void] {
  const [unit, setUnitState] = useState<TempUnit>(read);

  useEffect(() => {
    const refresh = () => setUnitState(read());
    window.addEventListener(CHANGE_EVENT, refresh);
    window.addEventListener("storage", refresh);
    return () => {
      window.removeEventListener(CHANGE_EVENT, refresh);
      window.removeEventListener("storage", refresh);
    };
  }, []);

  const setUnit = (u: TempUnit) => {
    window.localStorage.setItem(STORAGE_KEY, u);
    window.dispatchEvent(new Event(CHANGE_EVENT));
  };

  return [unit, setUnit];
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
