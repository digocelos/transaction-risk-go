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

// State is the preparation state of one model: the standardization fitted
// on the train partition. Features holds one entry per feature, in
// descriptor order; features that are not standardized have mean 0 and
// scale 1.
type State struct {
	Schema   string         `json:"feature_schema"`
	Features []FeatureState `json:"features"`
}

// FeatureState is the standardization of one feature.
type FeatureState struct {
	Name            string  `json:"name"`
	Mean            float64 `json:"mean"`
	Scale           float64 `json:"scale"`
	ConstantInTrain bool    `json:"constant_in_train"`
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

// Fit learns the standardization from the prepared usable records of the
// train partition, in dataset line order. For each standardized feature it
// uses only the records where the feature is available: the mean, then the
// population variance in a second pass. A standard deviation of exactly 0
// gives a scale of 1 and is marked ConstantInTrain. A feature that is never
// available yields a *FeatureNeverKnownError. Records must come from
// Prepare.
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
		for _, record := range train {
			if available(record, i) {
				sum += record.Values[i]
				n++
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

	return state, nil
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
		case feature.Kind != Standardized && (f.Mean != 0 || f.Scale != 1 || f.ConstantInTrain):
			return fmt.Errorf("%s is not standardized, so its mean must be 0 and its scale 1", f.Name)
		case math.IsNaN(f.Mean) || math.IsInf(f.Mean, 0):
			return fmt.Errorf("%s has a non-finite mean", f.Name)
		case !(f.Scale > 0) || math.IsInf(f.Scale, 0):
			return fmt.Errorf("%s must have a finite, positive scale", f.Name)
		case f.ConstantInTrain && f.Scale != 1:
			return fmt.Errorf("%s is constant in train, so its scale must be 1", f.Name)
		}
	}

	return nil
}

// Apply returns the values a network receives for prepared values: each
// available standardized feature becomes (value − mean) / scale, an
// unavailable one stays exactly 0, and indicators and cyclic features keep
// their values. It fails rather than return a value that must not reach a
// network: a non-finite value, an indicator other than 0 or 1, a cyclic
// value outside [−1, 1], or a non-zero value for an unavailable feature.
func (s State) Apply(p Prepared) ([]float64, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	values := make([]float64, FeatureCount)

	for i, feature := range descriptor {
		value := p.Values[i]

		switch {
		case feature.Kind == Indicator && value != 0 && value != 1:
			return nil, fmt.Errorf("%s must be 0 or 1, got %v", feature.Name, value)
		case feature.Kind == Cyclic && (value < -1 || value > 1):
			return nil, fmt.Errorf("%s must be from -1 to 1, got %v", feature.Name, value)
		case !available(p, i) && value != 0:
			return nil, fmt.Errorf("%s is unavailable but holds %v", feature.Name, value)
		}

		if feature.Kind == Standardized && available(p, i) {
			value = (value - s.Features[i].Mean) / s.Features[i].Scale
		}

		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, errors.New(feature.Name + " is not finite")
		}

		values[i] = value
	}

	return values, nil
}
