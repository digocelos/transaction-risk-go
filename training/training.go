// Package training trains the transaction risk baseline from a
// pix-training-example-v1 dataset and evaluates it with the temporal
// evaluation protocol: the network is fitted on the train partition only,
// then the validation and test partitions are each scored once at a fixed
// threshold. It never reads the system clock or shuffles records.
package training

import (
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
	"github.com/digocelos/transaction-risk-go/transaction"
)

// HiddenNeurons is the size of the baseline's hidden layer: the network is
// 4-4-1, as in cmd/demo.
const HiddenNeurons = 4

// Config is a training run configuration. Every value is explicit; none
// has a default.
type Config struct {
	Boundaries   evaluation.Boundaries
	Seed         int64
	Epochs       int
	LearningRate float64
	Threshold    float64
}

// Result is a trained network and the report of its run.
type Result struct {
	Network *nn.Network
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

// example is a usable record reduced to what training and evaluation read.
type example struct {
	features        [transaction.FeatureCount]float64
	label           trainingdata.Label
	amountCents     int64
	lossAmountCents *int64
}

// Run reads and classifies every record of a dataset, trains the baseline
// on the usable train records, and evaluates the validation and test
// partitions. progress, when not nil, receives the training loss of each
// epoch, measured before that epoch's update. The configuration is
// validated before the dataset is read.
func Run(dataset io.Reader, config Config, progress func(epoch int, loss float64)) (Result, error) {
	if err := validate(config); err != nil {
		return Result{}, err
	}

	partitioner, err := evaluation.NewPartitioner(config.Boundaries)
	if err != nil {
		return Result{}, err
	}

	examples := map[evaluation.Partition][]example{}
	var exclusions []trainingdata.Exclusion
	var diagnostics []trainingdata.Diagnostic

	summary, err := trainingdata.Scan(dataset, trainingdata.TransactionFeaturesV1,
		func(record trainingdata.Record) error {
			if exclusion, ok := record.Exclusion(); ok {
				exclusions = append(exclusions, exclusion)
			}
			diagnostics = append(diagnostics, record.Diagnostics...)

			partition, ok := partitioner.Add(record)
			if !ok || record.Category != trainingdata.Usable {
				return nil
			}

			features, err := transaction.FromScoringInput(record.Example.ScoringInput)
			if err != nil {
				return fmt.Errorf("line %d: %w", record.Line, err)
			}

			examples[partition] = append(examples[partition], example{
				features:        features,
				label:           record.Example.Outcome.Label,
				amountCents:     record.Example.ScoringInput.PixPayment.AmountCents,
				lossAmountCents: record.Example.Outcome.LossAmountCents,
			})
			return nil
		})
	if err != nil {
		return Result{}, err
	}

	datasetReport := trainingdata.NewReport(summary, exclusions)
	if summary.Records.Invalid > 0 {
		return Result{}, &InvalidDatasetError{Report: datasetReport, Diagnostics: diagnostics}
	}

	partitions := partitioner.Report()
	if partitions.Train.Fraud == 0 || partitions.Train.Legitimate == 0 {
		return Result{}, &UntrainableError{Train: partitions.Train}
	}

	network, err := train(examples[evaluation.Train], config, progress)
	if err != nil {
		return Result{}, err
	}

	validation, err := evaluate(network, examples[evaluation.Validation], config.Threshold)
	if err != nil {
		return Result{}, fmt.Errorf("evaluate validation partition: %w", err)
	}

	test, err := evaluate(network, examples[evaluation.Test], config.Threshold)
	if err != nil {
		return Result{}, fmt.Errorf("evaluate test partition: %w", err)
	}

	return Result{
		Network: network,
		Report: trainingreport.Report{
			Configuration: trainingreport.Configuration{
				Algorithm:     nn.AlgorithmFullBatchGradientDescent,
				FeatureSchema: transaction.FeatureSchema,
				Seed:          config.Seed,
				Epochs:        config.Epochs,
				LearningRate:  config.LearningRate,
				Threshold:     config.Threshold,
			},
			Dataset:    datasetReport,
			Partitions: partitions,
			Validation: validation,
			Test:       test,
		},
	}, nil
}

func validate(config Config) error {
	switch {
	case config.Epochs < 1:
		return errors.New("epochs must be at least 1")
	case !(config.LearningRate > 0) || math.IsInf(config.LearningRate, 0):
		return errors.New("learning rate must be finite and positive")
	case !(config.Threshold >= 0 && config.Threshold <= 1):
		return errors.New("threshold must be a number from 0 to 1")
	}

	return nil
}

// train fits a new baseline network with full-batch gradient descent, in
// dataset order.
func train(examples []example, config Config, progress func(int, float64)) (*nn.Network, error) {
	network, err := nn.NewNetwork(config.Seed, transaction.FeatureCount, HiddenNeurons, 1)
	if err != nil {
		return nil, err
	}

	samples := make([]nn.Sample, len(examples))
	for i, example := range examples {
		target := 0.0
		if example.label == trainingdata.Fraud {
			target = 1
		}

		samples[i] = nn.Sample{Inputs: examples[i].features[:], Targets: []float64{target}}
	}

	for epoch := 0; epoch < config.Epochs; epoch++ {
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
func evaluate(network *nn.Network, examples []example, threshold float64) (evaluation.Metrics, error) {
	predictions := make([]evaluation.Prediction, len(examples))

	for i, example := range examples {
		outputs, err := network.Predict(example.features[:])
		if err != nil {
			return evaluation.Metrics{}, err
		}

		predictions[i] = evaluation.Prediction{
			Score:           outputs[0],
			Label:           example.label,
			AmountCents:     example.amountCents,
			LossAmountCents: example.lossAmountCents,
		}
	}

	return evaluation.Evaluate(predictions, threshold)
}
