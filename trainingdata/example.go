// Package trainingdata reads training datasets that follow the
// pix-training-example-v2 contract: it validates and classifies every
// record, one line at a time, and fingerprints the exact dataset bytes. The
// feature schema being trained decides which version a dataset must use.
package trainingdata

import (
	"errors"
	"time"

	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/pixrisk"
)

// TrainingExampleV2 is the training example version this package supports.
const TrainingExampleV2 = "pix-training-example-v2"

const (
	// MaxRecordSize is the largest record line, in bytes, excluding its LF.
	MaxRecordSize = 131072

	// LabelMaturity is how long after decision time a label must be known
	// before the example is used.
	LabelMaturity = 90 * 24 * time.Hour
)

// Codes specific to training datasets. Records also use the codes of
// pixinput, with the same meaning.
const (
	CodeRecordTooLarge                    = "record_too_large"
	CodeCarriageReturnNotAllowed          = "carriage_return_not_allowed"
	CodeBlankLine                         = "blank_line"
	CodeUnsupportedTrainingExampleVersion = "unsupported_training_example_version"
	CodeLabelBeforeDecisionTime           = "label_before_decision_time"
	CodeLossAmountMismatch                = "loss_amount_mismatch"
	CodeLossExceedsAmount                 = "loss_exceeds_amount"
)

// Label is the outcome of a past Pix payment.
type Label string

const (
	Fraud      Label = "fraud"
	Legitimate Label = "legitimate"
)

// Category tells whether a record can be used to train and evaluate.
type Category string

const (
	Usable       Category = "usable"
	Invalid      Category = "invalid"
	Insufficient Category = "insufficient"
	Immature     Category = "immature"
)

// Outcome is the training-only information about a past Pix payment.
// A nil LossAmountCents is an unknown loss.
type Outcome struct {
	Label           Label
	LabelAsOf       time.Time
	LossAmountCents *int64
}

// Example is a valid training example. A nil GroupKey means the dataset
// owner supplied no key.
type Example struct {
	ScoringInput pixinput.ScoringInput
	Outcome      Outcome
	GroupKey     *string
}

// Diagnostic is one reason why a record is invalid. Path is a JSON Pointer
// into the record; Message is for humans and may change.
type Diagnostic struct {
	Line    int
	Path    string
	Code    string
	Message string
}

// Blocker is one reason why a feature schema cannot convert a record's
// scoring input. Path is a JSON Pointer into the record.
type Blocker struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Record is one classified line of a dataset. Example is set unless the
// record is invalid; Diagnostics are set only for invalid records and
// Blockers only for insufficient ones.
type Record struct {
	Line        int
	Category    Category
	Example     Example
	Diagnostics []Diagnostic
	Blockers    []Blocker
}

// FeatureSchema decides whether scoring inputs can produce the features of
// the model being trained. A dataset for it uses TrainingExampleVersion,
// whose records embed ContractVersion scoring inputs. Blockers returns nil
// when the input converts, and paths relative to the scoring input
// otherwise.
type FeatureSchema struct {
	Name                   string
	TrainingExampleVersion string
	ContractVersion        string
	Blockers               func(pixinput.ScoringInput) ([]Blocker, error)
}

// PixRiskV1 classifies pix-training-example-v2 records with pix-risk-v1: a
// record is insufficient when a required fact is missing or stale.
var PixRiskV1 = FeatureSchema{
	Name:                   pixrisk.Schema,
	TrainingExampleVersion: TrainingExampleV2,
	ContractVersion:        pixinput.ContractV2,
	Blockers: func(input pixinput.ScoringInput) ([]Blocker, error) {
		_, err := pixrisk.Prepare(input)

		var insufficient *pixrisk.InsufficientInputError
		switch {
		case errors.As(err, &insufficient):
			blockers := make([]Blocker, len(insufficient.Blockers))
			for i, blocker := range insufficient.Blockers {
				blockers[i] = Blocker{Path: blocker.Path, Reason: blocker.Reason}
			}
			return blockers, nil
		case err != nil:
			return nil, err
		}

		return nil, nil
	},
}
