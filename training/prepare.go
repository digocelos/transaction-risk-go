package training

import (
	"fmt"
	"io"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// PreparedExample is one usable record prepared with pix-risk-v1. Prepared
// holds its values before standardization and Values the values a network
// receives, after the fitted state.
type PreparedExample struct {
	Line            int
	Prepared        pixrisk.Prepared
	Values          []float64
	Label           trainingdata.Label
	AmountCents     int64
	LossAmountCents *int64
}

// PreparedDataset is a pix-training-example-v2 dataset prepared with
// pix-risk-v1: its dataset report, its partitions, the state fitted on the
// train partition, and the usable records of each partition in line order.
type PreparedDataset struct {
	Dataset    trainingdata.Report
	Partitions evaluation.PartitionReport
	State      pixrisk.State
	Train      []PreparedExample
	Validation []PreparedExample
	Test       []PreparedExample
}

// PrepareDataset reads and classifies every record of a dataset with
// pix-risk-v1, partitions it by decision time, fits the preparation state
// on the usable train records only, and applies that frozen state to every
// partition. The boundaries are validated before the dataset is read. A
// dataset with invalid records yields an *InvalidDatasetError, and a
// standardized feature that no usable train record has yields a
// *pixrisk.FeatureNeverKnownError.
func PrepareDataset(dataset io.Reader, boundaries evaluation.Boundaries) (PreparedDataset, error) {
	partitioner, err := evaluation.NewPartitioner(boundaries)
	if err != nil {
		return PreparedDataset{}, err
	}

	partitions := map[evaluation.Partition][]PreparedExample{}
	var exclusions []trainingdata.Exclusion
	var diagnostics []trainingdata.Diagnostic

	summary, err := trainingdata.Scan(dataset, trainingdata.PixRiskV1, func(record trainingdata.Record) error {
		if exclusion, ok := record.Exclusion(); ok {
			exclusions = append(exclusions, exclusion)
		}
		diagnostics = append(diagnostics, record.Diagnostics...)

		partition, ok := partitioner.Add(record)
		if !ok || record.Category != trainingdata.Usable {
			return nil
		}

		prepared, err := pixrisk.Prepare(record.Example.ScoringInput)
		if err != nil {
			return fmt.Errorf("line %d: %w", record.Line, err)
		}

		partitions[partition] = append(partitions[partition], PreparedExample{
			Line:            record.Line,
			Prepared:        prepared,
			Label:           record.Example.Outcome.Label,
			AmountCents:     record.Example.ScoringInput.PixPayment.AmountCents,
			LossAmountCents: record.Example.Outcome.LossAmountCents,
		})
		return nil
	})
	if err != nil {
		return PreparedDataset{}, err
	}

	report := trainingdata.NewReport(summary, exclusions)
	if summary.Records.Invalid > 0 {
		return PreparedDataset{}, &InvalidDatasetError{Report: report, Diagnostics: diagnostics}
	}

	train := make([]pixrisk.Prepared, len(partitions[evaluation.Train]))
	for i, example := range partitions[evaluation.Train] {
		train[i] = example.Prepared
	}

	state, err := pixrisk.Fit(train)
	if err != nil {
		return PreparedDataset{}, err
	}

	for _, partition := range []evaluation.Partition{evaluation.Train, evaluation.Validation, evaluation.Test} {
		for i := range partitions[partition] {
			example := &partitions[partition][i]

			example.Values, err = state.Apply(example.Prepared)
			if err != nil {
				return PreparedDataset{}, fmt.Errorf("line %d: %w", example.Line, err)
			}
		}
	}

	return PreparedDataset{
		Dataset:    report,
		Partitions: partitioner.Report(),
		State:      state,
		Train:      partitions[evaluation.Train],
		Validation: partitions[evaluation.Validation],
		Test:       partitions[evaluation.Test],
	}, nil
}
