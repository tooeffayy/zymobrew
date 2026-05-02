package calc_test

import (
	"math"
	"testing"

	"zymobrew/internal/calc"
)

func TestFamily(t *testing.T) {
	cases := []struct {
		unit string
		want calc.UnitFamily
	}{
		{"g", calc.FamilyMass},
		{"GRAMS", calc.FamilyMass},
		{"kg", calc.FamilyMass},
		{"oz", calc.FamilyMass},
		{"lb", calc.FamilyMass},
		{"Lbs.", calc.FamilyMass},
		{"mL", calc.FamilyVolume},
		{"L", calc.FamilyVolume},
		{"liter", calc.FamilyVolume},
		{"tsp", calc.FamilyVolume},
		{"fl oz", calc.FamilyVolume},
		{"FL. OZ.", calc.FamilyVolume},
		{"gal", calc.FamilyVolume},
		{"stick", calc.FamilyCount},
		{"pack", calc.FamilyCount},
		{"each", calc.FamilyCount},
		{"", calc.FamilyUnknown},
		{"narwhal", calc.FamilyUnknown},
	}
	for _, c := range cases {
		t.Run(c.unit, func(t *testing.T) {
			if got := calc.Family(c.unit); got != c.want {
				t.Fatalf("Family(%q) = %q, want %q", c.unit, got, c.want)
			}
		})
	}
}

func TestConvert(t *testing.T) {
	cases := []struct {
		name       string
		amount     float64
		from, to   string
		want       float64
		wantOK     bool
		tolerance  float64 // absolute tolerance (default 1e-6 if 0)
	}{
		// Mass
		{"g→g identity", 100, "g", "g", 100, true, 0},
		{"kg→g", 1.5, "kg", "g", 1500, true, 0},
		{"g→kg", 1500, "g", "kg", 1.5, true, 0},
		{"lb→g", 1, "lb", "g", 453.59237, true, 1e-9},
		{"g→lb", 453.59237, "g", "lb", 1, true, 1e-9},
		{"oz→g", 1, "oz", "g", 28.349523125, true, 1e-9},
		{"oz→lb", 16, "oz", "lb", 1, true, 1e-12},
		{"lb→oz", 1, "lb", "oz", 16, true, 1e-12},
		{"normalized lb. → grams", 2, "Lb.", "GRAMS", 907.18474, true, 1e-9},

		// Volume
		{"mL→mL identity", 250, "mL", "mL", 250, true, 0},
		{"L→mL", 1, "L", "mL", 1000, true, 0},
		{"gal→L", 1, "gal", "L", 3.785411784, true, 1e-9},
		{"fl oz→mL", 1, "fl oz", "mL", 29.5735295625, true, 1e-9},
		{"tsp→mL", 1, "tsp", "mL", 4.92892159375, true, 1e-9},
		{"cup→mL", 1, "cup", "mL", 236.5882365, true, 1e-9},

		// Cross-family refusal
		{"g→mL refuse", 100, "g", "mL", 0, false, 0},
		{"lb→cup refuse", 1, "lb", "cup", 0, false, 0},
		{"stick→g refuse", 1, "stick", "g", 0, false, 0},

		// Counts: same-string identity passes (fast path); different-name same-family refuses
		{"stick→stick identity", 2, "stick", "stick", 2, true, 0},
		{"stick→pack refuse", 1, "stick", "pack", 0, false, 0},

		// Unknown units refuse
		{"unknown from", 1, "narwhal", "g", 0, false, 0},
		{"unknown to", 1, "g", "narwhal", 0, false, 0},
		{"both unknown but equal", 1, "narwhal", "narwhal", 1, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := calc.Convert(c.amount, c.from, c.to)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (got value %v)", ok, c.wantOK, got)
			}
			if !c.wantOK {
				return
			}
			tol := c.tolerance
			if tol == 0 {
				tol = 1e-6
			}
			if math.Abs(got-c.want) > tol {
				t.Fatalf("Convert(%v %s → %s) = %v, want %v (±%v)", c.amount, c.from, c.to, got, c.want, tol)
			}
		})
	}
}
