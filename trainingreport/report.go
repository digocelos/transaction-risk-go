// Package trainingreport writes the training report of a trained model,
// pix-training-report-v2, as specified in
// docs/contracts/pix-training-report-v2.md: the configuration and its
// identity, the dataset report, the temporal partitions, the coverage of
// populations and availability states, the model selection, and the
// validation and test metrics. A trained model binds its report by the
// SHA-256 of the exact report bytes (ADR 0010).
package trainingreport

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// Version is the version of the training report layout.
const Version = "pix-training-report-v2"

// Stop reasons of a training run.
const (
	StopMaxEpochs     = "max_epochs"
	StopEarlyStopping = "early_stopping"
)

// Report is the training report. The train partition is never evaluated;
// Test is nil unless the configuration asked for the test evaluation.
type Report struct {
	TrainingReportVersion string                     `json:"training_report_version"`
	Configuration         Configuration              `json:"configuration"`
	Dataset               trainingdata.Report        `json:"dataset"`
	Partitions            evaluation.PartitionReport `json:"partitions"`
	Coverage              Coverage                   `json:"coverage"`
	Selection             Selection                  `json:"selection"`
	Validation            evaluation.Metrics         `json:"validation"`
	TestEvaluated         bool                       `json:"test_evaluated"`
	Test                  *evaluation.Metrics        `json:"test"`
}

// Configuration is the run configuration. Marshal fills SHA256 and
// Canonical from Config.
type Configuration struct {
	Config    trainingconfig.Config `json:"-"`
	SHA256    string                `json:"config_sha256"`
	Canonical json.RawMessage       `json:"config"`
}

// Selection describes how the model and its threshold were chosen.
// Objective and SelectedObjectiveValue are nil, and Evaluations is 0, for
// the final_epoch checkpoint.
type Selection struct {
	RequestedEpochs        int             `json:"requested_epochs"`
	ActualEpochs           int             `json:"actual_epochs"`
	SelectedEpoch          int             `json:"selected_epoch"`
	StopReason             string          `json:"stop_reason"`
	CheckpointPolicy       string          `json:"checkpoint_policy"`
	Objective              *string         `json:"objective"`
	Evaluations            int             `json:"evaluations"`
	SelectedObjectiveValue *float64        `json:"selected_objective_value"`
	Threshold              ThresholdChoice `json:"threshold"`
}

// ThresholdChoice is the decision threshold and how it was chosen.
// MaxFalsePositiveRate and Candidates are nil for a fixed threshold.
type ThresholdChoice struct {
	Policy               string   `json:"policy"`
	Value                float64  `json:"value"`
	MaxFalsePositiveRate *float64 `json:"max_false_positive_rate"`
	Candidates           *int     `json:"candidates"`
}

// Coverage counts what each partition contains, so a reader sees which
// populations and availability states the model was trained and evaluated
// on. It describes data, not detection quality.
type Coverage struct {
	Train               PartitionCoverage `json:"train"`
	Validation          PartitionCoverage `json:"validation"`
	Test                PartitionCoverage `json:"test"`
	NotExercisedInTrain []SourceState     `json:"not_exercised_in_train"`
}

// PartitionCoverage counts the usable records of one partition by
// population, limit period, and availability state of each optional source.
type PartitionCoverage struct {
	UsableRecords int                       `json:"usable_records"`
	Populations   map[string]int            `json:"populations"`
	LimitPeriods  map[string]int            `json:"limit_periods"`
	Sources       map[string]map[string]int `json:"sources"`
}

// SourceState names one availability state of one optional source.
type SourceState struct {
	Source string `json:"source"`
	State  string `json:"state"`
}

// NewCoverage counts the prepared usable records of each partition, and
// lists every possible state of every source that no train record has, in
// source order.
func NewCoverage(train, validation, test []pixrisk.Prepared) Coverage {
	coverage := Coverage{
		Train:               partitionCoverage(train),
		Validation:          partitionCoverage(validation),
		Test:                partitionCoverage(test),
		NotExercisedInTrain: []SourceState{},
	}

	for _, source := range pixrisk.Sources() {
		for _, state := range pixrisk.PossibleStates(source) {
			if coverage.Train.Sources[source][state] == 0 {
				coverage.NotExercisedInTrain = append(coverage.NotExercisedInTrain, SourceState{source, state})
			}
		}
	}

	return coverage
}

func partitionCoverage(records []pixrisk.Prepared) PartitionCoverage {
	coverage := PartitionCoverage{
		UsableRecords: len(records),
		Populations:   map[string]int{},
		LimitPeriods:  map[string]int{},
		Sources:       map[string]map[string]int{},
	}

	for _, population := range pixrisk.Populations() {
		coverage.Populations[population] = 0
	}
	for _, period := range pixrisk.LimitPeriods() {
		coverage.LimitPeriods[period] = 0
	}
	for _, source := range pixrisk.Sources() {
		coverage.Sources[source] = map[string]int{}
		for _, state := range pixrisk.PossibleStates(source) {
			coverage.Sources[source][state] = 0
		}
	}

	for _, record := range records {
		for _, population := range record.Populations() {
			coverage.Populations[population]++
		}
		coverage.LimitPeriods[record.LimitPeriod()]++
		for source, state := range record.SourceStates() {
			coverage.Sources[source][state]++
		}
	}

	return coverage
}

// Marshal validates a report and encodes it deterministically: the same
// report always produces the same bytes, timestamps are written in UTC, and
// the output ends with a newline. It fills the configuration's canonical
// form and SHA-256. The model's report_sha256 is the SHA-256 of exactly
// these bytes.
func Marshal(report Report) ([]byte, error) {
	canonical, err := report.Configuration.Config.Canonical()
	if err != nil {
		return nil, fmt.Errorf("invalid training report: %w", err)
	}

	sum, err := report.Configuration.Config.SHA256()
	if err != nil {
		return nil, fmt.Errorf("invalid training report: %w", err)
	}

	if err := validate(report); err != nil {
		return nil, fmt.Errorf("invalid training report: %w", err)
	}

	report.TrainingReportVersion = Version
	report.Configuration.Canonical = canonical
	report.Configuration.SHA256 = sum
	report.Partitions.TrainEnd = report.Partitions.TrainEnd.UTC()
	report.Partitions.ValidationEnd = report.Partitions.ValidationEnd.UTC()

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode training report: %w", err)
	}

	return append(data, '\n'), nil
}

// Provenance returns the model provenance that a report describes, binding
// the report by reportSHA256. The epochs are those of the selected
// checkpoint, and the threshold is the selected one.
func Provenance(report Report, reportSHA256 string) nn.Provenance {
	config := report.Configuration.Config
	records := report.Dataset.Records

	return nn.Provenance{
		Algorithm:              config.Algorithm,
		Seed:                   config.Seed,
		Epochs:                 report.Selection.SelectedEpoch,
		LearningRate:           config.LearningRate,
		TrainingExampleVersion: report.Dataset.TrainingExampleVersion,
		DatasetSHA256:          report.Dataset.DatasetSHA256,
		DatasetBytes:           report.Dataset.DatasetBytes,
		Records: nn.RecordCounts{
			Total:        records.Total,
			Usable:       records.Usable,
			Invalid:      records.Invalid,
			Insufficient: records.Insufficient,
			Immature:     records.Immature,
		},
		TrainEnd:      report.Partitions.TrainEnd,
		ValidationEnd: report.Partitions.ValidationEnd,
		Threshold:     report.Selection.Threshold.Value,
		ReportSHA256:  reportSHA256,
	}
}

func validate(report Report) error {
	config := report.Configuration.Config
	dataset := report.Dataset

	switch {
	case dataset.DatasetReportVersion != trainingdata.DatasetReportVersion:
		return fmt.Errorf("unsupported dataset report version: %q", dataset.DatasetReportVersion)
	case dataset.FeatureSchema != config.FeatureSchema:
		return errors.New("the dataset must be classified with the configured feature schema")
	case dataset.Records.Invalid != 0:
		return errors.New("a dataset with invalid records cannot be trained or evaluated")
	case !report.Partitions.TrainEnd.Equal(config.TrainEnd) || !report.Partitions.ValidationEnd.Equal(config.ValidationEnd):
		return errors.New("the partitions must use the configured boundaries")
	}

	if err := validateSelection(report.Selection, config); err != nil {
		return err
	}

	threshold := report.Selection.Threshold.Value
	evaluated := config.TestEvaluation == trainingconfig.TestAfterSelection

	switch {
	case report.Validation.Threshold != threshold:
		return errors.New("validation metrics must use the selected threshold")
	case report.TestEvaluated != evaluated:
		return errors.New("test_evaluated must follow the configured test evaluation")
	case evaluated && (report.Test == nil || report.Test.Threshold != threshold):
		return errors.New("an evaluated test partition must have metrics at the selected threshold")
	case !evaluated && report.Test != nil:
		return errors.New("a skipped test partition must have no metrics")
	}

	return nil
}

func validateSelection(selection Selection, config trainingconfig.Config) error {
	switch {
	case selection.RequestedEpochs != config.MaxEpochs:
		return errors.New("requested epochs must be the configured maximum")
	case selection.ActualEpochs < 1 || selection.ActualEpochs > selection.RequestedEpochs:
		return errors.New("actual epochs must be from 1 to the requested epochs")
	case selection.SelectedEpoch < 1 || selection.SelectedEpoch > selection.ActualEpochs:
		return errors.New("the selected epoch must be from 1 to the actual epochs")
	case selection.StopReason == StopMaxEpochs && selection.ActualEpochs != selection.RequestedEpochs:
		return errors.New("a run stopped by max_epochs trains every requested epoch")
	case selection.StopReason == StopEarlyStopping && config.EarlyStopping.Policy != trainingconfig.EarlyStoppingPatience:
		return errors.New("only a configured early stopping can stop a run early")
	case selection.StopReason != StopMaxEpochs && selection.StopReason != StopEarlyStopping:
		return fmt.Errorf("unknown stop reason %q", selection.StopReason)
	case selection.CheckpointPolicy != config.Checkpoint.Policy:
		return errors.New("the checkpoint policy must be the configured one")
	}

	switch selection.CheckpointPolicy {
	case trainingconfig.CheckpointFinalEpoch:
		if selection.SelectedEpoch != selection.ActualEpochs || selection.Objective != nil ||
			selection.Evaluations != 0 || selection.SelectedObjectiveValue != nil {
			return errors.New("the final_epoch checkpoint keeps the last epoch and has no validation objective")
		}
	case trainingconfig.CheckpointBestValidation:
		value := selection.SelectedObjectiveValue
		if selection.Objective == nil || *selection.Objective != config.Checkpoint.Objective ||
			selection.Evaluations < 1 || value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
			return errors.New("the best_validation checkpoint needs its objective, evaluations, and a finite selected value")
		}
	}

	threshold := selection.Threshold
	switch {
	case threshold.Policy != config.Threshold.Policy:
		return errors.New("the threshold policy must be the configured one")
	case !(threshold.Value >= 0 && threshold.Value <= 1):
		return errors.New("the threshold must be a number from 0 to 1")
	}

	switch threshold.Policy {
	case trainingconfig.ThresholdFixed:
		if threshold.Value != config.Threshold.Value || threshold.MaxFalsePositiveRate != nil || threshold.Candidates != nil {
			return errors.New("a fixed threshold is the configured value")
		}
	case trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate:
		if threshold.MaxFalsePositiveRate == nil || *threshold.MaxFalsePositiveRate != config.Threshold.MaxFalsePositiveRate ||
			threshold.Candidates == nil || *threshold.Candidates < 1 {
			return errors.New("a selected threshold records the configured limit and its candidates")
		}
	}

	return nil
}
