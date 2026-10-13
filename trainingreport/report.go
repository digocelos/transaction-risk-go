// Package trainingreport writes the training report of a trained model: the
// configuration, the dataset report, the temporal partitions, and the
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
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// Version is the version of the training report layout.
const Version = "pix-training-report-v1"

// Configuration is the training configuration, fixed before the test
// partition is evaluated.
type Configuration struct {
	Algorithm     string  `json:"algorithm"`
	FeatureSchema string  `json:"feature_schema"`
	Seed          int64   `json:"seed"`
	Epochs        int     `json:"epochs"`
	LearningRate  float64 `json:"learning_rate"`
	Threshold     float64 `json:"threshold"`
}

// Report is the training report. Validation and Test hold the metrics of
// those partitions only; the train partition is never evaluated.
type Report struct {
	TrainingReportVersion string                     `json:"training_report_version"`
	Configuration         Configuration              `json:"configuration"`
	Dataset               trainingdata.Report        `json:"dataset"`
	Partitions            evaluation.PartitionReport `json:"partitions"`
	Validation            evaluation.Metrics         `json:"validation"`
	Test                  evaluation.Metrics         `json:"test"`
}

// Marshal validates a report and encodes it deterministically: the same
// report always produces the same bytes, timestamps are written in UTC, and
// the output ends with a newline. The model's report_sha256 is the SHA-256
// of exactly these bytes.
func Marshal(report Report) ([]byte, error) {
	if err := validate(report); err != nil {
		return nil, fmt.Errorf("invalid training report: %w", err)
	}

	report.TrainingReportVersion = Version
	report.Partitions.TrainEnd = report.Partitions.TrainEnd.UTC()
	report.Partitions.ValidationEnd = report.Partitions.ValidationEnd.UTC()

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode training report: %w", err)
	}

	return append(data, '\n'), nil
}

// Provenance returns the model provenance that a report describes, binding
// the report by reportSHA256.
func Provenance(report Report, reportSHA256 string) nn.Provenance {
	records := report.Dataset.Records

	return nn.Provenance{
		Algorithm:              report.Configuration.Algorithm,
		Seed:                   report.Configuration.Seed,
		Epochs:                 report.Configuration.Epochs,
		LearningRate:           report.Configuration.LearningRate,
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
		Threshold:     report.Configuration.Threshold,
		ReportSHA256:  reportSHA256,
	}
}

func validate(report Report) error {
	config := report.Configuration
	dataset := report.Dataset

	switch {
	case config.Algorithm == "":
		return errors.New("algorithm is required")
	case config.Epochs < 1:
		return errors.New("epochs must be at least 1")
	case !(config.LearningRate > 0) || math.IsInf(config.LearningRate, 0):
		return errors.New("learning rate must be finite and positive")
	case !(config.Threshold >= 0 && config.Threshold <= 1):
		return errors.New("threshold must be a number from 0 to 1")
	case report.Validation.Threshold != config.Threshold || report.Test.Threshold != config.Threshold:
		return errors.New("validation and test metrics must use the configured threshold")
	case dataset.DatasetReportVersion != trainingdata.DatasetReportVersion:
		return fmt.Errorf("unsupported dataset report version: %q", dataset.DatasetReportVersion)
	case config.FeatureSchema == "" || dataset.FeatureSchema != config.FeatureSchema:
		return errors.New("the dataset must be classified with the configured feature schema")
	case dataset.Records.Invalid != 0:
		return errors.New("a dataset with invalid records cannot be trained or evaluated")
	case !report.Partitions.TrainEnd.Before(report.Partitions.ValidationEnd):
		return errors.New("train_end must be earlier than validation_end")
	}

	return nil
}
