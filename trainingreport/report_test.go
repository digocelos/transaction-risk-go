package trainingreport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

const examplesDir = "../docs/contracts/training-examples"

// dataset joins the three usable training examples: a legitimate payment on
// 2026-05-14, a fraud on 2026-06-02, and a fraud with unknown loss on
// 2026-06-18.
func dataset(t *testing.T) []byte {
	t.Helper()

	var data []byte
	for _, name := range []string{"01-usable-legitimate", "02-usable-fraud", "03-unknown-loss"} {
		line, err := os.ReadFile(filepath.Join(examplesDir, name+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
	}

	return data
}

// buildReport runs the dataset through the training data and evaluation
// packages, with fixed scores standing in for a model.
func buildReport(t *testing.T, boundaries evaluation.Boundaries) Report {
	t.Helper()

	partitioner, err := evaluation.NewPartitioner(boundaries)
	if err != nil {
		t.Fatal(err)
	}

	scores := map[int]float64{1: 0.2, 2: 0.9, 3: 0.4}
	predictions := map[evaluation.Partition][]evaluation.Prediction{}
	var exclusions []trainingdata.Exclusion

	summary, err := trainingdata.Scan(bytes.NewReader(dataset(t)), trainingdata.TransactionFeaturesV1,
		func(record trainingdata.Record) error {
			if exclusion, ok := record.Exclusion(); ok {
				exclusions = append(exclusions, exclusion)
			}

			partition, ok := partitioner.Add(record)
			if ok && record.Category == trainingdata.Usable {
				example := record.Example
				predictions[partition] = append(predictions[partition], evaluation.Prediction{
					Score:           scores[record.Line],
					Label:           example.Outcome.Label,
					AmountCents:     example.ScoringInput.PixPayment.AmountCents,
					LossAmountCents: example.Outcome.LossAmountCents,
				})
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	validation, err := evaluation.Evaluate(predictions[evaluation.Validation], 0.5)
	if err != nil {
		t.Fatal(err)
	}

	test, err := evaluation.Evaluate(predictions[evaluation.Test], 0.5)
	if err != nil {
		t.Fatal(err)
	}

	return Report{
		Configuration: Configuration{
			Algorithm:     nn.AlgorithmFullBatchGradientDescent,
			FeatureSchema: "transaction-features-v1",
			Seed:          42,
			Epochs:        5000,
			LearningRate:  1,
			Threshold:     0.5,
		},
		Dataset:    trainingdata.NewReport(summary, exclusions),
		Partitions: partitioner.Report(),
		Validation: validation,
		Test:       test,
	}
}

var testBoundaries = evaluation.Boundaries{
	TrainEnd:      time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
	ValidationEnd: time.Date(2026, 6, 10, 23, 59, 59, 0, time.UTC),
}

func TestMarshalIsDeterministic(t *testing.T) {
	first, err := Marshal(buildReport(t, testBoundaries))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	second, err := Marshal(buildReport(t, testBoundaries))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Error("two encodings of the same report differ")
	}

	// The same instants written with another offset produce the same bytes.
	brasilia := time.FixedZone("", -3*60*60)
	shifted, err := Marshal(buildReport(t, evaluation.Boundaries{
		TrainEnd:      testBoundaries.TrainEnd.In(brasilia),
		ValidationEnd: testBoundaries.ValidationEnd.In(brasilia),
	}))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, shifted) {
		t.Error("boundary offsets changed the report bytes")
	}

	if !bytes.HasSuffix(first, []byte("}\n")) {
		t.Error("report must end with a newline")
	}
}

func TestMarshalRecordsEveryPart(t *testing.T) {
	data, err := Marshal(buildReport(t, testBoundaries))
	if err != nil {
		t.Fatal(err)
	}

	var report struct {
		Version       string `json:"training_report_version"`
		Configuration struct {
			Algorithm string  `json:"algorithm"`
			Seed      int64   `json:"seed"`
			Threshold float64 `json:"threshold"`
		} `json:"configuration"`
		Dataset struct {
			SHA256  string `json:"dataset_sha256"`
			Records struct {
				Usable int `json:"usable"`
			} `json:"records"`
		} `json:"dataset"`
		Partitions struct {
			TrainEnd string                     `json:"train_end"`
			Train    evaluation.PartitionCounts `json:"train"`
			Test     evaluation.PartitionCounts `json:"test"`
		} `json:"partitions"`
		Validation evaluation.Metrics `json:"validation"`
		Test       evaluation.Metrics `json:"test"`
	}

	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(dataset(t))

	switch {
	case report.Version != "pix-training-report-v1":
		t.Errorf("version = %q", report.Version)
	case report.Configuration.Algorithm != "full-batch-gradient-descent" || report.Configuration.Seed != 42:
		t.Errorf("configuration = %+v", report.Configuration)
	case report.Dataset.SHA256 != hex.EncodeToString(sum[:]) || report.Dataset.Records.Usable != 3:
		t.Errorf("dataset = %+v", report.Dataset)
	case report.Partitions.TrainEnd != "2026-05-31T23:59:59Z":
		t.Errorf("train_end = %q", report.Partitions.TrainEnd)
	case report.Partitions.Train.Legitimate != 1 || report.Partitions.Test.Fraud != 1:
		t.Errorf("partitions = %+v", report.Partitions)
	case report.Validation.TruePositives != 1 || report.Test.FalseNegatives != 1:
		t.Errorf("metrics: validation %+v, test %+v", report.Validation, report.Test)
	case report.Test.UnknownLossFraudsMissed != 1:
		t.Errorf("test unknown loss missed = %d, want 1", report.Test.UnknownLossFraudsMissed)
	}
}

func TestMarshalRejectsInconsistentReports(t *testing.T) {
	tests := map[string]func(*Report){
		"empty algorithm":         func(r *Report) { r.Configuration.Algorithm = "" },
		"zero epochs":             func(r *Report) { r.Configuration.Epochs = 0 },
		"invalid learning rate":   func(r *Report) { r.Configuration.LearningRate = -1 },
		"threshold above 1":       func(r *Report) { r.Configuration.Threshold = 1.5 },
		"threshold mismatch":      func(r *Report) { r.Test.Threshold = 0.4 },
		"feature schema mismatch": func(r *Report) { r.Configuration.FeatureSchema = "transaction-features-v2" },
		"invalid records":         func(r *Report) { r.Dataset.Records.Invalid = 1; r.Dataset.Records.Total++ },
		"wrong dataset report":    func(r *Report) { r.Dataset.DatasetReportVersion = "v0" },
		"reversed boundaries": func(r *Report) {
			r.Partitions.TrainEnd, r.Partitions.ValidationEnd = r.Partitions.ValidationEnd, r.Partitions.TrainEnd
		},
	}

	for name, change := range tests {
		report := buildReport(t, testBoundaries)
		change(&report)

		if _, err := Marshal(report); err == nil {
			t.Errorf("%s: Marshal() error = nil, want an error", name)
		}
	}
}

// A trained model states exactly which dataset bytes and configuration
// produced it, and binds its report by hash.
func TestModelProvenanceBindsTheReport(t *testing.T) {
	report := buildReport(t, testBoundaries)

	reportBytes, err := Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	reportSum := sha256.Sum256(reportBytes)

	network, err := nn.Load("../model.json", "transaction-features-v1")
	if err != nil {
		t.Fatal(err)
	}

	provenance := Provenance(report, hex.EncodeToString(reportSum[:]))

	path := filepath.Join(t.TempDir(), "trained.json")
	if err := network.SaveWithProvenance(path, "transaction-features-v1", provenance); err != nil {
		t.Fatalf("SaveWithProvenance() error = %v", err)
	}

	_, loaded, err := nn.LoadWithProvenance(path, "transaction-features-v1")
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(dataset(t))

	switch {
	case loaded.ReportSHA256 != hex.EncodeToString(reportSum[:]):
		t.Errorf("report_sha256 = %s, want the SHA-256 of the report bytes", loaded.ReportSHA256)
	case loaded.DatasetSHA256 != hex.EncodeToString(sum[:]) || loaded.DatasetBytes != int64(len(dataset(t))):
		t.Errorf("dataset fingerprint = %s (%d bytes)", loaded.DatasetSHA256, loaded.DatasetBytes)
	case loaded.Seed != 42 || loaded.Epochs != 5000 || loaded.Threshold != 0.5:
		t.Errorf("configuration = %+v", loaded)
	case loaded.Records.Usable != 3 || loaded.TrainingExampleVersion != "pix-training-example-v1":
		t.Errorf("records = %+v", loaded.Records)
	case !loaded.TrainEnd.Equal(testBoundaries.TrainEnd) || !loaded.ValidationEnd.Equal(testBoundaries.ValidationEnd):
		t.Errorf("boundaries = %s, %s", loaded.TrainEnd, loaded.ValidationEnd)
	}

	if strings.Contains(string(reportBytes), "NaN") {
		t.Error("report contains NaN")
	}
}
