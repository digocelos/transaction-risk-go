// Package training trains a payer-side Pix fraud model from a
// pix-training-example-v2 dataset, as a pix-training-config-v1
// configuration describes, and evaluates it with the temporal evaluation
// protocol: the preparation state and the network are fitted on the train
// partition only, then the validation and test partitions are each scored
// once at a fixed threshold. It never reads the system clock or shuffles
// records.
package training

import (
	"context"
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

// Run validates the configuration, prepares the dataset with pix-risk-v1,
// trains the configured network on the usable train records in line order
// for MaxEpochs full-batch epochs, and evaluates the validation and test
// partitions at the fixed threshold. progress, when not nil, receives the
// training loss of each epoch, measured before that epoch's update. ctx is
// checked every 1,024 records while reading and before every epoch; a
// canceled run returns ctx's error and no result.
func Run(ctx context.Context, dataset io.Reader, config trainingconfig.Config, progress func(epoch int, loss float64)) (Result, error) {
	if err := config.Validate(); err != nil {
		return Result{}, fmt.Errorf("invalid training configuration: %w", err)
	}

	prepared, err := PrepareDataset(ctx, dataset, config.Boundaries())
	if err != nil {
		return Result{}, err
	}

	network, err := train(ctx, prepared.Train, config, progress)
	if err != nil {
		return Result{}, err
	}

	validation, err := evaluate(network, prepared.Validation, config.Threshold.Value)
	if err != nil {
		return Result{}, fmt.Errorf("evaluate validation partition: %w", err)
	}

	test, err := evaluate(network, prepared.Test, config.Threshold.Value)
	if err != nil {
		return Result{}, fmt.Errorf("evaluate test partition: %w", err)
	}

	return Result{
		Network: network,
		State:   prepared.State,
		Report: trainingreport.Report{
			Configuration: trainingreport.Configuration{
				Algorithm:     config.Algorithm,
				FeatureSchema: config.FeatureSchema,
				Seed:          config.Seed,
				Epochs:        config.MaxEpochs,
				LearningRate:  config.LearningRate,
				Threshold:     config.Threshold.Value,
			},
			Dataset:    prepared.Dataset,
			Partitions: prepared.Partitions,
			Validation: validation,
			Test:       test,
		},
	}, nil
}

// train fits a new network with full-batch gradient descent, in dataset
// order. TrainBatch fails without changing the network when a step
// diverges.
func train(ctx context.Context, examples []PreparedExample, config trainingconfig.Config, progress func(int, float64)) (*nn.Network, error) {
	network, err := nn.NewNetwork(config.Seed, config.Sizes()...)
	if err != nil {
		return nil, err
	}

	samples := make([]nn.Sample, len(examples))
	for i, example := range examples {
		target := 0.0
		if example.Label == trainingdata.Fraud {
			target = 1
		}

		samples[i] = nn.Sample{Inputs: example.Values, Targets: []float64{target}}
	}

	for epoch := 0; epoch < config.MaxEpochs; epoch++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		loss, err := network.TrainBatch(samples, config.LearningRate)
		if err != nil {
			return nil, fmt.Errorf("train epoch %d: %w", epoch, err)
		}

		if progress != nil {
			progress(epoch, loss)
		}
	}

	return network, nil
}

// evaluate scores the examples of one partition and measures them.
func evaluate(network *nn.Network, examples []PreparedExample, threshold float64) (evaluation.Metrics, error) {
	predictions := make([]evaluation.Prediction, len(examples))

	for i, example := range examples {
		outputs, err := network.Predict(example.Values)
		if err != nil {
			return evaluation.Metrics{}, err
		}

		predictions[i] = evaluation.Prediction{
			Score:           outputs[0],
			Label:           example.Label,
			AmountCents:     example.AmountCents,
			LossAmountCents: example.LossAmountCents,
		}
	}

	return evaluation.Evaluate(predictions, threshold)
}
