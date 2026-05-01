import { DateInput, DateSegment, TimeField } from "react-aria-components";
import { Time, parseTime } from "@internationalized/date";

// Thin wrapper over react-aria's TimeField that round-trips a "HH:mm"
// string. We keep the string boundary because the rest of the app
// stores form state as strings and converts to RFC3339 at submit time
// — adopting Time everywhere would touch a lot of code for no win.
//
// react-aria gives us: consistent rendering across browsers (Safari
// ships nothing for `<input type="time">`), 12/24h locale awareness,
// keyboard arrow-step on each segment, and proper screen-reader
// announcements. The downside is ~20KB of JS for the picker bundle.
export function TimeInput({
  value, onChange, ariaLabel,
}: {
  value: string;
  onChange: (v: string) => void;
  ariaLabel: string;
}) {
  const parsed = value ? safeParse(value) : null;

  return (
    <TimeField
      aria-label={ariaLabel}
      value={parsed}
      // hourCycle 12 vs 24 is left to the user's locale; react-aria
      // reads navigator.language by default, matching the native
      // <input type="time"> behavior we replaced.
      onChange={(next) => {
        if (!next) {
          onChange("");
          return;
        }
        const pad = (n: number) => String(n).padStart(2, "0");
        onChange(`${pad(next.hour)}:${pad(next.minute)}`);
      }}
    >
      <DateInput className="time-input">
        {(segment) => (
          <DateSegment segment={segment} className="time-input-segment" />
        )}
      </DateInput>
    </TimeField>
  );
}

// parseTime throws on malformed input; we'd rather render an empty
// field than crash the form when the value drifts (e.g. seed data
// that's "9:00" instead of "09:00").
function safeParse(s: string): Time | null {
  try {
    return parseTime(s);
  } catch {
    return null;
  }
}
