// Package training trains a payer-side Pix fraud model from a
// pix-training-example-v2 dataset, as a pix-training-config-v2
// configuration describes, and evaluates it with the temporal evaluation
// protocol: the preparation state and the network are fitted on the train
// partition only, the checkpoint and the threshold are chosen on the
// validation partition only, and the test partition, when configured, is
// scored once after everything is frozen. It never reads the system clock
// or shuffles records.
package training

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// Result is a trained network, the preparation state it expects, and the
// report of its run.
type Result struct {
	Network *nn.Network
	State   pixrisk.State
	Report  trainingreport.Report
}

// InvalidDatasetError reports a dataset with at least one invalid record,
// which must not be trained or evaluated. Report is the dataset report, and
// Diagnostics lists every problem of every invalid record, in line order.
type InvalidDatasetError struct {
	Report      trainingdata.Report
	Diagnostics []trainingdata.Diagnostic
}

func (e *InvalidDatasetError) Error() string {
	return fmt.Sprintf("the dataset has %d invalid records and cannot be trained or evaluated",
		e.Report.Records.Invalid)
}

// UntrainableError reports a train partition without at least one usable
// fraud record and one usable legitimate record.
type UntrainableError struct {
	Train evaluation.PartitionCounts
}

func (e *UntrainableError) Error() string {
	return fmt.Sprintf("the train partition has %d usable fraud and %d usable legitimate records; "+
		"training needs at least one of each", e.Train.Fraud, e.Train.Legitimate)
}

// ValidationRequirementError reports a validation partition that cannot
// support the configured selection: the best_validation checkpoint needs a
// usable validation record, validation_pr_auc a usable validation fraud,
// and a validation-selected threshold a usable fraud and a usable
// legitimate validation record.
type ValidationRequirementError struct {
	Validation  evaluation.PartitionCounts
	Requirement string
}

func (e *ValidationRequirementError) Error() string {
	return fmt.Sprintf("the validation partition has %d usable fraud and %d usable legitimate records; %s",
		e.Validation.Fraud, e.Validation.Legitimate, e.Requirement)
}

// Run validates the configuration, prepares the dataset with pix-risk-v1,
// and checks that the validation partition supports the configured
// selection before training. It trains the configured network on the
// usable train records in line order, keeps the configured checkpoint,
// chooses the threshold, measures the validation partition, and, when
// configured, evaluates the test partition once with everything frozen.
// The test partition never influences any choice. progress, when not nil,
// receives the training loss of each epoch, measured before that epoch's
// update. ctx is checked every 1,024 records while reading and before every
// epoch; a canceled run returns ctx's error and no result. An infeasible
// threshold policy returns an *evaluation.InfeasibleThresholdError.
func Run(ctx context.Context, dataset io.Reader, config trainingconfig.Config, progress func(epoch int, loss float64)) (Result, error) {
	if err := config.Validate(); err != nil {
		return Result{}, fmt.Errorf("invalid training configuration: %w", err)
	}

	prepared, err := PrepareDataset(ctx, dataset, config.Boundaries())
	if err != nil {
		return Result{}, err
	}

	if err := checkValidation(prepared.Partitions.Validation, config); err != nil {
		return Result{}, err
	}

	network, selection, err := train(ctx, prepared, config, progress)
	if err != nil {
		return Result{}, err
	}

	selection.Threshold, err = chooseThreshold(network, prepared.Validation, config.Threshold)
	if err != nil {
		return Result{}, err
	}

	threshold := selection.Threshold.Value

	validation, err := evaluate(network, prepared.Validation, threshold)
	if err != nil {
		return Result{}, fmt.Errorf("evaluate validation partition: %w", err)
	}

	report := trainingreport.Report{
		Configuration: trainingreport.Configuration{Config: config},
		Dataset:       prepared.Dataset,
		Partitions:    prepared.Partitions,
		Coverage: trainingreport.NewCoverage(
			preparedValues(prepared.Train), preparedValues(prepared.Validation), preparedValues(prepared.Test)),
		Selection:  selection,
		Validation: validation,
	}

	if config.TestEvaluation == trainingconfig.TestAfterSelection {
		test, err := evaluate(network, prepared.Test, threshold)
		if err != nil {
			return Result{}, fmt.Errorf("evaluate test partition: %w", err)
		}
		report.TestEvaluated = true
		report.Test = &test
	}

	return Result{Network: network, State: prepared.State, Report: report}, nil
}

func preparedValues(examples []PreparedExample) []pixrisk.Prepared {
	values := make([]pixrisk.Prepared, len(examples))
	for i, example := range examples {
		values[i] = example.Prepared
	}
	return values
}

// checkValidation enforces what the configured selection needs from the
// validation partition, before any training.
func checkValidation(validation evaluation.PartitionCounts, config trainingconfig.Config) error {
	fail := func(requirement string) error {
		return &ValidationRequirementError{Validation: validation, Requirement: requirement}
	}

	if config.Checkpoint.Policy == trainingconfig.CheckpointBestValidation {
		switch {
		case validation.Fraud+validation.Legitimate == 0:
			return fail("the best_validation checkpoint needs at least one usable validation record")
		case config.Checkpoint.Objective == trainingconfig.ObjectiveValidationPRAUC && validation.Fraud == 0:
			return fail("the validation_pr_auc objective needs at least one usable validation fraud record")
		}
	}

	if config.Threshold.Policy == trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate &&
		(validation.Fraud == 0 || validation.Legitimate == 0) {
		return fail("a validation-selected threshold needs at least one usable fraud and one usable legitimate validation record")
	}

	return nil
}

// train fits a new network with full-batch gradient descent, in dataset
// order, and keeps the configured checkpoint. With best_validation, the
// validation objective is measured every EvaluationInterval epochs and
// after the last epoch; an evaluation improves on the best one only by more
// than MinImprovement, so ties keep the earliest epoch, and the best
// network is restored. Early stopping ends training after Patience
// evaluations without improvement. TrainBatch fails without changing the
// network when a step diverges.
func train(ctx context.Context, prepared PreparedDataset, config trainingconfig.Config, progress func(int, float64)) (*nn.Network, trainingreport.Selection, error) {
	selection := trainingreport.Selection{
		RequestedEpochs:  config.MaxEpochs,
		StopReason:       trainingreport.StopMaxEpochs,
		CheckpointPolicy: config.Checkpoint.Policy,
	}

	network, err := nn.NewNetwork(config.Seed, config.Sizes()...)
	if err != nil {
		return nil, selection, err
	}

	samples := make([]nn.Sample, len(prepared.Train))
	for i, example := range prepared.Train {
		samples[i] = nn.Sample{Inputs: example.Values, Targets: []float64{target(example.Label)}}
	}

	checkpoint := config.Checkpoint
	selecting := checkpoint.Policy == trainingconfig.CheckpointBestValidation

	var best *nn.Network
	var bestValue float64
	bestEpoch, withoutImprovement := 0, 0

	for epoch := 1; epoch <= config.MaxEpochs; epoch++ {
		if err := ctx.Err(); err != nil {
			return nil, selection, err
		}

		loss, err := network.TrainBatch(samples, config.LearningRate)
		if err != nil {
			return nil, selection, fmt.Errorf("train epoch %d: %w", epoch-1, err)
		}

		if progress != nil {
			progress(epoch-1, loss)
		}

		selection.ActualEpochs = epoch

		if !selecting || (epoch%checkpoint.EvaluationInterval != 0 && epoch != config.MaxEpochs) {
			continue
		}

		value, err := objective(network, prepared.Validation, checkpoint.Objective)
		if err != nil {
			return nil, selection, fmt.Errorf("evaluate the validation objective at epoch %d: %w", epoch, err)
		}
		selection.Evaluations++

		if best == nil || improves(value, bestValue, checkpoint) {
			best, bestValue, bestEpoch, withoutImprovement = network.Clone(), value, epoch, 0
		} else {
			withoutImprovement++
		}

		if config.EarlyStopping.Policy == trainingconfig.EarlyStoppingPatience &&
			withoutImprovement >= config.EarlyStopping.Patience {
			selection.StopReason = trainingreport.StopEarlyStopping
			break
		}
	}

	if !selecting {
		selection.SelectedEpoch = selection.ActualEpochs
		return network, selection, nil
	}

	name := checkpoint.Objective
	selection.Objective = &name
	selection.SelectedEpoch = bestEpoch
	selection.SelectedObjectiveValue = &bestValue

	return best, selection, nil
}

func target(label trainingdata.Label) float64 {
	if label == trainingdata.Fraud {
		return 1
	}
	return 0
}

// improves reports whether value is better than best by more than the
// minimum improvement: lower for the validation loss, higher for PR-AUC.
func improves(value, best float64, checkpoint trainingconfig.Checkpoint) bool {
	if checkpoint.Objective == trainingconfig.ObjectiveValidationLoss {
		return value < best-checkpoint.MinImprovement
	}
	return value > best+checkpoint.MinImprovement
}

// objective measures the validation objective: the mean squared error, or
// the average precision.
func objective(network *nn.Network, examples []PreparedExample, name string) (float64, error) {
	predictions, err := predict(network, examples)
	if err != nil {
		return 0, err
	}

	if name == trainingconfig.ObjectiveValidationLoss {
		total := 0.0
		for _, p := range predictions {
			difference := p.Score - target(p.Label)
			total += difference * difference
		}
		return total / float64(len(predictions)), nil
	}

	metrics, err := evaluation.Evaluate(predictions, 0.5)
	if err != nil {
		return 0, err
	}
	if metrics.PRAUC == nil {
		return 0, errors.New("PR-AUC is undefined without validation fraud records")
	}
	return *metrics.PRAUC, nil
}

// chooseThreshold returns the configured threshold, or selects one from
// the validation predictions of the frozen network.
func chooseThreshold(network *nn.Network, validation []PreparedExample, policy trainingconfig.Threshold) (trainingreport.ThresholdChoice, error) {
	choice := trainingreport.ThresholdChoice{Policy: policy.Policy}

	if policy.Policy == trainingconfig.ThresholdFixed {
		choice.Value = policy.Value
		return choice, nil
	}

	predictions, err := predict(network, validation)
	if err != nil {
		return choice, err
	}

	selected, err := evaluation.SelectThreshold(predictions, policy.MaxFalsePositiveRate)
	if err != nil {
		return choice, err
	}

	rate, candidates := policy.MaxFalsePositiveRate, selected.Candidates
	choice.Value = selected.Threshold
	choice.MaxFalsePositiveRate = &rate
	choice.Candidates = &candidates

	return choice, nil
}

// predict scores the examples of one partition.
func predict(network *nn.Network, examples []PreparedExample) ([]evaluation.Prediction, error) {
	predictions := make([]evaluation.Prediction, len(examples))

	for i, example := range examples {
		outputs, err := network.Predict(example.Values)
		if err != nil {
			return nil, err
		}

		predictions[i] = evaluation.Prediction{
			Score:           outputs[0],
			Label:           example.Label,
			AmountCents:     example.AmountCents,
			LossAmountCents: example.LossAmountCents,
		}
	}

	return predictions, nil
}

// evaluate scores the examples of one partition and measures them.
func evaluate(network *nn.Network, examples []PreparedExample, threshold float64) (evaluation.Metrics, error) {
	predictions, err := predict(network, examples)
	if err != nil {
		return evaluation.Metrics{}, err
	}

	return evaluation.Evaluate(predictions, threshold)
}
