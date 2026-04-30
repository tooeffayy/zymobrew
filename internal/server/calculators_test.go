package server_test

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
)

// Calculators are pure functions of request body — no DB needed, so tests
// build the server with a nil pool (same shape as TestOpenAPICoversAllRoutes).

func newCalcServer() *server.Server {
	return server.New(nil, config.Config{InstanceMode: config.ModeOpen}, nil, nil)
}

func postJSON(t *testing.T, srv *server.Server, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	out := rec.Body.Bytes()
	return resp, out
}

func TestCalcABV_Default(t *testing.T) {
	srv := newCalcServer()
	resp, body := postJSON(t, srv, "/api/calculators/abv", map[string]any{
		"og": 1.100, "fg": 1.020,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		ABV     float64 `json:"abv_percent"`
		Formula string  `json:"formula"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Formula != "alternative" {
		t.Errorf("default formula = %q, want alternative", got.Formula)
	}
	if math.Abs(got.ABV-11.583) > 0.01 {
		t.Errorf("abv %.4f, want ~11.583", got.ABV)
	}
}

func TestCalcABV_Simple(t *testing.T) {
	srv := newCalcServer()
	resp, body := postJSON(t, srv, "/api/calculators/abv", map[string]any{
		"og": 1.100, "fg": 1.020, "formula": "simple",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		ABV     float64 `json:"abv_percent"`
		Formula string  `json:"formula"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Formula != "simple" || math.Abs(got.ABV-10.5) > 0.001 {
		t.Errorf("got %+v, want abv 10.5 / formula=simple", got)
	}
}

func TestCalcABV_BadInputs(t *testing.T) {
	srv := newCalcServer()
	cases := []struct {
		name string
		body any
	}{
		{"fg above og", map[string]any{"og": 1.020, "fg": 1.030}},
		{"og out of range", map[string]any{"og": 1.500, "fg": 1.020}},
		{"unknown formula", map[string]any{"og": 1.080, "fg": 1.010, "formula": "junk"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, body := postJSON(t, srv, "/api/calculators/abv", c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status %d, body %s; want 400", resp.StatusCode, body)
			}
		})
	}
}

func TestCalcPredictedFG(t *testing.T) {
	srv := newCalcServer()
	resp, body := postJSON(t, srv, "/api/calculators/predicted-fg", map[string]any{
		"og": 1.100, "attenuation_percent": 75,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		FG  float64 `json:"fg"`
		ABV float64 `json:"abv_percent"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.FG-1.025) > 0.0001 {
		t.Errorf("fg %.5f, want 1.025", got.FG)
	}
	if math.Abs(got.ABV-9.84375) > 0.001 {
		t.Errorf("abv %.5f, want 9.84375", got.ABV)
	}
}

func TestCalcPredictedFG_BadAttenuation(t *testing.T) {
	srv := newCalcServer()
	resp, _ := postJSON(t, srv, "/api/calculators/predicted-fg", map[string]any{
		"og": 1.080, "attenuation_percent": 150,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
}

func TestCalcHoneyWeight(t *testing.T) {
	srv := newCalcServer()
	resp, body := postJSON(t, srv, "/api/calculators/honey-weight", map[string]any{
		"target_og": 1.100, "batch_volume_l": 19,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		HoneyKG  float64 `json:"honey_kg"`
		HoneyLB  float64 `json:"honey_lb"`
		HoneyPPG float64 `json:"honey_ppg"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.HoneyPPG != 35 {
		t.Errorf("default ppg = %.2f, want 35", got.HoneyPPG)
	}
	if math.Abs(got.HoneyLB-14.34) > 0.05 {
		t.Errorf("lb %.4f, want ~14.34", got.HoneyLB)
	}
	if math.Abs(got.HoneyKG-6.50) > 0.05 {
		t.Errorf("kg %.4f, want ~6.50", got.HoneyKG)
	}
}

func TestCalcSugarWeight(t *testing.T) {
	srv := newCalcServer()
	resp, body := postJSON(t, srv, "/api/calculators/sugar-weight", map[string]any{
		"target_og": 1.080, "batch_volume_l": 19,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		SugarKG  float64 `json:"sugar_kg"`
		SugarLB  float64 `json:"sugar_lb"`
		SugarPPG float64 `json:"sugar_ppg"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.SugarPPG != 46 {
		t.Errorf("default ppg = %.2f, want 46", got.SugarPPG)
	}
	if math.Abs(got.SugarLB-8.729) > 0.01 {
		t.Errorf("lb %.4f, want ~8.729", got.SugarLB)
	}
	if math.Abs(got.SugarKG-3.959) > 0.01 {
		t.Errorf("kg %.4f, want ~3.959", got.SugarKG)
	}
}

func TestCalcSugarWeight_BadInput(t *testing.T) {
	srv := newCalcServer()
	resp, _ := postJSON(t, srv, "/api/calculators/sugar-weight", map[string]any{
		"target_og": 1.0, "batch_volume_l": 19,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
}

func TestCalcPitchRate(t *testing.T) {
	srv := newCalcServer()
	resp, body := postJSON(t, srv, "/api/calculators/pitch-rate", map[string]any{
		"og": 1.100, "batch_volume_l": 19,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		Cells       float64 `json:"cells_billion"`
		DryGrams    float64 `json:"dry_yeast_grams"`
		LiquidPacks float64 `json:"liquid_packs"`
		Factor      float64 `json:"pitch_factor"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Factor != 0.75 {
		t.Errorf("default factor = %.3f, want 0.75", got.Factor)
	}
	if math.Abs(got.Cells-339.0) > 1.0 {
		t.Errorf("cells_billion %.2f, want ~339", got.Cells)
	}
	if math.Abs(got.DryGrams-33.9) > 0.5 {
		t.Errorf("dry_yeast_grams %.2f, want ~33.9", got.DryGrams)
	}
	if math.Abs(got.LiquidPacks-3.39) > 0.05 {
		t.Errorf("liquid_packs %.3f, want ~3.39", got.LiquidPacks)
	}
}

func TestCalcPitchRate_BadVolume(t *testing.T) {
	srv := newCalcServer()
	resp, _ := postJSON(t, srv, "/api/calculators/pitch-rate", map[string]any{
		"og": 1.080, "batch_volume_l": 0,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
}

func TestCalc_MalformedJSON(t *testing.T) {
	srv := newCalcServer()
	req := httptest.NewRequest(http.MethodPost, "/api/calculators/abv", bytes.NewBufferString(`{not valid`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Result().StatusCode)
	}
}

func TestCalc_NoAuthRequired(t *testing.T) {
	// Calculators are public — no Authorization header, no cookie should still 200.
	srv := newCalcServer()
	resp, _ := postJSON(t, srv, "/api/calculators/abv", map[string]any{
		"og": 1.080, "fg": 1.010,
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("unauthenticated calculator call: status %d, want 200", resp.StatusCode)
	}
}
