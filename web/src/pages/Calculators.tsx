import { ReactNode, useEffect, useState } from "react";

import { api } from "../api";

// Public, no-auth surface. Five stateless calculators wired straight to
// /api/calculators/*. Each card is independent — the user might be
// sizing honey for a planned batch, or back-checking ABV on a finished
// one, and shouldn't have to fill in the others.
//
// Inputs are auto-debounced (250ms) and re-fetched on change. We don't
// validate client-side beyond "the field parsed as a finite number" —
// the server returns 400 with a human message and we surface it as-is.

function useCalc<TRes>(
  endpoint: string,
  body: object | null,
): { result: TRes | null; error: string | null } {
  const [result, setResult] = useState<TRes | null>(null);
  const [error, setError] = useState<string | null>(null);
  // JSON-stringify the body so adding/removing optional fields (formula,
  // honey_ppg) is reflected in the dep array without us having to enumerate
  // every primitive at the call site.
  const bodyKey = body ? JSON.stringify(body) : null;

  useEffect(() => {
    if (!body) {
      setResult(null);
      setError(null);
      return;
    }
    let cancelled = false;
    const t = setTimeout(async () => {
      try {
        const data = await api.post<TRes>(endpoint, body);
        if (!cancelled) {
          setResult(data);
          setError(null);
        }
      } catch (e) {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : "calculation failed");
          setResult(null);
        }
      }
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
    // bodyKey is the stable representation of body; ESLint can't see through
    // JSON.stringify, but we know the dep is correct.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [endpoint, bodyKey]);

  return { result, error };
}

function parseNum(s: string): number | null {
  if (s.trim() === "") return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

interface CalcCardProps {
  title: string;
  help: string;
  fields: ReactNode;
  error: string | null;
  result: ReactNode | null;
}

function CalcCard({ title, help, fields, error, result }: CalcCardProps) {
  return (
    <section className="calc-card">
      <h2>{title}</h2>
      <p className="muted calc-help">{help}</p>
      <div className="calc-fields">{fields}</div>
      {error && <p className="error calc-error">{error}</p>}
      {!error && result && <div className="calc-result">{result}</div>}
    </section>
  );
}

function ResultRow({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="calc-result-row">
      <span className="calc-result-label">{label}</span>
      <span className="calc-result-value">
        {value}
        {sub && <span className="calc-result-sub"> {sub}</span>}
      </span>
    </div>
  );
}

// --- ABV ------------------------------------------------------------------

interface ABVResult { abv_percent: number; formula: string }

function ABVCalculator() {
  const [og, setOg] = useState("");
  const [fg, setFg] = useState("");
  const [formula, setFormula] = useState<"alternative" | "simple">("alternative");

  const ogN = parseNum(og);
  const fgN = parseNum(fg);
  const body = ogN !== null && fgN !== null ? { og: ogN, fg: fgN, formula } : null;
  const { result, error } = useCalc<ABVResult>("/api/calculators/abv", body);

  return (
    <CalcCard
      title="ABV"
      help="Alcohol by volume from original and final gravity. Defaults to the high-gravity formula — better at mead-strength worts where the simple form drifts low."
      fields={
        <>
          <label className="field">
            <span>Original gravity (OG)</span>
            <input type="number" inputMode="decimal" step="0.001"
              value={og} onChange={(e) => setOg(e.target.value)} placeholder="1.100" />
          </label>
          <label className="field">
            <span>Final gravity (FG)</span>
            <input type="number" inputMode="decimal" step="0.001"
              value={fg} onChange={(e) => setFg(e.target.value)} placeholder="1.005" />
          </label>
          <label className="field">
            <span>Formula</span>
            <select value={formula} onChange={(e) => setFormula(e.target.value as "alternative" | "simple")}>
              <option value="alternative">Alternative (high-gravity)</option>
              <option value="simple">Simple ((OG−FG) × 131.25)</option>
            </select>
          </label>
        </>
      }
      error={error}
      result={result && (
        <ResultRow label="ABV" value={`${result.abv_percent.toFixed(2)}%`} />
      )}
    />
  );
}

// --- Predicted FG ---------------------------------------------------------

interface PredictedFGResult { fg: number; abv_percent: number }

function PredictedFGCalculator() {
  const [og, setOg] = useState("");
  const [att, setAtt] = useState("");

  const ogN = parseNum(og);
  const attN = parseNum(att);
  const body = ogN !== null && attN !== null ? { og: ogN, attenuation_percent: attN } : null;
  const { result, error } = useCalc<PredictedFGResult>("/api/calculators/predicted-fg", body);

  return (
    <CalcCard
      title="Predicted FG"
      help="Estimate the final gravity from a yeast's apparent attenuation. Useful pre-pitch for sizing a brew to a target ABV before committing to a yeast strain."
      fields={
        <>
          <label className="field">
            <span>Original gravity (OG)</span>
            <input type="number" inputMode="decimal" step="0.001"
              value={og} onChange={(e) => setOg(e.target.value)} placeholder="1.100" />
          </label>
          <label className="field">
            <span>Apparent attenuation (%)</span>
            <input type="number" inputMode="decimal" step="0.1"
              value={att} onChange={(e) => setAtt(e.target.value)} placeholder="75" />
          </label>
        </>
      }
      error={error}
      result={result && (
        <>
          <ResultRow label="Predicted FG" value={result.fg.toFixed(3)} />
          <ResultRow label="Resulting ABV" value={`${result.abv_percent.toFixed(2)}%`} sub="(simple formula)" />
        </>
      )}
    />
  );
}

// --- Honey weight ---------------------------------------------------------

interface HoneyWeightResult { honey_kg: number; honey_lb: number; honey_ppg: number }

function HoneyWeightCalculator() {
  const [targetOG, setTargetOG] = useState("");
  const [volumeL, setVolumeL] = useState("");
  const [ppg, setPpg] = useState("");

  const targetOGN = parseNum(targetOG);
  const volumeLN = parseNum(volumeL);
  const ppgN = parseNum(ppg);
  const body = targetOGN !== null && volumeLN !== null
    ? { target_og: targetOGN, batch_volume_l: volumeLN, ...(ppgN !== null ? { honey_ppg: ppgN } : {}) }
    : null;
  const { result, error } = useCalc<HoneyWeightResult>("/api/calculators/honey-weight", body);

  return (
    <CalcCard
      title="Honey weight"
      help="How much honey to hit a target OG for a given batch volume. Default 35 PPG is the wildflower-honey midpoint; varietals run 33–38."
      fields={
        <>
          <label className="field">
            <span>Target OG</span>
            <input type="number" inputMode="decimal" step="0.001"
              value={targetOG} onChange={(e) => setTargetOG(e.target.value)} placeholder="1.100" />
          </label>
          <label className="field">
            <span>Batch volume (L)</span>
            <input type="number" inputMode="decimal" step="0.1"
              value={volumeL} onChange={(e) => setVolumeL(e.target.value)} placeholder="5" />
          </label>
          <label className="field">
            <span>Honey PPG <span className="muted">(optional)</span></span>
            <input type="number" inputMode="decimal" step="0.1"
              value={ppg} onChange={(e) => setPpg(e.target.value)} placeholder="35" />
          </label>
        </>
      }
      error={error}
      result={result && (
        <>
          <ResultRow label="Honey" value={`${result.honey_kg.toFixed(2)} kg`} sub={`(${result.honey_lb.toFixed(2)} lb)`} />
          <ResultRow label="PPG used" value={result.honey_ppg.toFixed(1)} />
        </>
      )}
    />
  );
}

// --- Sugar weight ---------------------------------------------------------

interface SugarWeightResult { sugar_kg: number; sugar_lb: number; sugar_ppg: number }

function SugarWeightCalculator() {
  const [targetOG, setTargetOG] = useState("");
  const [volumeL, setVolumeL] = useState("");
  const [ppg, setPpg] = useState("");

  const targetOGN = parseNum(targetOG);
  const volumeLN = parseNum(volumeL);
  const ppgN = parseNum(ppg);
  const body = targetOGN !== null && volumeLN !== null
    ? { target_og: targetOGN, batch_volume_l: volumeLN, ...(ppgN !== null ? { sugar_ppg: ppgN } : {}) }
    : null;
  const { result, error } = useCalc<SugarWeightResult>("/api/calculators/sugar-weight", body);

  return (
    <CalcCard
      title="Sugar weight"
      help="Chaptalization sugar (cane / dextrose) to hit a target OG. Default 46 PPG is sucrose; cider and wine recipes use this when a juice's natural gravity falls short."
      fields={
        <>
          <label className="field">
            <span>Target OG</span>
            <input type="number" inputMode="decimal" step="0.001"
              value={targetOG} onChange={(e) => setTargetOG(e.target.value)} placeholder="1.080" />
          </label>
          <label className="field">
            <span>Batch volume (L)</span>
            <input type="number" inputMode="decimal" step="0.1"
              value={volumeL} onChange={(e) => setVolumeL(e.target.value)} placeholder="5" />
          </label>
          <label className="field">
            <span>Sugar PPG <span className="muted">(optional)</span></span>
            <input type="number" inputMode="decimal" step="0.1"
              value={ppg} onChange={(e) => setPpg(e.target.value)} placeholder="46" />
          </label>
        </>
      }
      error={error}
      result={result && (
        <>
          <ResultRow label="Sugar" value={`${result.sugar_kg.toFixed(2)} kg`} sub={`(${result.sugar_lb.toFixed(2)} lb)`} />
          <ResultRow label="PPG used" value={result.sugar_ppg.toFixed(1)} />
        </>
      )}
    />
  );
}

// --- Pitch rate -----------------------------------------------------------

interface PitchRateResult {
  cells_billion: number;
  dry_yeast_grams: number;
  liquid_packs: number;
  pitch_factor: number;
}

function PitchRateCalculator() {
  const [og, setOg] = useState("");
  const [volumeL, setVolumeL] = useState("");
  const [factor, setFactor] = useState("");

  const ogN = parseNum(og);
  const volumeLN = parseNum(volumeL);
  const factorN = parseNum(factor);
  const body = ogN !== null && volumeLN !== null
    ? { og: ogN, batch_volume_l: volumeLN, ...(factorN !== null ? { pitch_factor: factorN } : {}) }
    : null;
  const { result, error } = useCalc<PitchRateResult>("/api/calculators/pitch-rate", body);

  return (
    <CalcCard
      title="Pitch rate"
      help="Yeast cell count for a healthy fermentation, plus matching dry-yeast grams and liquid-pack count. Default factor 0.75 is the ale baseline; mead is often pitched at this rate or higher because honey is nutrient-poor."
      fields={
        <>
          <label className="field">
            <span>Original gravity (OG)</span>
            <input type="number" inputMode="decimal" step="0.001"
              value={og} onChange={(e) => setOg(e.target.value)} placeholder="1.100" />
          </label>
          <label className="field">
            <span>Batch volume (L)</span>
            <input type="number" inputMode="decimal" step="0.1"
              value={volumeL} onChange={(e) => setVolumeL(e.target.value)} placeholder="5" />
          </label>
          <label className="field">
            <span>Pitch factor <span className="muted">(M cells/mL/°P, optional)</span></span>
            <input type="number" inputMode="decimal" step="0.05"
              value={factor} onChange={(e) => setFactor(e.target.value)} placeholder="0.75" />
          </label>
        </>
      }
      error={error}
      result={result && (
        <>
          <ResultRow label="Cells needed" value={`${result.cells_billion.toFixed(1)} B`} />
          <ResultRow label="Dry yeast" value={`${result.dry_yeast_grams.toFixed(1)} g`} sub="(fresh sachet)" />
          <ResultRow label="Liquid packs" value={result.liquid_packs.toFixed(2)} sub="(100B/pack fresh)" />
          <ResultRow label="Factor used" value={result.pitch_factor.toFixed(2)} />
        </>
      )}
    />
  );
}

export function Calculators() {
  return (
    <div className="page calculators-page">
      <h1>Calculators</h1>
      <p className="page-intro muted">
        Brewing math without a recipe. Inputs update results as you type — tweak target OG to see honey shift, or back-check ABV on a finished batch.
      </p>
      <div className="calc-grid">
        <ABVCalculator />
        <PredictedFGCalculator />
        <HoneyWeightCalculator />
        <SugarWeightCalculator />
        <PitchRateCalculator />
      </div>
    </div>
  );
}
