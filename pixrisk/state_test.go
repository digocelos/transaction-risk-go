package pixrisk

import (
	"errors"
	"math"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The descriptor matches the feature tables of the specification: names,
// order, kinds, and availability indicators.
func TestDescriptorMatchesTheSpecification(t *testing.T) {
	data, err := os.ReadFile("../docs/features/pix-risk-v1.md")
	if err != nil {
		t.Fatal(err)
	}

	text := string(data)
	section := text[strings.Index(text, "## 9. Features"):strings.Index(text, "### 9.10")]
	rows := regexp.MustCompile("(?m)^\\| (\\d+) \\| `([a-z0-9_]+)` \\| (\\w+) \\|[^|]*\\| (always|\\d+) \\|$").
		FindAllStringSubmatch(section, -1)

	descriptor := Descriptor()
	if len(rows) != FeatureCount || len(descriptor) != FeatureCount {
		t.Fatalf("specification has %d features, descriptor %d, want %d", len(rows), len(descriptor), FeatureCount)
	}

	for i, row := range rows {
		indicator := Always
		if row[4] != "always" {
			indicator, _ = strconv.Atoi(row[4])
		}

		want := Feature{Name: row[2], Kind: Kind(row[3]), Indicator: indicator}
		if row[1] != strconv.Itoa(i) || descriptor[i] != want {
			t.Errorf("feature %d = %+v, specification row %s says %+v", i, descriptor[i], row[1], want)
		}
	}

	// Callers cannot change the descriptor.
	descriptor[0].Name = "changed"
	if Descriptor()[0].Name != "amount" {
		t.Error("Descriptor() exposes shared state")
	}
}

// vector returns prepared values where the named standardized features take
// the given values, available unless the value is nil.
func vector(t *testing.T, values map[string]*float64) Prepared {
	t.Helper()

	var prepared Prepared
	descriptor := Descriptor()

	for name, value := range values {
		i := index(t, name)
		if value == nil {
			continue
		}

		prepared.Values[i] = *value
		if indicator := descriptor[i].Indicator; indicator != Always {
			prepared.Values[indicator] = 1
		}
	}

	return prepared
}

func f(value float64) *float64 { return &value }

// everyFeature makes every standardized feature available with value.
func everyFeature(value float64) map[string]*float64 {
	values := map[string]*float64{}
	for _, feature := range Descriptor() {
		if feature.Kind == Standardized {
			values[feature.Name] = f(value)
		}
	}
	return values
}

func TestFitUsesOnlyAvailableValues(t *testing.T) {
	train := make([]Prepared, 4)
	for i, value := range []*float64{f(1), f(2), f(3), nil} {
		values := everyFeature(float64(i))
		values["person_settlements_d90"] = value
		train[i] = vector(t, values)
	}

	state, err := Fit(train)
	if err != nil {
		t.Fatalf("Fit() error = %v", err)
	}

	got := state.Features[index(t, "person_settlements_d90")]
	// Population variance of 1, 2, 3: ((−1)² + 0² + 1²) / 3.
	want := FeatureState{Name: "person_settlements_d90", Mean: 2, Scale: math.Sqrt(2.0 / 3)}
	if got != want {
		t.Errorf("person_settlements_d90 state = %+v, want %+v", got, want)
	}

	// Always-available features use every record: 0, 1, 2, 3.
	if got := state.Features[index(t, "amount")]; got.Mean != 1.5 || got.Scale != math.Sqrt(1.25) {
		t.Errorf("amount state = %+v", got)
	}

	if got := state.Features[index(t, "no_pix_key")]; got != (FeatureState{Name: "no_pix_key", Mean: 0, Scale: 1}) {
		t.Errorf("indicator state = %+v, want identity", got)
	}

	if state.Schema != Schema || len(state.Features) != FeatureCount {
		t.Errorf("state = %s with %d features", state.Schema, len(state.Features))
	}
}

func TestFitMarksConstantFeatures(t *testing.T) {
	train := []Prepared{vector(t, everyFeature(1)), vector(t, everyFeature(2))}
	for i := range train {
		train[i].Values[index(t, "settled_count_90d")] = 3
	}

	state, err := Fit(train)
	if err != nil {
		t.Fatal(err)
	}

	got := state.Features[index(t, "settled_count_90d")]
	want := FeatureState{Name: "settled_count_90d", Mean: 3, Scale: 1, ConstantInTrain: true}
	if got != want {
		t.Errorf("state = %+v, want %+v", got, want)
	}
}

func TestFitStopsWhenAFeatureIsNeverAvailable(t *testing.T) {
	values := everyFeature(1)
	values["limit_utilization"] = nil
	values["key_settlements_m12"] = nil

	_, err := Fit([]Prepared{vector(t, values)})

	var never *FeatureNeverKnownError
	if !errors.As(err, &never) {
		t.Fatalf("Fit() error = %v, want *FeatureNeverKnownError", err)
	}

	if want := []string{"limit_utilization", "key_settlements_m12"}; !reflect.DeepEqual(never.Features, want) {
		t.Errorf("features = %v, want %v", never.Features, want)
	}

	if _, err := Fit(nil); !errors.As(err, &never) {
		t.Errorf("Fit(nil) error = %v, want *FeatureNeverKnownError", err)
	}
}

func TestApplyStandardizesOnlyAvailableValues(t *testing.T) {
	state, err := Fit([]Prepared{vector(t, everyFeature(1)), vector(t, everyFeature(3))})
	if err != nil {
		t.Fatal(err)
	}

	values := everyFeature(5)
	values["limit_utilization"] = nil
	prepared := vector(t, values)
	prepared.Values[index(t, "local_time_sin")] = -0.5
	prepared.Values[index(t, "no_pix_key")] = 1

	final, err := state.Apply(prepared)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(final) != FeatureCount {
		t.Fatalf("Apply() returned %d values", len(final))
	}

	// Mean 2 and scale 1 for every standardized feature.
	tests := map[string]float64{
		"amount":                  3,
		"key_link_age":            3,
		"limit_utilization":       0,
		"limit_utilization_known": 0,
		"key_entry_known":         1,
		"local_time_sin":          -0.5,
		"no_pix_key":              1,
	}
	for name, want := range tests {
		if got := final[index(t, name)]; got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}

func TestApplyRejectsValuesThatMustNotReachTheNetwork(t *testing.T) {
	state, err := Fit([]Prepared{vector(t, everyFeature(1)), vector(t, everyFeature(3))})
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]func(*Prepared){
		"NaN feature":                 func(p *Prepared) { p.Values[index(t, "amount")] = math.NaN() },
		"infinite feature":            func(p *Prepared) { p.Values[index(t, "local_time_cos")] = math.Inf(1) },
		"indicator other than 0 or 1": func(p *Prepared) { p.Values[index(t, "no_pix_key")] = 0.5 },
		"value without its indicator": func(p *Prepared) {
			p.Values[index(t, "key_entry_known")] = 0
		},
		"cyclic value out of range": func(p *Prepared) { p.Values[index(t, "local_time_sin")] = 1.5 },
	}

	for name, change := range tests {
		prepared := vector(t, everyFeature(2))
		change(&prepared)

		if _, err := state.Apply(prepared); err == nil {
			t.Errorf("%s: Apply() error = nil, want an error", name)
		}
	}
}

func TestStateValidation(t *testing.T) {
	valid, err := Fit([]Prepared{vector(t, everyFeature(1)), vector(t, everyFeature(3))})
	if err != nil {
		t.Fatal(err)
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() of a fitted state: %v", err)
	}

	tests := map[string]func(*State){
		"other schema":        func(s *State) { s.Schema = "transaction-features-v1" },
		"missing feature":     func(s *State) { s.Features = s.Features[:FeatureCount-1] },
		"reordered features":  func(s *State) { s.Features[0], s.Features[9] = s.Features[9], s.Features[0] },
		"NaN mean":            func(s *State) { s.Features[0].Mean = math.NaN() },
		"zero scale":          func(s *State) { s.Features[0].Scale = 0 },
		"negative scale":      func(s *State) { s.Features[0].Scale = -1 },
		"infinite scale":      func(s *State) { s.Features[0].Scale = math.Inf(1) },
		"indicator with mean": func(s *State) { s.Features[1].Mean = 0.5 },
		"constant with scale": func(s *State) { s.Features[0].ConstantInTrain = true; s.Features[0].Scale = 2 },
	}

	for name, change := range tests {
		state := State{Schema: valid.Schema, Features: append([]FeatureState(nil), valid.Features...)}
		change(&state)

		if err := state.Validate(); err == nil {
			t.Errorf("%s: Validate() error = nil, want an error", name)
		}

		if _, err := state.Apply(vector(t, everyFeature(2))); err == nil {
			t.Errorf("%s: Apply() error = nil, want an error", name)
		}
	}
}

// A standardized value that overflows binary64 is an error, never an
// infinite input to the network.
func TestApplyRejectsStandardizedValuesThatOverflow(t *testing.T) {
	state, err := Fit([]Prepared{vector(t, everyFeature(1)), vector(t, everyFeature(3))})
	if err != nil {
		t.Fatal(err)
	}
	state.Features[index(t, "amount")].Scale = math.SmallestNonzeroFloat64
	if err := state.Validate(); err != nil {
		t.Fatalf("a tiny positive scale is a valid state: %v", err)
	}

	_, err = state.Apply(vector(t, everyFeature(1e10)))
	if err == nil || !strings.Contains(err.Error(), "amount is not finite") {
		t.Errorf("Apply() error = %v, want amount is not finite", err)
	}
}
