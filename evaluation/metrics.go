package evaluation

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// maxCents is the largest amount sum reported exactly, 2^53−1.
const maxCents = 1<<53 - 1

// Prediction is one usable example scored by a model. Fraud is the positive
// class. A nil LossAmountCents is an unknown loss.
type Prediction struct {
	Score           float64
	Label           trainingdata.Label
	AmountCents     int64
	LossAmountCents *int64
}

// PRPoint is one step of the precision-recall curve: the precision and
// recall of predicting fraud for every score at or above Threshold. Recall
// is null when there are no fraud examples.
type PRPoint struct {
	Threshold float64  `json:"threshold"`
	Precision float64  `json:"precision"`
	Recall    *float64 `json:"recall"`
}

// Metrics measures predictions at one threshold, as the evaluation
// protocol defines. A ratio whose denominator is zero is nil, encoded as
// JSON null, and never NaN, infinity, or zero. Loss sums include known
// losses only; frauds with an unknown loss are counted separately.
type Metrics struct {
	Threshold      float64 `json:"threshold"`
	TruePositives  int     `json:"true_positives"`
	FalsePositives int     `json:"false_positives"`
	TrueNegatives  int     `json:"true_negatives"`
	FalseNegatives int     `json:"false_negatives"`

	// Precision is TP / (TP + FP), nil when nothing is predicted fraud.
	Precision *float64 `json:"precision"`
	// Recall is TP / (TP + FN), nil when there are no fraud examples.
	Recall *float64 `json:"recall"`
	// F1 is 2·TP / (2·TP + FP + FN), nil when that denominator is zero.
	F1 *float64 `json:"f1"`
	// FalsePositiveRate is FP / (FP + TN), nil when there are no
	// legitimate examples.
	FalsePositiveRate *float64 `json:"false_positive_rate"`
	// FalsePositivesPer1000Legitimate is 1000·FP / (FP + TN), nil when
	// there are no legitimate examples.
	FalsePositivesPer1000Legitimate *float64 `json:"false_positives_per_1000_legitimate"`
	// PRAUC is the average precision over PRCurve, nil when there are no
	// fraud examples.
	PRAUC   *float64  `json:"pr_auc"`
	PRCurve []PRPoint `json:"pr_curve"`

	FraudAmountCentsDetected     int64 `json:"fraud_amount_cents_detected"`
	FraudAmountCentsMissed       int64 `json:"fraud_amount_cents_missed"`
	LegitimateAmountCentsFlagged int64 `json:"legitimate_amount_cents_flagged"`
	KnownLossCentsDetected       int64 `json:"known_loss_cents_detected"`
	KnownLossCentsMissed         int64 `json:"known_loss_cents_missed"`
	UnknownLossFraudsDetected    int   `json:"unknown_loss_frauds_detected"`
	UnknownLossFraudsMissed      int   `json:"unknown_loss_frauds_missed"`
}

// Evaluate measures predictions at threshold: a score at or above the
// threshold predicts fraud. Scores and the threshold must be numbers from 0
// to 1. The result does not depend on the order of predictions. Evaluate
// fails rather than report an amount sum above 2^53−1.
func Evaluate(predictions []Prediction, threshold float64) (Metrics, error) {
	if !isProbability(threshold) {
		return Metrics{}, fmt.Errorf("threshold must be a number from 0 to 1, got %v", threshold)
	}

	m := Metrics{Threshold: threshold, PRCurve: []PRPoint{}}
	sums := &centSums{}

	for i, p := range predictions {
		if err := validate(p); err != nil {
			return Metrics{}, fmt.Errorf("prediction %d: %w", i, err)
		}

		predictedFraud := p.Score >= threshold

		switch {
		case p.Label == trainingdata.Fraud && predictedFraud:
			m.TruePositives++
			sums.add(&m.FraudAmountCentsDetected, p.AmountCents)
			if p.LossAmountCents == nil {
				m.UnknownLossFraudsDetected++
			} else {
				sums.add(&m.KnownLossCentsDetected, *p.LossAmountCents)
			}
		case p.Label == trainingdata.Fraud:
			m.FalseNegatives++
			sums.add(&m.FraudAmountCentsMissed, p.AmountCents)
			if p.LossAmountCents == nil {
				m.UnknownLossFraudsMissed++
			} else {
				sums.add(&m.KnownLossCentsMissed, *p.LossAmountCents)
			}
		case predictedFraud:
			m.FalsePositives++
			sums.add(&m.LegitimateAmountCentsFlagged, p.AmountCents)
		default:
			m.TrueNegatives++
		}
	}

	if sums.overflow {
		return Metrics{}, errors.New("an amount sum exceeds 2^53-1")
	}

	tp, fp, tn, fn := m.TruePositives, m.FalsePositives, m.TrueNegatives, m.FalseNegatives

	m.Precision = ratio(tp, tp+fp)
	m.Recall = ratio(tp, tp+fn)
	m.F1 = ratio(2*tp, 2*tp+fp+fn)
	m.FalsePositiveRate = ratio(fp, fp+tn)
	m.FalsePositivesPer1000Legitimate = ratio(1000*fp, fp+tn)
	m.PRCurve, m.PRAUC = precisionRecall(predictions, tp+fn)

	return m, nil
}

// precisionRecall builds the curve from the highest score to the lowest.
// Equal scores form a single step, so the curve and the average precision
// do not depend on how ties are ordered. The average precision is
// Σ (R_k − R_(k−1)) · P_k over the steps, with R_0 = 0.
func precisionRecall(predictions []Prediction, positives int) ([]PRPoint, *float64) {
	sorted := slices.Clone(predictions)
	slices.SortFunc(sorted, func(a, b Prediction) int {
		return cmp.Compare(b.Score, a.Score)
	})

	curve := []PRPoint{}
	tp, fp := 0, 0
	averagePrecision, previousRecall := 0.0, 0.0

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

		precision := float64(tp) / float64(tp+fp)
		recall := ratio(tp, positives)
		curve = append(curve, PRPoint{Threshold: score, Precision: precision, Recall: recall})

		if recall != nil {
			averagePrecision += (*recall - previousRecall) * precision
			previousRecall = *recall
		}

		start = end
	}

	if positives == 0 {
		return curve, nil
	}

	return curve, &averagePrecision
}

func validate(p Prediction) error {
	switch {
	case !isProbability(p.Score):
		return fmt.Errorf("score must be a number from 0 to 1, got %v", p.Score)
	case p.Label != trainingdata.Fraud && p.Label != trainingdata.Legitimate:
		return fmt.Errorf("label must be fraud or legitimate, got %q", p.Label)
	case p.AmountCents < 0:
		return fmt.Errorf("amount_cents must not be negative, got %d", p.AmountCents)
	case p.LossAmountCents != nil && *p.LossAmountCents < 0:
		return fmt.Errorf("loss_amount_cents must not be negative, got %d", *p.LossAmountCents)
	}

	return nil
}

func isProbability(value float64) bool {
	return !math.IsNaN(value) && value >= 0 && value <= 1
}

// ratio returns numerator / denominator, or nil when the denominator is 0.
func ratio(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}

	value := float64(numerator) / float64(denominator)
	return &value
}

// centSums adds amounts and remembers whether any sum left the exact range.
type centSums struct {
	overflow bool
}

func (s *centSums) add(total *int64, cents int64) {
	if *total > maxCents-cents {
		s.overflow = true
		return
	}
	*total += cents
}
