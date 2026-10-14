// Package modelevaluation evaluates a saved pix-risk-model-v1 artifact on
// an explicit scope of a pix-training-example-v2 dataset and writes the
// evaluation report pix-evaluation-report-v1, as specified in
// docs/contracts/pix-evaluation-report-v1.md. Evaluation only reads: it
// never trains, refits the preparation state, selects a checkpoint, or
// tunes a threshold, and it never changes the artifact or the dataset
// (ADR 0017).
package modelevaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// Scope selects the records an evaluation measures by the decision time of
// their scoring input. A partition scope uses the boundaries stored in the
// artifact: validation is after train_end through validation_end, and test
// is after validation_end. A window scope is after After, exclusive,
// through Through, inclusive, or unbounded when Through is nil; After must
// not be earlier than the artifact's train_end, so the train partition is
// never evaluated.
type Scope struct {
	Partition evaluation.Partition
	After     time.Time
	Through   *time.Time
}

// PartitionScope returns the scope of a partition of the artifact.
func PartitionScope(partition evaluation.Partition) Scope {
	return Scope{Partition: partition}
}

// WindowScope returns the scope of decision times after after, through
// through when it is not nil.
func WindowScope(after time.Time, through *time.Time) Scope {
	return Scope{After: after, Through: through}
}

// Options configure an evaluation. Threshold, when not nil, overrides the
// artifact's threshold for this evaluation only, and the report labels it.
// Reference, when not nil, is the dataset the model was trained on; its
// SHA-256 must be the artifact's dataset_sha256, and its train partition
// is used to measure group overlap.
type Options struct {
	Scope     Scope
	Threshold *float64
	Reference io.Reader
}

// cancellationInterval is the number of records read between two checks of
// the context.
const cancellationInterval = 1024

// Evaluate decodes a model artifact from its exact bytes, reads and
// classifies every record of a dataset with pix-risk-v1, scores the usable
// records in scope with the artifact's preparation state and network, and
// measures them at the artifact's threshold or at the override. A reference
// dataset is read first and must be the model's training dataset. A
// dataset with invalid records yields a *training.InvalidDatasetError and
// no report. The artifact, the scope, and the override are validated before
// any dataset is read, and ctx is checked before reading and every 1,024
// records.
func Evaluate(ctx context.Context, model []byte, dataset io.Reader, options Options) (Report, error) {
	artifact, err := modelartifact.Decode(model)
	if err != nil {
		return Report{}, err
	}

	scope, err := resolve(options.Scope, artifact.Training)
	if err != nil {
		return Report{}, err
	}

	threshold := Threshold{Source: ThresholdFromArtifact, Value: artifact.Threshold.Value}
	if override := options.Threshold; override != nil {
		if !(*override >= 0 && *override <= 1) {
			return Report{}, fmt.Errorf("the threshold override must be a number from 0 to 1, got %v", *override)
		}
		threshold = Threshold{Source: ThresholdOverride, Value: *override}
	}

	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	var reference map[string]bool
	if options.Reference != nil {
		reference, err = referenceKeys(ctx, options.Reference, artifact.Training)
		if err != nil {
			return Report{}, err
		}
	}

	scanned, err := scan(ctx, dataset, artifact, scope)
	if err != nil {
		return Report{}, err
	}

	metrics, err := evaluation.Evaluate(scanned.predictions, threshold.Value)
	if err != nil {
		return Report{}, err
	}

	sameDataset := scanned.dataset.DatasetSHA256 == artifact.Training.DatasetSHA256

	return Report{
		Software:              modelartifact.SoftwareFromBuild(),
		Model:                 identify(artifact, sha256Hex(model)),
		Dataset:               scanned.dataset,
		SameDatasetAsTraining: sameDataset,
		Scope:                 scope.report(),
		Records:               scanned.records,
		Coverage:              trainingreport.NewPartitionCoverage(scanned.prepared),
		GroupOverlap:          scanned.overlap(reference, sameDataset),
		Threshold:             threshold,
		Metrics:               metrics,
		Completeness:          newCompleteness(scanned.records.InScope, metrics),
	}, nil
}

// scanned is what one read of the evaluated dataset collects: its report,
// the record counts, the predictions and prepared values of the usable
// records in scope, and the group keys of its usable train records and of
// the usable records in scope.
type scanned struct {
	dataset     trainingdata.Report
	records     Records
	predictions []evaluation.Prediction
	prepared    []pixrisk.Prepared
	trainKeys   map[string]bool
	scopeKeys   map[string]int
	withoutKey  int
}

// scan reads, classifies, and scores the evaluated dataset.
func scan(ctx context.Context, dataset io.Reader, artifact modelartifact.Artifact, scope window) (scanned, error) {
	result := scanned{trainKeys: map[string]bool{}, scopeKeys: map[string]int{}}
	var exclusions []trainingdata.Exclusion
	var diagnostics []trainingdata.Diagnostic

	summary, err := trainingdata.Scan(dataset, trainingdata.PixRiskV1, func(record trainingdata.Record) error {
		if record.Line%cancellationInterval == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}

		if exclusion, ok := record.Exclusion(); ok {
			exclusions = append(exclusions, exclusion)
		}
		diagnostics = append(diagnostics, record.Diagnostics...)

		if key, ok := trainKey(record, artifact.Training.TrainEnd); ok {
			result.trainKeys[key] = true
		}

		if record.Category == trainingdata.Invalid {
			return nil
		}

		decisionTime := record.Example.ScoringInput.DecisionTime
		if !scope.contains(decisionTime) {
			result.records.OutOfScope++
			return nil
		}

		return result.add(artifact, record)
	})
	if err != nil {
		return scanned{}, err
	}

	result.dataset = trainingdata.NewReport(summary, exclusions)
	if summary.Records.Invalid > 0 {
		return scanned{}, &training.InvalidDatasetError{Report: result.dataset, Diagnostics: diagnostics}
	}

	return result, nil
}

// add counts a record in scope and scores it when it is usable.
func (s *scanned) add(artifact modelartifact.Artifact, record trainingdata.Record) error {
	switch record.Category {
	case trainingdata.Insufficient:
		s.records.InScope.Insufficient++
		return nil
	case trainingdata.Immature:
		s.records.InScope.Immature++
		return nil
	}

	example := record.Example
	if example.Outcome.Label == trainingdata.Fraud {
		s.records.InScope.Fraud++
	} else {
		s.records.InScope.Legitimate++
	}
	if !example.ScoringInput.DecisionTime.After(artifact.Training.ValidationEnd) {
		s.records.InValidationRange++
	}
	if example.GroupKey == nil {
		s.withoutKey++
	} else {
		s.scopeKeys[*example.GroupKey]++
	}

	prepared, score, err := scoreRecord(artifact, example)
	if err != nil {
		return fmt.Errorf("line %d: %w", record.Line, err)
	}

	s.prepared = append(s.prepared, prepared)
	s.predictions = append(s.predictions, evaluation.Prediction{
		Score:           score,
		Label:           example.Outcome.Label,
		AmountCents:     example.ScoringInput.PixPayment.AmountCents,
		LossAmountCents: example.Outcome.LossAmountCents,
	})
	return nil
}

// overlap compares the keys in scope with the train keys of the reference
// dataset, or of the evaluated dataset when it is the training dataset.
func (s *scanned) overlap(reference map[string]bool, sameDataset bool) GroupOverlap {
	overlap := GroupOverlap{Reference: ReferenceUnavailable, RecordsWithoutGroupKey: s.withoutKey}

	trainKeys := reference
	switch {
	case reference != nil:
		overlap.Reference = ReferenceDataset
	case sameDataset:
		overlap.Reference = ReferenceEvaluatedDataset
		trainKeys = s.trainKeys
	default:
		return overlap
	}

	shared, records := 0, 0
	for key, count := range s.scopeKeys {
		if trainKeys[key] {
			shared++
			records += count
		}
	}

	overlap.SharedKeys, overlap.RecordsSharingTrainKeys = &shared, &records
	return overlap
}

// trainKey returns the group key of a usable record in the train partition
// of the model.
func trainKey(record trainingdata.Record, trainEnd time.Time) (string, bool) {
	if record.Category != trainingdata.Usable || record.Example.GroupKey == nil ||
		record.Example.ScoringInput.DecisionTime.After(trainEnd) {
		return "", false
	}
	return *record.Example.GroupKey, true
}

// referenceKeys reads a reference dataset, checks that it is the model's
// training dataset, and returns the group keys of its usable train records.
func referenceKeys(ctx context.Context, reference io.Reader, trained modelartifact.Training) (map[string]bool, error) {
	keys := map[string]bool{}

	summary, err := trainingdata.Scan(reference, trainingdata.PixRiskV1, func(record trainingdata.Record) error {
		if record.Line%cancellationInterval == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}

		if key, ok := trainKey(record, trained.TrainEnd); ok {
			keys[key] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reference dataset: %w", err)
	}

	if summary.DatasetSHA256 != trained.DatasetSHA256 {
		return nil, fmt.Errorf("the reference dataset (SHA-256 %s) is not the model's training dataset (SHA-256 %s)",
			summary.DatasetSHA256, trained.DatasetSHA256)
	}

	return keys, nil
}

// identify describes a decoded artifact. Decoding accepts only the
// versions it names.
func identify(artifact modelartifact.Artifact, artifactSHA256 string) Model {
	trained := artifact.Training

	return Model{
		ArtifactVersion:       modelartifact.Version,
		ArtifactSHA256:        artifactSHA256,
		Software:              artifact.Software,
		Contracts:             Contracts{ScoringInput: pixinput.ContractV2, TrainingExample: trainingdata.TrainingExampleV2},
		FeatureSchema:         pixrisk.Schema,
		ConfigVersion:         trainingconfig.Version,
		ConfigSHA256:          trained.ConfigSHA256,
		TrainingReportVersion: trainingreport.Version,
		TrainingReportSHA256:  trained.ReportSHA256,
		TrainingDatasetSHA256: trained.DatasetSHA256,
		TrainEnd:              trained.TrainEnd.UTC(),
		ValidationEnd:         trained.ValidationEnd.UTC(),
		SelectedEpoch:         artifact.SelectedEpoch,
		Threshold:             artifact.Threshold,
	}
}

// window is a resolved scope.
type window struct {
	partition evaluation.Partition
	after     time.Time
	through   *time.Time
}

func (w window) report() ScopeReport {
	report := ScopeReport{After: w.after, Through: w.through}
	if w.partition != "" {
		partition := w.partition
		report.Partition = &partition
	}
	return report
}

func (w window) contains(decisionTime time.Time) bool {
	return decisionTime.After(w.after) && (w.through == nil || !decisionTime.After(*w.through))
}

// resolve turns a scope into a window over the artifact's boundaries.
func resolve(scope Scope, trained modelartifact.Training) (window, error) {
	trainEnd, validationEnd := trained.TrainEnd.UTC(), trained.ValidationEnd.UTC()

	if scope.Partition != "" {
		if !scope.After.IsZero() || scope.Through != nil {
			return window{}, errors.New("a scope is either a partition or a window, not both")
		}

		switch scope.Partition {
		case evaluation.Validation:
			return window{partition: evaluation.Validation, after: trainEnd, through: &validationEnd}, nil
		case evaluation.Test:
			return window{partition: evaluation.Test, after: validationEnd}, nil
		case evaluation.Train:
			return window{}, errors.New("the train partition is never evaluated")
		default:
			return window{}, fmt.Errorf("unknown partition %q", scope.Partition)
		}
	}

	switch {
	case scope.After.IsZero():
		return window{}, errors.New("a scope needs a partition or a window start")
	case scope.After.Before(trainEnd):
		return window{}, fmt.Errorf("the window start %s must not be earlier than the model's train_end %s",
			scope.After.UTC().Format(time.RFC3339Nano), trainEnd.Format(time.RFC3339Nano))
	case scope.Through != nil && !scope.Through.After(scope.After):
		return window{}, errors.New("the window end must be later than its start")
	}

	resolved := window{after: scope.After.UTC()}
	if scope.Through != nil {
		through := scope.Through.UTC()
		resolved.through = &through
	}

	return resolved, nil
}

// scoreRecord prepares the scoring input of a usable example with
// pix-risk-v1, applies the artifact's frozen preparation state, and scores
// it.
func scoreRecord(artifact modelartifact.Artifact, example trainingdata.Example) (pixrisk.Prepared, float64, error) {
	prepared, err := pixrisk.Prepare(example.ScoringInput)
	if err != nil {
		return pixrisk.Prepared{}, 0, err
	}

	values, err := artifact.State.Apply(prepared)
	if err != nil {
		return pixrisk.Prepared{}, 0, err
	}

	outputs, err := artifact.Network.Predict(values)
	if err != nil {
		return pixrisk.Prepared{}, 0, err
	}

	return prepared, outputs[0], nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
