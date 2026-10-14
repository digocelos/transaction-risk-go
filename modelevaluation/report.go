package modelevaluation

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// Version is the version of the evaluation report layout.
const Version = "pix-evaluation-report-v1"

// Sources of the threshold an evaluation measures at.
const (
	ThresholdFromArtifact = "artifact"
	ThresholdOverride     = "override"
)

// Report is the evaluation report of one model on one scope of one dataset.
// Software is the build that evaluated, and Model.Software the build that
// trained.
type Report struct {
	EvaluationReportVersion string                           `json:"evaluation_report_version"`
	Software                modelartifact.Software           `json:"software"`
	Model                   Model                            `json:"model"`
	Dataset                 trainingdata.Report              `json:"dataset"`
	SameDatasetAsTraining   bool                             `json:"same_dataset_as_training"`
	Scope                   ScopeReport                      `json:"scope"`
	Records                 Records                          `json:"records"`
	Coverage                trainingreport.PartitionCoverage `json:"coverage"`
	GroupOverlap            GroupOverlap                     `json:"group_overlap"`
	Threshold               Threshold                        `json:"threshold"`
	Metrics                 evaluation.Metrics               `json:"metrics"`
	Completeness            Completeness                     `json:"completeness"`
}

// Model identifies the evaluated artifact by the SHA-256 of its exact
// bytes, and what produced it, as the artifact records.
type Model struct {
	ArtifactVersion       string                  `json:"artifact_version"`
	ArtifactSHA256        string                  `json:"artifact_sha256"`
	Software              modelartifact.Software  `json:"software"`
	Contracts             Contracts               `json:"contracts"`
	FeatureSchema         string                  `json:"feature_schema"`
	ConfigVersion         string                  `json:"config_version"`
	ConfigSHA256          string                  `json:"config_sha256"`
	TrainingReportVersion string                  `json:"training_report_version"`
	TrainingReportSHA256  string                  `json:"training_report_sha256"`
	TrainingDatasetSHA256 string                  `json:"training_dataset_sha256"`
	TrainEnd              time.Time               `json:"train_end"`
	ValidationEnd         time.Time               `json:"validation_end"`
	SelectedEpoch         int                     `json:"selected_epoch"`
	Threshold             modelartifact.Threshold `json:"threshold"`
}

// Where group overlap takes the model's train records from.
const (
	ReferenceEvaluatedDataset = "evaluated_dataset"
	ReferenceDataset          = "reference_dataset"
	ReferenceUnavailable      = "unavailable"
)

// GroupOverlap compares the group keys of the usable records in scope with
// those of the usable records in the train partition of the model's
// training dataset. Reference tells where the train records come from: the
// evaluated dataset, when it is the training dataset, or a reference
// dataset verified by its SHA-256. SharedKeys is the number of distinct
// keys in scope that train records also have, and RecordsSharingTrainKeys
// the number of usable records in scope with such a key; both are nil when
// the reference is unavailable. RecordsWithoutGroupKey is the number of
// usable records in scope that have no key and cannot be compared. Keys
// are never model inputs.
type GroupOverlap struct {
	Reference               string `json:"reference"`
	SharedKeys              *int   `json:"shared_keys"`
	RecordsSharingTrainKeys *int   `json:"records_sharing_train_keys"`
	RecordsWithoutGroupKey  int    `json:"records_without_group_key"`
}

// Contracts are the contract versions the model scores and was trained on.
type Contracts struct {
	ScoringInput    string `json:"scoring_input"`
	TrainingExample string `json:"training_example"`
}

// Threshold is the threshold every metric is measured at: the artifact's,
// or an override that applies to this evaluation only.
type Threshold struct {
	Source string  `json:"source"`
	Value  float64 `json:"value"`
}

// ScopeReport is the evaluated scope, resolved to decision times: after
// After, exclusive, through Through, inclusive, or unbounded when Through
// is nil. Partition names the artifact partition the scope came from, and
// is nil for a window.
type ScopeReport struct {
	Partition *evaluation.Partition `json:"partition"`
	After     time.Time             `json:"after"`
	Through   *time.Time            `json:"through"`
}

// Records counts the records that are not invalid. InScope counts the
// records in scope by outcome, with Fraud and Legitimate counting usable
// records only. InValidationRange is the number of usable records in scope
// whose decision time is at or before the model's validation_end, the
// period its checkpoint and threshold were chosen on.
type Records struct {
	InScope           evaluation.PartitionCounts `json:"in_scope"`
	OutOfScope        int                        `json:"out_of_scope"`
	InValidationRange int                        `json:"in_validation_range"`
}

// Marshal validates a report and encodes it deterministically: the same
// report always produces the same bytes, timestamps are written in UTC, and
// the output ends with a newline.
func Marshal(report Report) ([]byte, error) {
	if err := validate(report); err != nil {
		return nil, fmt.Errorf("invalid evaluation report: %w", err)
	}

	report.EvaluationReportVersion = Version
	report.Model.TrainEnd = report.Model.TrainEnd.UTC()
	report.Model.ValidationEnd = report.Model.ValidationEnd.UTC()
	report.Scope.After = report.Scope.After.UTC()
	if through := report.Scope.Through; through != nil {
		utc := through.UTC()
		report.Scope.Through = &utc
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode evaluation report: %w", err)
	}

	return append(data, '\n'), nil
}

func validate(report Report) error {
	model, threshold := report.Model, report.Threshold

	switch {
	case threshold.Source != ThresholdFromArtifact && threshold.Source != ThresholdOverride:
		return fmt.Errorf("unknown threshold source %q", threshold.Source)
	case threshold.Source == ThresholdFromArtifact && threshold.Value != model.Threshold.Value:
		return errors.New("the artifact's threshold must be the model's threshold")
	case !(threshold.Value >= 0 && threshold.Value <= 1):
		return errors.New("the threshold must be a number from 0 to 1")
	case report.Metrics.Threshold != threshold.Value:
		return errors.New("the metrics must use the evaluation's threshold")
	case report.Dataset.Records.Invalid != 0:
		return errors.New("a dataset with invalid records cannot be evaluated")
	case report.SameDatasetAsTraining != (report.Dataset.DatasetSHA256 == model.TrainingDatasetSHA256):
		return errors.New("same_dataset_as_training must compare the dataset with the training dataset")
	}

	if err := validateScope(report.Scope, model); err != nil {
		return err
	}
	if err := validateOverlap(report.GroupOverlap, report.SameDatasetAsTraining); err != nil {
		return err
	}

	inScope, metrics := report.Records.InScope, report.Metrics
	usable := inScope.Fraud + inScope.Legitimate

	switch {
	case metrics.TruePositives+metrics.FalseNegatives != inScope.Fraud ||
		metrics.FalsePositives+metrics.TrueNegatives != inScope.Legitimate:
		return errors.New("the metrics must count the usable records in scope")
	case report.Coverage.UsableRecords != usable:
		return errors.New("the coverage must count the usable records in scope")
	case !reflect.DeepEqual(report.Completeness, newCompleteness(inScope, metrics)):
		return errors.New("the completeness must follow the records in scope and the metrics")
	}

	return nil
}

func validateScope(scope ScopeReport, model Model) error {
	switch {
	case scope.After.Before(model.TrainEnd):
		return errors.New("the scope must not start before the model's train_end")
	case scope.Through != nil && !scope.Through.After(scope.After):
		return errors.New("the scope must end after its start")
	case scope.Partition == nil:
		return nil
	}

	switch *scope.Partition {
	case evaluation.Validation:
		if !scope.After.Equal(model.TrainEnd) || scope.Through == nil || !scope.Through.Equal(model.ValidationEnd) {
			return errors.New("the validation scope must use the model's boundaries")
		}
	case evaluation.Test:
		if !scope.After.Equal(model.ValidationEnd) || scope.Through != nil {
			return errors.New("the test scope must use the model's boundaries")
		}
	default:
		return fmt.Errorf("unsupported scope partition %q", *scope.Partition)
	}

	return nil
}

func validateOverlap(overlap GroupOverlap, sameDataset bool) error {
	measured := overlap.SharedKeys != nil && overlap.RecordsSharingTrainKeys != nil

	switch overlap.Reference {
	case ReferenceUnavailable:
		if overlap.SharedKeys != nil || overlap.RecordsSharingTrainKeys != nil {
			return errors.New("an unavailable group overlap has no counts")
		}
	case ReferenceEvaluatedDataset:
		if !sameDataset || !measured {
			return errors.New("an overlap with the evaluated dataset needs the training dataset and counts")
		}
	case ReferenceDataset:
		if !measured {
			return errors.New("an overlap with a reference dataset needs counts")
		}
	default:
		return fmt.Errorf("unknown group overlap reference %q", overlap.Reference)
	}

	return nil
}
