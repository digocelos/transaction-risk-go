package pixrisk

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Codes of the fitted state.
const (
	// CodeFeatureNeverKnown stops training: a standardized feature is
	// unavailable in every usable train record.
	CodeFeatureNeverKnown = "feature_never_known"
	// CodeConstantInTrain marks a standardized feature with a standard
	// deviation of exactly 0 in the train partition; its scale is 1.
	CodeConstantInTrain = "constant_in_train"
)

// Statuses of a model note: what applying the preparation state did to a
// feature of one input.
const (
	// StatusClipped marks a standardized feature whose value was outside
	// the train range and was clipped to it before standardization.
	StatusClipped = "clipped"
	// StatusUnexercised marks a feature that is not 0 in the input but was
	// 0 in every usable train record, so the network never trained on it.
	StatusUnexercised = "unexercised"
)

// State is the preparation state of one model: the standardization and
// the train range fitted on the train partition, and which features the
// train partition exercised. Features holds one entry per feature, in
// descriptor order; features that are not standardized have mean 0, scale
// 1, and no range.
type State struct {
	Schema   string         `json:"feature_schema"`
	Features []FeatureState `json:"features"`
}

// FeatureState is the fitted state of one feature. Min and Max are the
// range of the fixed-transformed values among the usable train records in
// which a standardized feature is available, and nil for other features.
// ExercisedInTrain reports whether some usable train record gave the
// feature a value other than 0 after standardization.
type FeatureState struct {
	Name             string   `json:"name"`
	Mean             float64  `json:"mean"`
	Scale            float64  `json:"scale"`
	ConstantInTrain  bool     `json:"constant_in_train"`
	Min              *float64 `json:"min"`
	Max              *float64 `json:"max"`
	ExercisedInTrain bool     `json:"exercised_in_train"`
}

// ModelNote reports what applying a preparation state did to one feature
// of an input: clipped to the train range, or activated although no train
// record exercised it. Unlike a preparation note, it depends on the model.
type ModelNote struct {
	Feature string `json:"feature"`
	Status  string `json:"status"`
}

// FeatureNeverKnownError lists the standardized features that are
// unavailable in every train record, in descriptor order.
type FeatureNeverKnownError struct {
	Features []string
}

func (e *FeatureNeverKnownError) Error() string {
	return fmt.Sprintf("%s: no usable train record has %s", CodeFeatureNeverKnown, strings.Join(e.Features, ", "))
}

// available reports whether feature i of p holds a fact.
func available(p Prepared, i int) bool {
	indicator := descriptor[i].Indicator
	return indicator == Always || p.Values[indicator] == 1
}

// Fit learns the preparation state from the prepared usable records of the
// train partition, in dataset line order. For each standardized feature it
// uses only the records where the feature is available: the mean, then the
// population variance in a second pass, and the minimum and maximum of the
// values. A standard deviation of exactly 0 gives a scale of 1 and is
// marked ConstantInTrain. A feature that is never available yields a
// *FeatureNeverKnownError. Every feature is then marked as exercised when
// some record gives it a value other than 0 after the state is applied.
// Records must come from Prepare.
func Fit(train []Prepared) (State, error) {
	state := State{Schema: Schema, Features: make([]FeatureState, FeatureCount)}
	var never []string

	for i, feature := range descriptor {
		state.Features[i] = FeatureState{Name: feature.Name, Mean: 0, Scale: 1}
		if feature.Kind != Standardized {
			continue
		}

		var sum float64
		n := 0
		minimum, maximum := math.Inf(1), math.Inf(-1)
		for _, record := range train {
			if available(record, i) {
				sum += record.Values[i]
				n++
				minimum = math.Min(minimum, record.Values[i])
				maximum = math.Max(maximum, record.Values[i])
			}
		}

		if n == 0 {
			never = append(never, feature.Name)
			continue
		}

		mean := sum / float64(n)

		var squares float64
		for _, record := range train {
			if available(record, i) {
				deviation := record.Values[i] - mean
				squares += deviation * deviation
			}
		}

		std := math.Sqrt(squares / float64(n))

		state.Features[i].Mean = mean
		state.Features[i].Min = &minimum
		state.Features[i].Max = &maximum
		if std == 0 {
			state.Features[i].ConstantInTrain = true
		} else {
			state.Features[i].Scale = std
		}
	}

	if len(never) > 0 {
		return State{}, &FeatureNeverKnownError{Features: never}
	}

	if err := state.Validate(); err != nil {
		return State{}, fmt.Errorf("fit %s: %w", Schema, err)
	}

	// A feature is exercised when the network sees it as something other
	// than 0 in at least one train record. The train records are within
	// the train range by construction, so nothing is clipped here.
	for _, record := range train {
		values, err := state.Apply(record)
		if err != nil {
			return State{}, fmt.Errorf("fit %s: %w", Schema, err)
		}
		for i, value := range values {
			if value != 0 {
				state.Features[i].ExercisedInTrain = true
			}
		}
	}

	return state, nil
}

// Unexercised returns the indices of the features that no usable train
// record exercised, in descriptor order.
func (s State) Unexercised() []int {
	var unexercised []int
	for i, feature := range s.Features {
		if !feature.ExercisedInTrain {
			unexercised = append(unexercised, i)
		}
	}
	return unexercised
}

// Validate checks that the state belongs to pix-risk-v1 and can be applied:
// every feature in order, finite means, finite positive scales, and the
// identity for features that are not standardized.
func (s State) Validate() error {
	if s.Schema != Schema {
		return fmt.Errorf("preparation state is for %q, not %s", s.Schema, Schema)
	}

	if len(s.Features) != FeatureCount {
		return fmt.Errorf("preparation state has %d features, want %d", len(s.Features), FeatureCount)
	}

	for i, feature := range descriptor {
		f := s.Features[i]

		switch {
		case f.Name != feature.Name:
			return fmt.Errorf("preparation state feature %d is %q, want %q", i, f.Name, feature.Name)
		case feature.Kind != Standardized && (f.Mean != 0 || f.Scale != 1 || f.ConstantInTrain || f.Min != nil || f.Max != nil):
			return fmt.Errorf("%s is not standardized, so its mean must be 0, its scale 1, and it has no range", f.Name)
		case math.IsNaN(f.Mean) || math.IsInf(f.Mean, 0):
			return fmt.Errorf("%s has a non-finite mean", f.Name)
		case !(f.Scale > 0) || math.IsInf(f.Scale, 0):
			return fmt.Errorf("%s must have a finite, positive scale", f.Name)
		case f.ConstantInTrain && f.Scale != 1:
			return fmt.Errorf("%s is constant in train, so its scale must be 1", f.Name)
		case feature.Kind == Standardized && (f.Min == nil || f.Max == nil):
			return fmt.Errorf("%s is standardized, so it has a train range", f.Name)
		case feature.Kind == Standardized && (math.IsNaN(*f.Min) || math.IsInf(*f.Min, 0) || math.IsNaN(*f.Max) || math.IsInf(*f.Max, 0)):
			return fmt.Errorf("%s has a non-finite train range", f.Name)
		case feature.Kind == Standardized && *f.Min > *f.Max:
			return fmt.Errorf("%s has a train range whose minimum exceeds its maximum", f.Name)
		case feature.Kind == Standardized && f.ConstantInTrain && *f.Min != *f.Max:
			return fmt.Errorf("%s is constant in train, so its range is one value", f.Name)
		}
	}

	return nil
}

// Apply returns the values a network receives for prepared values: each
// available standardized feature is clipped to the train range and becomes
// (value − mean) / scale, an unavailable one stays exactly 0, and
// indicators and cyclic features keep their values. It fails rather than
// return a value that must not reach a network: a non-finite value, an
// indicator other than 0 or 1, a cyclic value outside [−1, 1], or a
// non-zero value for an unavailable feature.
func (s State) Apply(p Prepared) ([]float64, error) {
	values, _, err := s.ApplyWithNotes(p)
	return values, err
}

// ApplyWithNotes is Apply with the model notes of the input: one clipped
// note per standardized feature whose value was outside the train range,
// and one unexercised note per feature that reaches the network as a value
// other than 0 although no train record exercised it. Notes are in
// descriptor order, clipped before unexercised for one feature, and the
// list is never nil.
func (s State) ApplyWithNotes(p Prepared) ([]float64, []ModelNote, error) {
	if err := s.Validate(); err != nil {
		return nil, nil, err
	}

	values := make([]float64, FeatureCount)
	notes := []ModelNote{}

	for i, feature := range descriptor {
		value := p.Values[i]
		state := s.Features[i]

		switch {
		case feature.Kind == Indicator && value != 0 && value != 1:
			return nil, nil, fmt.Errorf("%s must be 0 or 1, got %v", feature.Name, value)
		case feature.Kind == Cyclic && (value < -1 || value > 1):
			return nil, nil, fmt.Errorf("%s must be from -1 to 1, got %v", feature.Name, value)
		case !available(p, i) && value != 0:
			return nil, nil, fmt.Errorf("%s is unavailable but holds %v", feature.Name, value)
		}

		if feature.Kind == Standardized && available(p, i) {
			if math.IsNaN(value) {
				return nil, nil, errors.New(feature.Name + " is not finite")
			}
			if clipped := math.Min(math.Max(value, *state.Min), *state.Max); clipped != value {
				notes = append(notes, ModelNote{Feature: feature.Name, Status: StatusClipped})
				value = clipped
			}
			value = (value - state.Mean) / state.Scale
		}

		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, nil, errors.New(feature.Name + " is not finite")
		}

		if value != 0 && !state.ExercisedInTrain {
			notes = append(notes, ModelNote{Feature: feature.Name, Status: StatusUnexercised})
		}

		values[i] = value
	}

	return values, notes, nil
}
