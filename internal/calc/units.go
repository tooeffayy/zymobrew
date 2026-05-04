package calc

import "strings"

// UnitFamily groups units that can convert into one another. The strict
// match path uses Family equality as the gate — units in different
// families don't share a canonical scale and won't be combined.
type UnitFamily string

const (
	// FamilyMass / FamilyVolume are the two real conversion families
	// brewing actually needs. Canonical units are grams and milliliters.
	FamilyMass   UnitFamily = "mass"
	FamilyVolume UnitFamily = "volume"

	// FamilyCount captures dimensionless brewing units ("stick" of cinnamon,
	// "pack" of yeast, "each" for whole fruit). They never convert — a
	// recipe asking for 2 sticks can't be satisfied by inventory measured
	// in grams. Match logic treats them strictly (exact unit string).
	FamilyCount UnitFamily = "count"

	// FamilyUnknown is returned for unit strings the table doesn't know
	// about. Treated like FamilyCount in match logic — strict only,
	// never converted, never summed across spellings.
	FamilyUnknown UnitFamily = ""
)

// unitInfo describes one unit: its family and the multiplier to convert
// into the family's canonical unit (g for mass, mL for volume). The
// canonical unit itself has factor 1.0.
type unitInfo struct {
	family UnitFamily
	toBase float64
}

// unitTable is the case- and punctuation-insensitive lookup. Keys are
// normalized via normalizeUnit before lookup. Conversion factors are
// US customary — gallons / fluid ounces / etc. (UK definitions differ
// by a few percent and would surprise a brewer reading "1 gal").
//
// Counts ("stick", "pack", "each") sit in the table so callers get a
// definitive Family answer instead of falling into FamilyUnknown.
var unitTable = map[string]unitInfo{
	// --- Mass (canonical: g) -----------------------------------------
	"g":         {FamilyMass, 1.0},
	"gram":      {FamilyMass, 1.0},
	"grams":     {FamilyMass, 1.0},
	"kg":        {FamilyMass, 1000.0},
	"kilo":      {FamilyMass, 1000.0},
	"kilogram":  {FamilyMass, 1000.0},
	"kilograms": {FamilyMass, 1000.0},
	"mg":        {FamilyMass, 0.001},
	"milligram": {FamilyMass, 0.001},
	"oz":        {FamilyMass, 28.349523125}, // avoirdupois ounce; "fl oz" handled below
	"ounce":     {FamilyMass, 28.349523125},
	"ounces":    {FamilyMass, 28.349523125},
	"lb":        {FamilyMass, 453.59237},
	"lbs":       {FamilyMass, 453.59237},
	"pound":     {FamilyMass, 453.59237},
	"pounds":    {FamilyMass, 453.59237},

	// --- Volume (canonical: mL) --------------------------------------
	// US definitions; "tsp", "tbsp", "cup", "qt", "gal" are all US
	// customary. "fl oz" gets the punctuation stripped during normalize
	// so "fl oz", "fl. oz.", "floz", and "fluidounce" all match.
	"ml":          {FamilyVolume, 1.0},
	"milliliter":  {FamilyVolume, 1.0},
	"milliliters": {FamilyVolume, 1.0},
	"cl":          {FamilyVolume, 10.0},
	"l":           {FamilyVolume, 1000.0},
	"liter":       {FamilyVolume, 1000.0},
	"liters":      {FamilyVolume, 1000.0},
	"litre":       {FamilyVolume, 1000.0},
	"litres":      {FamilyVolume, 1000.0},
	"tsp":         {FamilyVolume, 4.92892159375},
	"teaspoon":    {FamilyVolume, 4.92892159375},
	"teaspoons":   {FamilyVolume, 4.92892159375},
	"tbsp":        {FamilyVolume, 14.78676478125},
	"tablespoon":  {FamilyVolume, 14.78676478125},
	"tablespoons": {FamilyVolume, 14.78676478125},
	"floz":        {FamilyVolume, 29.5735295625},
	"fluidounce":  {FamilyVolume, 29.5735295625},
	"fluidounces": {FamilyVolume, 29.5735295625},
	"cup":         {FamilyVolume, 236.5882365},
	"cups":        {FamilyVolume, 236.5882365},
	"pt":          {FamilyVolume, 473.176473},
	"pint":        {FamilyVolume, 473.176473},
	"pints":       {FamilyVolume, 473.176473},
	"qt":          {FamilyVolume, 946.352946},
	"quart":       {FamilyVolume, 946.352946},
	"quarts":      {FamilyVolume, 946.352946},
	"gal":         {FamilyVolume, 3785.411784},
	"gallon":      {FamilyVolume, 3785.411784},
	"gallons":     {FamilyVolume, 3785.411784},

	// --- Count (no conversion possible) ------------------------------
	// These are listed so Family() returns FamilyCount instead of
	// FamilyUnknown — that distinction is informational; both behave
	// the same way in the matcher (strict-match only, never summed
	// across rows in different units).
	"stick":  {FamilyCount, 0},
	"sticks": {FamilyCount, 0},
	"pack":   {FamilyCount, 0},
	"packs":  {FamilyCount, 0},
	"packet": {FamilyCount, 0},
	"each":   {FamilyCount, 0},
	"piece":  {FamilyCount, 0},
	"pieces": {FamilyCount, 0},
	"whole":  {FamilyCount, 0},
}

// normalizeUnit lowercases the string, trims whitespace, and strips
// dots/spaces so casual user input ("Lb.", "Fl Oz", "fl. oz.") collapses
// to a single canonical key. We don't trim anything more aggressive
// than that — "ml" and "cl" are both real units and a trailing 's' is
// load-bearing for plurals like "cups" vs "cup" (both in the table).
func normalizeUnit(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.ReplaceAll(u, ".", "")
	u = strings.ReplaceAll(u, " ", "")
	return u
}

// Family returns the conversion family of a unit string, or FamilyUnknown
// when the unit isn't in the table. Empty input returns FamilyUnknown
// so callers can treat "no unit" the same as "unrecognized unit"
// (both are non-convertible).
func Family(unit string) UnitFamily {
	if unit == "" {
		return FamilyUnknown
	}
	if info, ok := unitTable[normalizeUnit(unit)]; ok {
		return info.family
	}
	return FamilyUnknown
}

// Convert returns `amount` expressed in `to`, given it was originally in
// `from`. The boolean is false when conversion isn't possible: either
// unit is unknown, the units belong to different families, or the
// family is non-convertible (count units). Same-string inputs always
// succeed with the identity factor — callers don't need a special case.
func Convert(amount float64, from, to string) (float64, bool) {
	if from == to {
		return amount, true
	}
	fInfo, fOK := unitTable[normalizeUnit(from)]
	tInfo, tOK := unitTable[normalizeUnit(to)]
	if !fOK || !tOK {
		return 0, false
	}
	if fInfo.family != tInfo.family {
		return 0, false
	}
	if fInfo.family == FamilyCount {
		// Same-family-count would only succeed via the from==to fast path
		// above. "stick" → "pack" makes no physical sense, so refuse.
		return 0, false
	}
	// Convert: amount × (from→base) ÷ (to→base).
	return amount * fInfo.toBase / tInfo.toBase, true
}
