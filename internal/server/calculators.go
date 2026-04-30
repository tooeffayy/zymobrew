package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"zymobrew/internal/calc"
)

// Calculators are intentionally public — they're stateless math, useful on
// instance landing pages and recipe-builder UIs before a user is signed
// in. There's no DB access and the work is bounded, so they ride on the
// global Timeout/Recoverer middleware without their own rate limit.

type abvRequest struct {
	OG      float64 `json:"og"`
	FG      float64 `json:"fg"`
	Formula string  `json:"formula,omitempty"`
}

type abvResponse struct {
	ABVPercent float64 `json:"abv_percent"`
	Formula    string  `json:"formula"`
}

func (s *Server) handleCalcABV(w http.ResponseWriter, r *http.Request) {
	var req abvRequest
	if !decodeCalcRequest(w, r, &req) {
		return
	}
	formula := calc.ABVFormula(req.Formula)
	if formula == "" {
		formula = calc.ABVFormulaAlternative
	}
	abv, err := calc.ABV(req.OG, req.FG, formula)
	if err != nil {
		writeCalcError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, abvResponse{ABVPercent: abv, Formula: string(formula)})
}

type predictedFGRequest struct {
	OG                 float64 `json:"og"`
	AttenuationPercent float64 `json:"attenuation_percent"`
}

type predictedFGResponse struct {
	FG         float64 `json:"fg"`
	ABVPercent float64 `json:"abv_percent"`
}

func (s *Server) handleCalcPredictedFG(w http.ResponseWriter, r *http.Request) {
	var req predictedFGRequest
	if !decodeCalcRequest(w, r, &req) {
		return
	}
	fg, abv, err := calc.PredictedFG(req.OG, req.AttenuationPercent)
	if err != nil {
		writeCalcError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, predictedFGResponse{FG: fg, ABVPercent: abv})
}

type honeyWeightRequest struct {
	TargetOG      float64 `json:"target_og"`
	BatchVolumeL  float64 `json:"batch_volume_l"`
	HoneyPPG      float64 `json:"honey_ppg,omitempty"`
}

type honeyWeightResponse struct {
	HoneyKG  float64 `json:"honey_kg"`
	HoneyLB  float64 `json:"honey_lb"`
	HoneyPPG float64 `json:"honey_ppg"`
}

func (s *Server) handleCalcHoneyWeight(w http.ResponseWriter, r *http.Request) {
	var req honeyWeightRequest
	if !decodeCalcRequest(w, r, &req) {
		return
	}
	ppg := req.HoneyPPG
	if ppg <= 0 {
		ppg = calc.HoneyPPGDefault
	}
	kg, lb, err := calc.HoneyWeight(req.TargetOG, req.BatchVolumeL, ppg)
	if err != nil {
		writeCalcError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, honeyWeightResponse{HoneyKG: kg, HoneyLB: lb, HoneyPPG: ppg})
}

type pitchRateRequest struct {
	OG           float64 `json:"og"`
	BatchVolumeL float64 `json:"batch_volume_l"`
	PitchFactor  float64 `json:"pitch_factor,omitempty"`
}

type pitchRateResponse struct {
	CellsBillion  float64 `json:"cells_billion"`
	DryYeastGrams float64 `json:"dry_yeast_grams"`
	LiquidPacks   float64 `json:"liquid_packs"`
	PitchFactor   float64 `json:"pitch_factor"`
}

func (s *Server) handleCalcPitchRate(w http.ResponseWriter, r *http.Request) {
	var req pitchRateRequest
	if !decodeCalcRequest(w, r, &req) {
		return
	}
	factor := req.PitchFactor
	if factor <= 0 {
		factor = calc.PitchFactorDefault
	}
	cellsB, dryG, packs, err := calc.PitchRate(req.OG, req.BatchVolumeL, factor)
	if err != nil {
		writeCalcError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pitchRateResponse{
		CellsBillion:  cellsB,
		DryYeastGrams: dryG,
		LiquidPacks:   packs,
		PitchFactor:   factor,
	})
}

func decodeCalcRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return false
	}
	return true
}

// writeCalcError maps calc.ErrInvalidInput to 400 and surfaces the inner
// message; anything else is treated as 500 (we don't expect any).
func writeCalcError(w http.ResponseWriter, err error) {
	if errors.Is(err, calc.ErrInvalidInput) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": unwrapInvalidMessage(err)})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

// unwrapInvalidMessage pulls the human message out of an
// errors.Join(ErrInvalidInput, msg) — stripping the sentinel prefix so
// the API response says "og must be ..." not "invalid input\nog must
// be ...".
func unwrapInvalidMessage(err error) string {
	type unwrapper interface{ Unwrap() []error }
	if u, ok := err.(unwrapper); ok {
		for _, e := range u.Unwrap() {
			if !errors.Is(e, calc.ErrInvalidInput) {
				return e.Error()
			}
		}
	}
	return err.Error()
}
