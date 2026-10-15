package training

import (
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// DefaultMaxTrainingWork is the default limit on training work, from about
// 30 minutes to 2 hours of training on the reference machine of
// docs/operations/operating-limits.md.
const DefaultMaxTrainingWork int64 = 1_000_000_000_000

// Limits bound the resources of a run. They depend on the machine, not the
// model, so they are not part of the training configuration.
//
// MaxRecords bounds the records read from a dataset, and with them the
// memory a run keeps: every usable record stays in memory. MaxTrainingWork
// bounds the training work, the number of parameters times the samples
// passed through the network: train records times max_epochs, plus
// validation records times the validation evaluations of a best_validation
// checkpoint.
type Limits struct {
	MaxRecords      int
	MaxTrainingWork int64
}

// DefaultLimits are the limits of the supported envelope.
var DefaultLimits = Limits{MaxRecords: trainingdata.DefaultMaxRecords, MaxTrainingWork: DefaultMaxTrainingWork}

// Validate checks that both limits are positive.
func (l Limits) Validate() error {
	switch {
	case l.MaxRecords < 1:
		return fmt.Errorf("the record limit must be at least 1, got %d", l.MaxRecords)
	case l.MaxTrainingWork < 1:
		return fmt.Errorf("the training work limit must be at least 1, got %d", l.MaxTrainingWork)
	}
	return nil
}

// WorkLimitError reports a run whose training work exceeds the limit. Work
// is math.MaxUint64 when it does not fit in 64 bits.
type WorkLimitError struct {
	Work  uint64
	Limit int64
}

func (e *WorkLimitError) Error() string {
	return fmt.Sprintf("the run needs %d units of training work, above the configured limit of %d; "+
		"reduce max_epochs, the hidden layers, or the train records", e.Work, e.Limit)
}

// checkWork refuses a run whose training work exceeds the limit.
func checkWork(prepared PreparedDataset, config trainingconfig.Config, limit int64) error {
	sizes := config.Sizes()
	parameters := uint64(0)
	for i := 1; i < len(sizes); i++ {
		parameters += uint64(sizes[i-1]*sizes[i] + sizes[i])
	}

	evaluations := 0
	if config.Checkpoint.Policy == trainingconfig.CheckpointBestValidation {
		interval := config.Checkpoint.EvaluationInterval
		evaluations = (config.MaxEpochs + interval - 1) / interval
	}

	work := trainingWork(parameters, uint64(len(prepared.Train)), uint64(len(prepared.Validation)),
		uint64(config.MaxEpochs), uint64(evaluations))
	if work > uint64(limit) {
		return &WorkLimitError{Work: work, Limit: limit}
	}
	return nil
}

// trainingWork returns parameters × (train × epochs + validation ×
// evaluations), or math.MaxUint64 when it does not fit in 64 bits.
func trainingWork(parameters, train, validation, epochs, evaluations uint64) uint64 {
	multiply := func(a, b uint64) (uint64, error) {
		hi, lo := bits.Mul64(a, b)
		if hi != 0 {
			return 0, errors.New("overflow")
		}
		return lo, nil
	}

	trainSamples, err1 := multiply(train, epochs)
	validationSamples, err2 := multiply(validation, evaluations)
	samples, carry := bits.Add64(trainSamples, validationSamples, 0)
	work, err3 := multiply(parameters, samples)

	if err1 != nil || err2 != nil || carry != 0 || err3 != nil {
		return math.MaxUint64
	}
	return work
}
