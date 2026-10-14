package modelevaluation

import (
	"slices"

	"github.com/digocelos/transaction-risk-go/evaluation"
)

// Reasons why an evaluation is incomplete.
const (
	// ReasonNoUsableExamples: no usable record is in scope, so no metric
	// measures anything.
	ReasonNoUsableExamples = "no_usable_examples"

	// ReasonNoFraudExamples: no usable fraud record is in scope, so
	// detection cannot be measured.
	ReasonNoFraudExamples = "no_fraud_examples"

	// ReasonNoLegitimateExamples: no usable legitimate record is in scope,
	// so false alerts cannot be measured.
	ReasonNoLegitimateExamples = "no_legitimate_examples"

	// ReasonUnknownLossFrauds: a fraud in scope has an unknown loss, so the
	// known loss sums are partial.
	ReasonUnknownLossFrauds = "unknown_loss_frauds"
)

// Completeness tells whether every metric measured what it defines. An
// evaluation is complete when Incomplete is empty. Completeness is about
// the data in scope; a complete evaluation is not a quality pass.
type Completeness struct {
	Complete   bool         `json:"complete"`
	Incomplete []Incomplete `json:"incomplete"`
}

// Incomplete is one reason why an evaluation is incomplete and the metrics,
// by their report member names, that the reason leaves undefined, trivial,
// or partial.
type Incomplete struct {
	Reason  string   `json:"reason"`
	Metrics []string `json:"metrics"`
}

var (
	allMetrics = []string{
		"true_positives", "false_positives", "true_negatives", "false_negatives",
		"precision", "recall", "f1", "false_positive_rate", "false_positives_per_1000_legitimate",
		"pr_auc", "pr_curve",
		"fraud_amount_cents_detected", "fraud_amount_cents_missed", "legitimate_amount_cents_flagged",
		"known_loss_cents_detected", "known_loss_cents_missed",
		"unknown_loss_frauds_detected", "unknown_loss_frauds_missed",
	}

	// Without frauds, precision is 0 or undefined and every detection
	// measure is empty.
	fraudMetrics = []string{
		"precision", "recall", "f1", "pr_auc", "pr_curve",
		"fraud_amount_cents_detected", "fraud_amount_cents_missed",
		"known_loss_cents_detected", "known_loss_cents_missed",
		"unknown_loss_frauds_detected", "unknown_loss_frauds_missed",
	}

	// Without legitimate records, precision and PR-AUC are 1 or undefined,
	// F1 follows recall alone, and false alerts are undefined or empty.
	legitimateMetrics = []string{
		"precision", "f1", "false_positive_rate", "false_positives_per_1000_legitimate",
		"pr_auc", "pr_curve", "legitimate_amount_cents_flagged",
	}

	knownLossMetrics = []string{"known_loss_cents_detected", "known_loss_cents_missed"}
)

// newCompleteness finds the reasons, in the order of the constants, why
// metrics over the usable records in scope are incomplete.
func newCompleteness(inScope evaluation.PartitionCounts, metrics evaluation.Metrics) Completeness {
	completeness := Completeness{Incomplete: []Incomplete{}}
	add := func(reason string, metrics []string) {
		completeness.Incomplete = append(completeness.Incomplete,
			Incomplete{Reason: reason, Metrics: slices.Clone(metrics)})
	}

	if inScope.Fraud+inScope.Legitimate == 0 {
		add(ReasonNoUsableExamples, allMetrics)
	} else {
		if inScope.Fraud == 0 {
			add(ReasonNoFraudExamples, fraudMetrics)
		}
		if inScope.Legitimate == 0 {
			add(ReasonNoLegitimateExamples, legitimateMetrics)
		}
		if metrics.UnknownLossFraudsDetected+metrics.UnknownLossFraudsMissed > 0 {
			add(ReasonUnknownLossFrauds, knownLossMetrics)
		}
	}

	completeness.Complete = len(completeness.Incomplete) == 0
	return completeness
}
