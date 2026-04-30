// Package calc implements the four mead-focused brewing calculators that
// were deferred from Phase 3: ABV, predicted FG from yeast attenuation,
// honey weight from a target OG + batch volume, and yeast pitch rate.
//
// All functions are pure — no DB, no I/O — so they are safe to call from
// HTTP handlers without context plumbing. Inputs are validated; impossible
// or unphysical values return ErrInvalidInput rather than NaN/Inf so
// handlers can map cleanly to 400.
package calc

import (
	"errors"
	"math"
)

var ErrInvalidInput = errors.New("invalid input")

// Defaults tuned for mead. The recipes/batches schema is mead-only at the
// API boundary today; cider/wine/beer get their own factors when those
// brew types are unblocked (Phase 5+).
const (
	// HoneyPPGDefault is the standard "points per gallon per pound" yield
	// of typical wildflower honey. Real values vary 33–38 by floral
	// source; 35 is the community-accepted midpoint.
	HoneyPPGDefault = 35.0
	// SugarPPGDefault is the PPG of pure cane sugar (sucrose). Dextrose
	// runs ~46 as well; honey-PPG for cider/wine isn't a useful default
	// because those brewers chaptalize with sucrose far more than honey.
	SugarPPGDefault = 46.0
	// PitchFactorDefault is the pitch rate in million cells / mL / °P.
	// 0.75 matches the ale baseline; mead is often pitched at this rate
	// or higher because honey is nutrient-poor — callers can override.
	PitchFactorDefault = 0.75
	// DryYeastCellsPerGramBillion is viable cells per gram of dry yeast
	// (Lalvin et al. publish ~10×10⁹/g viable on a fresh sachet).
	DryYeastCellsPerGramBillion = 10.0
	// LiquidPackCellsBillion is the standard "100 billion cells, fresh"
	// figure used by White Labs / Wyeast smack packs. Older packs
	// degrade — callers doing real pitch math should derate themselves.
	LiquidPackCellsBillion = 100.0

	gallonsPerLiter = 0.26417205235814845
	kgPerPound      = 0.45359237
)

// ABVFormula selects the alcohol-by-volume formula. The simple formula
// underestimates at the high gravities mead routinely hits (OG > 1.080),
// so "alternative" is the default for this app.
type ABVFormula string

const (
	ABVFormulaSimple      ABVFormula = "simple"
	ABVFormulaAlternative ABVFormula = "alternative"
)

// ABV computes alcohol by volume (percent) from original and final gravity.
//
//   - "simple": (OG - FG) * 131.25 — fine through ~1.070 OG, drifts low above.
//   - "alternative": ((76.08*(OG-FG)/(1.775-OG)) * (FG/0.794)) — the
//     community-standard high-gravity correction; tracks measured ABV
//     better at mead-strength worts.
//
// FG must be < OG; matching/inverted gravities are rejected (no
// fermentation happened, or the inputs are swapped).
func ABV(og, fg float64, formula ABVFormula) (float64, error) {
	if err := validateGravity(og, "og"); err != nil {
		return 0, err
	}
	if err := validateGravity(fg, "fg"); err != nil {
		return 0, err
	}
	if fg >= og {
		return 0, errInvalid("fg must be less than og")
	}
	switch formula {
	case "", ABVFormulaAlternative:
		// (1.775 - OG) is the only divisor that can blow up; OG is bounded
		// above by validateGravity (max 1.200) so this stays positive.
		return ((76.08 * (og - fg) / (1.775 - og)) * (fg / 0.794)), nil
	case ABVFormulaSimple:
		return (og - fg) * 131.25, nil
	default:
		return 0, errInvalid("unknown formula")
	}
}

// PredictedFG returns the final gravity a yeast would reach given an
// apparent attenuation percentage (0–100). Used pre-pitch to size a brew
// for a target ABV before committing to a yeast strain.
//
// Also returns the simple-formula ABV the brewer would see at that FG,
// since that's the number they actually care about — saves a second call.
func PredictedFG(og, attenuationPct float64) (fg, abvPct float64, err error) {
	if err := validateGravity(og, "og"); err != nil {
		return 0, 0, err
	}
	if math.IsNaN(attenuationPct) || math.IsInf(attenuationPct, 0) || attenuationPct < 0 || attenuationPct > 100 {
		return 0, 0, errInvalid("attenuation_percent must be 0..100")
	}
	fg = og - (og-1.0)*(attenuationPct/100.0)
	abvPct = (og - fg) * 131.25
	return fg, abvPct, nil
}

// HoneyWeight returns the honey to add to hit target_og for the given
// batch volume, using the points-per-gallon-per-pound model. Volume is
// in liters (the schema's unit) but honey is reported in both kg and lbs
// because brewers buy honey in either.
//
// honeyPPG <= 0 falls back to HoneyPPGDefault. The function does not
// account for honey volume displacement — at typical mead ratios that's
// a 2–3% effect, well within the variance of honey's own gravity points.
func HoneyWeight(targetOG, batchVolumeL, honeyPPG float64) (kg, lb float64, err error) {
	if err := validateGravity(targetOG, "target_og"); err != nil {
		return 0, 0, err
	}
	if targetOG <= 1.0 {
		return 0, 0, errInvalid("target_og must be greater than 1.0")
	}
	if batchVolumeL <= 0 {
		return 0, 0, errInvalid("batch_volume_l must be positive")
	}
	if honeyPPG <= 0 {
		honeyPPG = HoneyPPGDefault
	}
	gravityPoints := (targetOG - 1.0) * 1000.0
	volumeGal := batchVolumeL * gallonsPerLiter
	lb = (gravityPoints * volumeGal) / honeyPPG
	kg = lb * kgPerPound
	return kg, lb, nil
}

// SugarWeight returns the chaptalization sugar (cane / dextrose / etc.)
// to add to hit target_og for the given batch volume. Same PPG model as
// HoneyWeight but with a sucrose-tuned default; cider and wine recipes
// use this when boosting an under-strength juice.
//
// sugarPPG <= 0 falls back to SugarPPGDefault.
func SugarWeight(targetOG, batchVolumeL, sugarPPG float64) (kg, lb float64, err error) {
	if err := validateGravity(targetOG, "target_og"); err != nil {
		return 0, 0, err
	}
	if targetOG <= 1.0 {
		return 0, 0, errInvalid("target_og must be greater than 1.0")
	}
	if batchVolumeL <= 0 {
		return 0, 0, errInvalid("batch_volume_l must be positive")
	}
	if sugarPPG <= 0 {
		sugarPPG = SugarPPGDefault
	}
	gravityPoints := (targetOG - 1.0) * 1000.0
	volumeGal := batchVolumeL * gallonsPerLiter
	lb = (gravityPoints * volumeGal) / sugarPPG
	kg = lb * kgPerPound
	return kg, lb, nil
}

// PitchRate computes the cell count needed for a healthy fermentation,
// plus the matching dry-yeast grams and liquid-pack count. pitchFactor
// is in million cells / mL / °P; <= 0 falls back to PitchFactorDefault.
//
// We use the standard Plato cubic (°P = -668.962 + 1262.45·SG -
// 776.43·SG² + 182.94·SG³) instead of the popular (SG-1)*1000/4
// linear approximation — the linear form drifts ~5% high by OG 1.100
// and mead routinely lives in that range.
func PitchRate(og, batchVolumeL, pitchFactor float64) (cellsBillion, dryYeastG, liquidPacks float64, err error) {
	if err := validateGravity(og, "og"); err != nil {
		return 0, 0, 0, err
	}
	if batchVolumeL <= 0 {
		return 0, 0, 0, errInvalid("batch_volume_l must be positive")
	}
	if pitchFactor <= 0 {
		pitchFactor = PitchFactorDefault
	}
	plato := platoFromSG(og)
	volumeML := batchVolumeL * 1000.0
	cellsMillion := pitchFactor * volumeML * plato
	cellsBillion = cellsMillion / 1000.0
	dryYeastG = cellsBillion / DryYeastCellsPerGramBillion
	liquidPacks = cellsBillion / LiquidPackCellsBillion
	return cellsBillion, dryYeastG, liquidPacks, nil
}

func platoFromSG(sg float64) float64 {
	return -668.962 + 1262.45*sg - 776.43*math.Pow(sg, 2) + 182.94*math.Pow(sg, 3)
}

func validateGravity(g float64, name string) error {
	if math.IsNaN(g) || math.IsInf(g, 0) {
		return errInvalid(name + " must be a finite number")
	}
	// 0.990 covers fully-attenuated dry meads (FG can dip below 1.000);
	// 1.200 caps the upper end of any fermentable wort/must we'll see.
	if g < 0.990 || g > 1.200 {
		return errInvalid(name + " must be between 0.990 and 1.200")
	}
	return nil
}

func errInvalid(msg string) error {
	return errors.Join(ErrInvalidInput, errors.New(msg))
}
