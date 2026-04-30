package calc

import (
	"errors"
	"math"
	"testing"
)

func approxEq(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestABV_Alternative(t *testing.T) {
	got, err := ABV(1.100, 1.020, ABVFormulaAlternative)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ((76.08 * 0.080) / 0.675) * (1.020/0.794) ≈ 11.583
	if !approxEq(got, 11.583, 0.01) {
		t.Errorf("got %.4f, want ~11.583", got)
	}
}

func TestABV_Simple(t *testing.T) {
	got, err := ABV(1.100, 1.020, ABVFormulaSimple)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEq(got, 10.5, 0.001) {
		t.Errorf("got %.4f, want 10.5", got)
	}
}

func TestABV_DefaultsToAlternative(t *testing.T) {
	def, err := ABV(1.100, 1.020, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	alt, _ := ABV(1.100, 1.020, ABVFormulaAlternative)
	if def != alt {
		t.Errorf("empty formula = %.4f, alternative = %.4f", def, alt)
	}
}

func TestABV_RejectsBadInputs(t *testing.T) {
	cases := []struct {
		name    string
		og, fg  float64
		formula ABVFormula
	}{
		{"fg equals og", 1.050, 1.050, ABVFormulaSimple},
		{"fg above og", 1.020, 1.030, ABVFormulaSimple},
		{"og out of range high", 1.500, 1.020, ABVFormulaSimple},
		{"og out of range low", 0.5, 1.020, ABVFormulaSimple},
		{"fg out of range", 1.100, 0.5, ABVFormulaSimple},
		{"unknown formula", 1.100, 1.020, "fancy"},
		{"NaN og", math.NaN(), 1.020, ABVFormulaSimple},
		{"Inf fg", 1.100, math.Inf(1), ABVFormulaSimple},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ABV(c.og, c.fg, c.formula)
			if err == nil {
				t.Errorf("expected error, got nil")
				return
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("error not wrapping ErrInvalidInput: %v", err)
			}
		})
	}
}

func TestPredictedFG(t *testing.T) {
	fg, abv, err := PredictedFG(1.100, 75)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEq(fg, 1.025, 0.0001) {
		t.Errorf("fg got %.5f, want 1.02500", fg)
	}
	// Simple ABV: (1.100 - 1.025) * 131.25 = 9.84375
	if !approxEq(abv, 9.84375, 0.001) {
		t.Errorf("abv got %.5f, want 9.84375", abv)
	}
}

func TestPredictedFG_BoundaryAttenuation(t *testing.T) {
	// 0% attenuation → FG == OG
	fg, _, err := PredictedFG(1.080, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEq(fg, 1.080, 0.0001) {
		t.Errorf("0%% attenuation: fg=%.4f, want 1.080", fg)
	}
	// 100% attenuation → FG == 1.000
	fg, _, err = PredictedFG(1.080, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEq(fg, 1.000, 0.0001) {
		t.Errorf("100%% attenuation: fg=%.4f, want 1.000", fg)
	}
}

func TestPredictedFG_RejectsBadAttenuation(t *testing.T) {
	for _, att := range []float64{-1, 100.5, math.NaN()} {
		_, _, err := PredictedFG(1.080, att)
		if err == nil {
			t.Errorf("attenuation %v: expected error", att)
		}
	}
}

func TestHoneyWeight(t *testing.T) {
	// 19L at OG 1.100 with 35 PPG honey ≈ 14.34 lb / 6.50 kg.
	kg, lb, err := HoneyWeight(1.100, 19, 35)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEq(lb, 14.34, 0.05) {
		t.Errorf("lb got %.4f, want ~14.34", lb)
	}
	if !approxEq(kg, 6.50, 0.05) {
		t.Errorf("kg got %.4f, want ~6.50", kg)
	}
}

func TestHoneyWeight_DefaultPPG(t *testing.T) {
	a, _, _ := HoneyWeight(1.090, 19, 0)
	b, _, _ := HoneyWeight(1.090, 19, HoneyPPGDefault)
	if a != b {
		t.Errorf("ppg=0 should fall back to default: got %v vs %v", a, b)
	}
}

func TestHoneyWeight_RejectsBadInputs(t *testing.T) {
	cases := []struct {
		name             string
		og, vol, ppg     float64
	}{
		{"og at 1.0", 1.0, 19, 35},
		{"og below 1.0", 0.999, 19, 35},
		{"zero volume", 1.090, 0, 35},
		{"negative volume", 1.090, -5, 35},
		{"og out of range", 1.500, 19, 35},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := HoneyWeight(c.og, c.vol, c.ppg); err == nil {
				t.Errorf("expected error")
			}
		})
	}
}

func TestSugarWeight(t *testing.T) {
	// 19L at OG 1.080 with 46 PPG sucrose. Hand-check:
	// gravityPoints=80; volumeGal = 19 * 0.26417 = 5.0193; lb = (80*5.0193)/46 = 8.7292.
	kg, lb, err := SugarWeight(1.080, 19, 46)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEq(lb, 8.729, 0.01) {
		t.Errorf("lb got %.4f, want ~8.729", lb)
	}
	if !approxEq(kg, 3.959, 0.01) {
		t.Errorf("kg got %.4f, want ~3.959", kg)
	}
}

func TestSugarWeight_DefaultPPG(t *testing.T) {
	a, _, _ := SugarWeight(1.080, 19, 0)
	b, _, _ := SugarWeight(1.080, 19, SugarPPGDefault)
	if a != b {
		t.Errorf("ppg=0 should fall back to default: got %v vs %v", a, b)
	}
}

func TestSugarWeight_RejectsBadInputs(t *testing.T) {
	if _, _, err := SugarWeight(1.0, 19, 46); err == nil {
		t.Error("expected error for og at 1.0")
	}
	if _, _, err := SugarWeight(1.080, 0, 46); err == nil {
		t.Error("expected error for zero volume")
	}
	if _, _, err := SugarWeight(1.500, 19, 46); err == nil {
		t.Error("expected error for og out of range")
	}
}

func TestPitchRate(t *testing.T) {
	cellsB, dryG, liquid, err := PitchRate(1.100, 19, 0.75)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// °P (poly) at SG=1.100 ≈ 23.79; 0.75 * 19000 * 23.79 ≈ 339000 M = 339 B.
	if !approxEq(cellsB, 339.0, 1.0) {
		t.Errorf("cellsBillion got %.2f, want ~339", cellsB)
	}
	if !approxEq(dryG, 33.9, 0.5) {
		t.Errorf("dryYeastG got %.2f, want ~33.9", dryG)
	}
	if !approxEq(liquid, 3.39, 0.05) {
		t.Errorf("liquidPacks got %.3f, want ~3.39", liquid)
	}
}

func TestPitchRate_DefaultsFactor(t *testing.T) {
	a, _, _, _ := PitchRate(1.080, 19, 0)
	b, _, _, _ := PitchRate(1.080, 19, PitchFactorDefault)
	if a != b {
		t.Errorf("factor=0 should fall back to default")
	}
}

func TestPitchRate_RejectsBadInputs(t *testing.T) {
	if _, _, _, err := PitchRate(1.500, 19, 0.75); err == nil {
		t.Error("expected error for og out of range")
	}
	if _, _, _, err := PitchRate(1.080, 0, 0.75); err == nil {
		t.Error("expected error for zero volume")
	}
}
