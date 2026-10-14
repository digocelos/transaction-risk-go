package evaluation

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/digocelos/transaction-risk-go/trainingdata"
)

func scored(pairs ...any) []Prediction {
	var predictions []Prediction
	for i := 0; i < len(pairs); i += 2 {
		predictions = append(predictions, Prediction{
			Score:       pairs[i].(float64),
			Label:       pairs[i+1].(trainingdata.Label),
			AmountCents: 1000,
		})
	}
	return predictions
}

// Labels for scored.
const (
	F = trainingdata.Fraud
	L = trainingdata.Legitimate
)

func TestSelectThresholdMaximizesRecallWithinTheFalsePositiveRate(t *testing.T) {
	// Five legitimate and four fraud predictions.
	predictions := scored(
		0.95, F,
		0.90, L,
		0.80, F,
		0.70, F,
		0.60, L,
		0.50, L,
		0.40, F,
		0.30, L,
		0.10, L,
	)

	tests := []struct {
		maxFPR float64
		want   ThresholdSelection
	}{
		// At 0.70: 3 of 4 frauds, 1 of 5 legitimate.
		{0.2, ThresholdSelection{Threshold: 0.70, Recall: 0.75, FalsePositiveRate: 0.2, Candidates: 9}},
		// At 0.40: every fraud, 3 of 5 legitimate.
		{0.6, ThresholdSelection{Threshold: 0.40, Recall: 1, FalsePositiveRate: 0.6, Candidates: 9}},
		// Every candidate is allowed; 0.40 already reaches full recall, and
		// lower thresholds only add false positives.
		{1, ThresholdSelection{Threshold: 0.40, Recall: 1, FalsePositiveRate: 0.6, Candidates: 9}},
		// Only 0.95 has no false positive.
		{0, ThresholdSelection{Threshold: 0.95, Recall: 0.25, FalsePositiveRate: 0, Candidates: 9}},
	}

	for _, test := range tests {
		got, err := SelectThreshold(predictions, test.maxFPR)
		if err != nil {
			t.Errorf("max FPR %v: error = %v", test.maxFPR, err)
			continue
		}
		if got != test.want {
			t.Errorf("max FPR %v: selection = %+v, want %+v", test.maxFPR, got, test.want)
		}
	}
}

// Among candidates with the same recall, the highest threshold wins,
// because it raises the fewest alerts.
func TestSelectThresholdBreaksTiesTowardTheHighestThreshold(t *testing.T) {
	predictions := scored(
		0.9, F,
		0.8, L,
		0.7, L,
		0.2, F,
	)

	got, err := SelectThreshold(predictions, 0.5)
	if err != nil {
		t.Fatal(err)
	}

	// 0.9 has recall 0.5 and FPR 0; 0.8 has recall 0.5 and FPR 0.5.
	want := ThresholdSelection{Threshold: 0.9, Recall: 0.5, FalsePositiveRate: 0, Candidates: 4}
	if got != want {
		t.Errorf("selection = %+v, want %+v", got, want)
	}
}

// Equal scores form one candidate and are never split.
func TestSelectThresholdKeepsEqualScoresTogether(t *testing.T) {
	predictions := scored(
		0.8, F,
		0.8, L,
		0.3, F,
		0.1, L,
	)

	// At 0.8 the legitimate prediction with the same score is flagged too.
	_, err := SelectThreshold(predictions, 0)

	var infeasible *InfeasibleThresholdError
	if !errors.As(err, &infeasible) {
		t.Fatalf("error = %v, want an InfeasibleThresholdError", err)
	}

	if infeasible.MaxFalsePositiveRate != 0 || infeasible.LowestAchievableFalsePositiveRate != 0.5 {
		t.Errorf("infeasible = %+v", infeasible)
	}

	got, err := SelectThreshold(predictions, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	want := ThresholdSelection{Threshold: 0.3, Recall: 1, FalsePositiveRate: 0.5, Candidates: 3}
	if got != want {
		t.Errorf("selection = %+v, want %+v", got, want)
	}
}

func TestSelectThresholdDoesNotDependOnOrder(t *testing.T) {
	predictions := scored(
		0.95, F, 0.90, L, 0.80, F, 0.70, F, 0.60, L,
		0.50, L, 0.40, F, 0.30, L, 0.10, L,
	)

	want, err := SelectThreshold(predictions, 0.2)
	if err != nil {
		t.Fatal(err)
	}

	reversed := slices.Clone(predictions)
	slices.Reverse(reversed)

	if got, err := SelectThreshold(reversed, 0.2); err != nil || got != want {
		t.Errorf("reversed: selection = %+v, %v, want %+v", got, err, want)
	}
}

func TestSelectThresholdRequiresBothLabelsAndAValidLimit(t *testing.T) {
	tests := map[string]struct {
		predictions []Prediction
		maxFPR      float64
	}{
		"no prediction":   {nil, 0.1},
		"fraud only":      {scored(0.9, F, 0.1, F), 0.1},
		"legitimate only": {scored(0.9, L, 0.1, L), 0.1},
		"negative limit":  {scored(0.9, F, 0.1, L), -0.1},
		"limit above 1":   {scored(0.9, F, 0.1, L), 1.5},
		"NaN limit":       {scored(0.9, F, 0.1, L), math.NaN()},
		"invalid score":   {scored(1.5, F, 0.1, L), 0.1},
	}

	for name, test := range tests {
		_, err := SelectThreshold(test.predictions, test.maxFPR)

		var infeasible *InfeasibleThresholdError
		if err == nil || errors.As(err, &infeasible) {
			t.Errorf("%s: error = %v, want a validation error", name, err)
		}
	}
}
