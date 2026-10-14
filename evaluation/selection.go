package evaluation

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// ThresholdSelection is the threshold chosen from validation predictions:
// the highest recall whose false positive rate stays within the limit.
// Candidates is the number of distinct scores considered.
type ThresholdSelection struct {
	Threshold         float64
	Recall            float64
	FalsePositiveRate float64
	Candidates        int
}

// InfeasibleThresholdError reports that no candidate threshold keeps the
// false positive rate within the limit. LowestAchievableFalsePositiveRate is
// the rate of the highest candidate, the lowest any candidate reaches.
type InfeasibleThresholdError struct {
	MaxFalsePositiveRate              float64
	LowestAchievableFalsePositiveRate float64
}

func (e *InfeasibleThresholdError) Error() string {
	return fmt.Sprintf("no threshold keeps the validation false positive rate at or below %v; "+
		"the lowest achievable rate is %v", e.MaxFalsePositiveRate, e.LowestAchievableFalsePositiveRate)
}

// SelectThreshold chooses a decision threshold from predictions, which
// must include at least one fraud and one legitimate prediction. The
// candidates are the distinct scores; a score at or above a candidate
// predicts fraud, so equal scores are never split. Among the candidates
// whose false positive rate, FP / (FP + TN), is at most
// maxFalsePositiveRate, it chooses the highest recall, and among equal
// recalls the highest threshold, which raises the fewest alerts. When no
// candidate is within the limit, it returns an *InfeasibleThresholdError.
// The result does not depend on the order of predictions.
func SelectThreshold(predictions []Prediction, maxFalsePositiveRate float64) (ThresholdSelection, error) {
	if !isProbability(maxFalsePositiveRate) {
		return ThresholdSelection{}, fmt.Errorf("the maximum false positive rate must be a number from 0 to 1, got %v",
			maxFalsePositiveRate)
	}

	frauds, legitimates := 0, 0
	for i, p := range predictions {
		if err := validate(p); err != nil {
			return ThresholdSelection{}, fmt.Errorf("prediction %d: %w", i, err)
		}
		if p.Label == trainingdata.Fraud {
			frauds++
		} else {
			legitimates++
		}
	}

	if frauds == 0 || legitimates == 0 {
		return ThresholdSelection{}, errors.New("selecting a threshold needs at least one fraud and one legitimate prediction")
	}

	sorted := slices.Clone(predictions)
	slices.SortFunc(sorted, func(a, b Prediction) int {
		return cmp.Compare(b.Score, a.Score)
	})

	var best *ThresholdSelection
	var lowest float64
	tp, fp, candidates := 0, 0, 0

	for start := 0; start < len(sorted); {
		score := sorted[start].Score

		end := start
		for end < len(sorted) && sorted[end].Score == score {
			if sorted[end].Label == trainingdata.Fraud {
				tp++
			} else {
				fp++
			}
			end++
		}

		candidates++
		recall := float64(tp) / float64(frauds)
		rate := float64(fp) / float64(legitimates)

		if candidates == 1 {
			lowest = rate
		}

		if rate <= maxFalsePositiveRate && (best == nil || recall > best.Recall) {
			best = &ThresholdSelection{Threshold: score, Recall: recall, FalsePositiveRate: rate}
		}

		start = end
	}

	if best == nil {
		return ThresholdSelection{}, &InfeasibleThresholdError{
			MaxFalsePositiveRate:              maxFalsePositiveRate,
			LowestAchievableFalsePositiveRate: lowest,
		}
	}

	best.Candidates = candidates
	return *best, nil
}
