import { useMemo } from "react";
import {
  Chart as ChartJS,
  ChartOptions,
  LinearScale,
  LineElement,
  PointElement,
  TimeScale,
  Tooltip,
} from "chart.js";
import annotationPlugin from "chartjs-plugin-annotation";
import "chartjs-adapter-date-fns";
import {
  addDays, addHours, addMonths, addWeeks,
  startOfDay, startOfHour, startOfMonth, startOfWeek,
} from "date-fns";
import { Line } from "react-chartjs-2";

import { BatchEvent, Reading } from "../api";
import { TempUnit, fromCelsius, tempLabel, useTemperatureUnit } from "../units";

// One-shot registration. Chart.js is tree-shakeable — only the bits
// we actually use are pulled into the bundle.
ChartJS.register(
  LinearScale,
  TimeScale,
  PointElement,
  LineElement,
  Tooltip,
  annotationPlugin,
);

// Events that materially change the brew get a vertical marker so the
// brewer can see "gravity dropped after I racked." Other event kinds
// (note/photo/addition/etc.) would clutter the chart.
const KEY_EVENT_KINDS = new Set<BatchEvent["kind"]>(["pitch", "rack", "bottle"]);

type MetricKey = "gravity" | "temperature_c" | "ph";

type Metric = {
  key: MetricKey;
  label: string;
  unit: string;
  decimals: number;
  // Convert the raw stored value (always SI / canonical) into the unit
  // we want to display. Identity for gravity/pH; cToF for temperature
  // when the user prefers Fahrenheit.
  transform: (v: number) => number;
};

function buildMetrics(tempUnit: TempUnit): Metric[] {
  return [
    { key: "gravity", label: "Gravity", unit: "", decimals: 3, transform: (v) => v },
    {
      key: "temperature_c",
      label: "Temperature",
      unit: tempLabel(tempUnit),
      decimals: 1,
      transform: (v) => fromCelsius(v, tempUnit),
    },
    { key: "ph", label: "pH", unit: "", decimals: 2, transform: (v) => v },
  ];
}

export function ReadingsChart({
  readings, events,
}: {
  readings: Reading[];
  events: BatchEvent[];
}) {
  const [tempUnit] = useTemperatureUnit();
  const metrics = useMemo(() => buildMetrics(tempUnit), [tempUnit]);
  const present = useMemo(
    () => metrics.filter((m) => readings.some((r) => typeof r[m.key] === "number")),
    [metrics, readings],
  );
  const keyEvents = useMemo(
    () => events.filter((e) => KEY_EVENT_KINDS.has(e.kind)),
    [events],
  );

  if (readings.length === 0 || present.length === 0) return null;

  return (
    <div className="readings-charts">
      {present.map((m) => (
        <MetricChart
          key={m.key}
          metric={m}
          points={readings
            .filter((r) => typeof r[m.key] === "number")
            .map((r) => ({ x: +new Date(r.taken_at), y: m.transform(r[m.key] as number) }))
            .sort((a, b) => a.x - b.x)}
          events={keyEvents}
        />
      ))}
    </div>
  );
}

type Point = { x: number; y: number };

function MetricChart({
  metric, points, events,
}: {
  metric: Metric;
  points: Point[];
  events: BatchEvent[];
}) {
  // CSS-var lookup happens once per render — cheap, and lets the chart
  // pick up theme changes if dark mode lands later.
  const colors = useMemo(() => readChartColors(), []);

  // Pick a sensible time unit based on the data span. Chart.js's
  // auto-pick keeps "hour" as the unit and just spaces ticks further
  // apart, which produces "4 PM / 1 PM / 10 AM" labels for a multi-day
  // batch — readable, but the brewer can't tell which day they're on.
  const unit = pickTimeUnit(points);
  // Extend the axis one unit before/after the data so the brewer sees
  // a labeled tick on each side rather than a context-less first
  // reading sitting at the edge.
  const bounds = useMemo(() => paddedBounds(points, unit), [points, unit]);
  // Push suggestedMin/Max ~15% beyond the data extremes so Chart.js's
  // "nice tick" algorithm rounds to a clean tick beyond each edge
  // instead of pinning the data extreme directly to the first/last
  // gridline. Without this the temperature/pH charts often peak right
  // on a tick and look cramped; gravity gets it for free because its
  // data sits mid-range.
  const yBounds = useMemo(() => {
    if (points.length === 0) return { min: undefined, max: undefined };
    let min = points[0].y;
    let max = points[0].y;
    for (const p of points) {
      if (p.y < min) min = p.y;
      if (p.y > max) max = p.y;
    }
    const range = max - min;
    // For flat-line series, fall back to one display step so we still
    // float the data point off both edges.
    const pad = range > 0 ? range * 0.15 : Math.pow(10, -metric.decimals);
    return { min: min - pad, max: max + pad };
  }, [points, metric.decimals]);

  const data = useMemo(() => ({
    datasets: [
      {
        label: metric.label,
        data: points,
        borderColor: colors.accent,
        backgroundColor: colors.accent,
        borderWidth: 2,
        tension: 0.25,
        pointRadius: 3,
        pointHoverRadius: 5,
        spanGaps: true,
      },
    ],
  }), [metric.label, points, colors]);

  const options = useMemo<ChartOptions<"line">>(() => ({
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: "index", intersect: false },
    scales: {
      x: {
        type: "time",
        min: bounds.min,
        max: bounds.max,
        time: {
          unit,
          tooltipFormat: "PPp",
          displayFormats: {
            hour: "ha",
            day: "MMM d",
            week: "MMM d",
            month: "MMM yyyy",
          },
        },
        ticks: {
          align: "start",
          color: colors.muted,
          maxRotation: 0,
          // No maxTicksLimit — let Chart.js fit all unit ticks. The
          // bounds are already capped to a single unit of padding, so
          // a typical batch produces a digestible number of ticks.
          // autoSkipPadding governs when labels would overlap and need
          // skipping, which only kicks in for very long ranges.
          autoSkipPadding: 16,
        },
        grid: { color: colors.lineSoft, drawTicks: false },
        border: { display: false },
      },
      y: {
        suggestedMin: yBounds.min,
        suggestedMax: yBounds.max,
        ticks: {
          color: colors.muted,
          callback: (value) => Number(value).toFixed(metric.decimals),
        },
        grid: { color: colors.lineSoft, drawTicks: true },
        border: { display: false },
        offset: true,
      },
    },
    plugins: {
      legend: { display: false },
      tooltip: {
        backgroundColor: colors.surface,
        titleColor: colors.fg,
        bodyColor: colors.fg,
        borderColor: colors.line,
        borderWidth: 1,
        padding: 8,
        callbacks: {
          label: (ctx) => ` ${metric.label}: ${Number(ctx.parsed.y).toFixed(metric.decimals)}${metric.unit ? ` ${metric.unit}` : ""}`,
        },
      },
      annotation: {
        annotations: Object.fromEntries(
          events.map((e) => [
            e.id,
            {
              type: "line" as const,
              xMin: +new Date(e.occurred_at),
              xMax: +new Date(e.occurred_at),
              borderColor: colors.event,
              borderWidth: 1,
              borderDash: [3, 3],
              label: {
                display: true,
                content: e.kind.toUpperCase(),
                position: "end" as const,
                backgroundColor: "transparent",
                color: colors.muted,
                font: { size: 9, weight: 600 },
                padding: { top: 0, bottom: 4, left: 4, right: 4 },
              },
            },
          ]),
        ),
      },
    },
  }), [bounds, colors, events, metric, unit, yBounds]);

  return (
    <figure className="reading-chart">
      <figcaption className="reading-chart-label">
        {metric.label}{metric.unit ? ` (${metric.unit})` : ""}
      </figcaption>
      <div className="reading-chart-canvas">
        <Line data={data} options={options} />
      </div>
    </figure>
  );
}

type TimeUnit = "hour" | "day" | "week" | "month";

function pickTimeUnit(points: Point[]): TimeUnit {
  if (points.length < 2) return "day";
  const span = points[points.length - 1].x - points[0].x;
  const HOUR = 60 * 60 * 1000;
  const DAY = 24 * HOUR;
  if (span <= 2 * DAY) return "hour";
  if (span <= 60 * DAY) return "day";
  if (span <= 365 * DAY) return "week";
  return "month";
}

// Anchor the leading edge at the start of the first reading's unit
// (so the leftmost tick is the first reading's own date), and pad
// the trailing edge by one full unit so there's a labeled tick after
// the last reading for context.
function paddedBounds(points: Point[], unit: TimeUnit): { min: number; max: number } {
  const first = new Date(points[0].x);
  const last = new Date(points[points.length - 1].x);
  switch (unit) {
    case "hour":
      return { min: startOfHour(first).getTime(), max: addHours(startOfHour(last), 1).getTime() };
    case "day":
      return { min: startOfDay(first).getTime(), max: addDays(startOfDay(last), 1).getTime() };
    case "week":
      return { min: startOfWeek(first).getTime(), max: addWeeks(startOfWeek(last), 1).getTime() };
    case "month":
      return { min: startOfMonth(first).getTime(), max: addMonths(startOfMonth(last), 1).getTime() };
  }
}

// Pull palette from the same OKLCH tokens defined in styles.css so the
// chart reads visually identical to the rest of the page. Computed
// values are valid CSS color strings; canvas's strokeStyle parses
// oklch() in modern browsers.
function readChartColors() {
  const get = (name: string) =>
    getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return {
    accent: get("--accent") || "#b07330",
    fg: get("--fg") || "#262019",
    muted: get("--muted") || "#807468",
    line: get("--line") || "#e3dcd2",
    lineSoft: get("--line-soft") || "#eee8df",
    surface: get("--surface") || "#ffffff",
    // Slightly translucent fg-soft so the dashed event marker reads
    // as secondary against the line.
    event: "rgba(80, 64, 50, 0.45)",
  };
}
