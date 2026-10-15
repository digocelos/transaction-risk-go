package modelevaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

const (
	// fixture has five train records (lines 1–5), one validation fraud
	// (line 6), and two test records: a fraud with an unknown loss (line
	// 7) and an immature legitimate record (line 8), with the baseline
	// boundaries.
	fixture = "../training/testdata/pix-risk-v1.jsonl"

	// selectionFixture has six train records (lines 1–6), two legitimate
	// and two fraud validation records (lines 7–10), and two legitimate
	// test records and a test fraud with an unknown loss (lines 11–13),
	// with the boundaries of selectionConfig.
	selectionFixture = "../training/testdata/pix-risk-v1-selection.jsonl"

	baseline = "../docs/contracts/training-config-examples/01-baseline.json"
)

// maxRecords is the record limit of the evaluations under test.
const maxRecords = trainingdata.DefaultMaxRecords

var software = modelartifact.Software{Module: "github.com/digocelos/transaction-risk-go", Version: "(devel)", Revision: "0123abc"}

func read(t testing.TB, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func baselineConfig(t testing.TB) trainingconfig.Config {
	t.Helper()

	cfg, err := trainingconfig.Decode(read(t, baseline))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs = 300
	return cfg
}

// selectionConfig restores the best validation loss and selects the
// threshold on validation.
func selectionConfig(t testing.TB) trainingconfig.Config {
	t.Helper()

	cfg := baselineConfig(t)
	cfg.TrainEnd = time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	cfg.ValidationEnd = time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	cfg.Checkpoint = trainingconfig.Checkpoint{
		Policy:             trainingconfig.CheckpointBestValidation,
		Objective:          trainingconfig.ObjectiveValidationLoss,
		EvaluationInterval: 10,
	}
	cfg.Threshold = trainingconfig.Threshold{
		Policy:               trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate,
		MaxFalsePositiveRate: 0.5,
	}
	return cfg
}

// trainModel trains a model on a dataset and returns the bytes of its
// artifact and the training result.
func trainModel(t testing.TB, dataset []byte, cfg trainingconfig.Config) ([]byte, training.Result) {
	t.Helper()

	result, err := training.Run(context.Background(), bytes.NewReader(dataset), cfg, training.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}

	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := modelartifact.FromTraining(result.Network, result.State, result.Report, digest(report), software)
	if err != nil {
		t.Fatal(err)
	}

	model, err := modelartifact.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}

	return model, result
}

func evaluate(t *testing.T, model, dataset []byte, options Options) Report {
	t.Helper()

	report, err := Evaluate(context.Background(), model, bytes.NewReader(dataset), options)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	// Every report Evaluate returns is consistent.
	if _, err := Marshal(report); err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return report
}

// A model loaded from its artifact reproduces the metrics of the final
// evaluation of its training run, partition by partition.
func TestEvaluateReproducesTheFinalEvaluation(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		config  func(testing.TB) trainingconfig.Config
	}{
		{"fixed threshold", fixture, baselineConfig},
		{"selected checkpoint and threshold", selectionFixture, selectionConfig},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dataset := read(t, c.fixture)
			model, result := trainModel(t, dataset, c.config(t))

			partitions := []struct {
				partition evaluation.Partition
				want      evaluation.Metrics
			}{
				{evaluation.Validation, result.Report.Validation},
				{evaluation.Test, *result.Report.Test},
			}

			for _, p := range partitions {
				report := evaluate(t, model, dataset, Options{MaxRecords: maxRecords, Scope: PartitionScope(p.partition)})

				if !reflect.DeepEqual(report.Metrics, p.want) {
					t.Errorf("%s metrics = %+v, want the training report's %+v", p.partition, report.Metrics, p.want)
				}
			}
		})
	}
}

func utc(month time.Month, day int) time.Time {
	return time.Date(2026, month, day, 0, 0, 0, 0, time.UTC)
}

func at(month time.Month, day int) *time.Time {
	value := utc(month, day)
	return &value
}

func partition(p evaluation.Partition) *evaluation.Partition {
	return &p
}

// Partition scopes use the artifact's boundaries, and a window scope
// measures the usable records after its start and through its end. The
// report states the scope, the records in and out of it, and how many of
// them fall in the model's validation range.
func TestEvaluateMeasuresTheRecordsInScope(t *testing.T) {
	dataset := read(t, selectionFixture)
	model, _ := trainModel(t, dataset, selectionConfig(t))

	cases := []struct {
		name        string
		scope       Scope
		wantScope   ScopeReport
		wantRecords Records
	}{
		{
			name:        "validation partition",
			scope:       PartitionScope(evaluation.Validation),
			wantScope:   ScopeReport{Partition: partition(evaluation.Validation), After: utc(5, 31), Through: at(6, 15)},
			wantRecords: Records{InScope: evaluation.PartitionCounts{Fraud: 2, Legitimate: 2}, OutOfScope: 9, InValidationRange: 4},
		},
		{
			name:        "test partition",
			scope:       PartitionScope(evaluation.Test),
			wantScope:   ScopeReport{Partition: partition(evaluation.Test), After: utc(6, 15)},
			wantRecords: Records{InScope: evaluation.PartitionCounts{Fraud: 1, Legitimate: 2}, OutOfScope: 10},
		},
		{
			name:        "bounded window",
			scope:       WindowScope(utc(6, 6), at(6, 21)),
			wantScope:   ScopeReport{After: utc(6, 6), Through: at(6, 21)},
			wantRecords: Records{InScope: evaluation.PartitionCounts{Fraud: 1, Legitimate: 2}, OutOfScope: 10, InValidationRange: 2},
		},
		{
			name:        "unbounded window",
			scope:       WindowScope(utc(6, 6), nil),
			wantScope:   ScopeReport{After: utc(6, 6)},
			wantRecords: Records{InScope: evaluation.PartitionCounts{Fraud: 2, Legitimate: 3}, OutOfScope: 8, InValidationRange: 2},
		},
		{
			name:        "window from train_end",
			scope:       WindowScope(utc(5, 31), at(6, 15)),
			wantScope:   ScopeReport{After: utc(5, 31), Through: at(6, 15)},
			wantRecords: Records{InScope: evaluation.PartitionCounts{Fraud: 2, Legitimate: 2}, OutOfScope: 9, InValidationRange: 4},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			report := evaluate(t, model, dataset, Options{MaxRecords: maxRecords, Scope: c.scope})

			if !reflect.DeepEqual(report.Scope, c.wantScope) {
				t.Errorf("Scope = %+v, want %+v", report.Scope, c.wantScope)
			}
			if report.Records != c.wantRecords {
				t.Errorf("Records = %+v, want %+v", report.Records, c.wantRecords)
			}

			m := report.Metrics
			if got, want := m.TruePositives+m.FalseNegatives, c.wantRecords.InScope.Fraud; got != want {
				t.Errorf("metrics count %d frauds, want %d", got, want)
			}
			if got, want := m.FalsePositives+m.TrueNegatives, c.wantRecords.InScope.Legitimate; got != want {
				t.Errorf("metrics count %d legitimate records, want %d", got, want)
			}
		})
	}
}

// A window excludes a record decided exactly at its start and includes one
// decided exactly at its end.
func TestEvaluateWindowBoundsAreExclusiveThenInclusive(t *testing.T) {
	dataset := read(t, selectionFixture)
	model, _ := trainModel(t, dataset, selectionConfig(t))
	saoPaulo := time.FixedZone("-03:00", -3*60*60)

	line8 := time.Date(2026, 6, 5, 14, 3, 7, 120_000_000, saoPaulo)   // fraud
	line10 := time.Date(2026, 6, 10, 14, 3, 7, 120_000_000, saoPaulo) // fraud

	report := evaluate(t, model, dataset, Options{MaxRecords: maxRecords, Scope: WindowScope(line8, &line10)})

	// Line 9, a legitimate record, and line 10 are in scope; line 8 is not.
	if want := (evaluation.PartitionCounts{Fraud: 1, Legitimate: 1}); report.Records.InScope != want {
		t.Errorf("Records.InScope = %+v, want %+v", report.Records.InScope, want)
	}
}

// The train partition is never evaluated, and a scope must be one
// partition or one window that starts at or after train_end.
func TestEvaluateRejectsInvalidScopes(t *testing.T) {
	dataset := read(t, selectionFixture)
	model, _ := trainModel(t, dataset, selectionConfig(t))

	cases := []struct {
		name  string
		scope Scope
		want  string
	}{
		{"train partition", PartitionScope(evaluation.Train), "the train partition is never evaluated"},
		{"unknown partition", PartitionScope("holdout"), "unknown partition"},
		{"no scope", Scope{}, "a scope needs a partition or a window start"},
		{"partition and window", Scope{Partition: evaluation.Test, After: utc(6, 20)}, "either a partition or a window"},
		{"window through without start", Scope{Through: at(6, 20)}, "a scope needs a partition or a window start"},
		{"window before train_end", WindowScope(utc(5, 30), nil), "must not be earlier than the model's train_end"},
		{"empty window", WindowScope(utc(6, 20), at(6, 20)), "must be later than its start"},
		{"reversed window", WindowScope(utc(6, 20), at(6, 10)), "must be later than its start"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Evaluate(context.Background(), model, bytes.NewReader(dataset), Options{MaxRecords: maxRecords, Scope: c.scope})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Evaluate() error = %v, want %q", err, c.want)
			}
		})
	}
}

// replaceLine replaces old with new in one line of a dataset.
func replaceLine(t *testing.T, dataset []byte, line int, old, new string) []byte {
	t.Helper()

	lines := bytes.Split(dataset, []byte("\n"))
	if !bytes.Contains(lines[line-1], []byte(old)) {
		t.Fatalf("line %d does not contain %q", line, old)
	}
	lines[line-1] = bytes.Replace(lines[line-1], []byte(old), []byte(new), 1)
	return bytes.Join(lines, []byte("\n"))
}

// excludedTestRecords makes the legitimate test records of the selection
// fixture excluded: line 11 insufficient, with a stale recent count, and
// line 13 immature.
func excludedTestRecords(t *testing.T, dataset []byte) []byte {
	t.Helper()

	dataset = replaceLine(t, dataset, 11,
		`"initiated_outgoing_pix_24h":{"count":2,"as_of":"2026-06-20T17:03:06.874Z"}`,
		`"initiated_outgoing_pix_24h":{"count":2,"as_of":"2026-06-20T16:00:00Z"}`)
	return replaceLine(t, dataset, 13, `"label_as_of":"2026-10-01T03:00:00Z"`, `"label_as_of":"2026-07-01T00:00:00Z"`)
}

func reasons(completeness Completeness) []string {
	var reasons []string
	for _, incomplete := range completeness.Incomplete {
		reasons = append(reasons, incomplete.Reason)
	}
	return reasons
}

// Insufficient and immature records in scope are counted and excluded,
// and an evaluation without usable records, without a class, or with
// unknown losses is incomplete for the metrics it cannot support.
func TestEvaluateMarksIncompleteEvaluations(t *testing.T) {
	dataset := read(t, selectionFixture)
	model, _ := trainModel(t, dataset, selectionConfig(t))

	cases := []struct {
		name        string
		dataset     []byte
		scope       Scope
		wantInScope evaluation.PartitionCounts
		threshold   *float64
		wantReasons []string
	}{
		{"both classes and known losses", dataset, PartitionScope(evaluation.Validation),
			evaluation.PartitionCounts{Fraud: 2, Legitimate: 2}, nil, nil},
		{"unknown loss", dataset, PartitionScope(evaluation.Test),
			evaluation.PartitionCounts{Fraud: 1, Legitimate: 2}, nil, []string{ReasonUnknownLossFrauds}},
		{"unknown loss missed", dataset, PartitionScope(evaluation.Test),
			evaluation.PartitionCounts{Fraud: 1, Legitimate: 2}, float(1), []string{ReasonUnknownLossFrauds}},
		{"unknown loss detected", dataset, PartitionScope(evaluation.Test),
			evaluation.PartitionCounts{Fraud: 1, Legitimate: 2}, float(0), []string{ReasonUnknownLossFrauds}},
		{"no fraud", dataset, WindowScope(utc(6, 26), nil),
			evaluation.PartitionCounts{Legitimate: 1}, nil, []string{ReasonNoFraudExamples}},
		{"no legitimate", dataset, WindowScope(utc(6, 9), at(6, 15)),
			evaluation.PartitionCounts{Fraud: 1}, nil, []string{ReasonNoLegitimateExamples}},
		{"no usable record", dataset, WindowScope(utc(7, 1), nil),
			evaluation.PartitionCounts{}, nil, []string{ReasonNoUsableExamples}},
		{"excluded records", excludedTestRecords(t, dataset), PartitionScope(evaluation.Test),
			evaluation.PartitionCounts{Fraud: 1, Insufficient: 1, Immature: 1}, nil,
			[]string{ReasonNoLegitimateExamples, ReasonUnknownLossFrauds}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			report := evaluate(t, model, c.dataset, Options{MaxRecords: maxRecords, Scope: c.scope, Threshold: c.threshold})

			if report.Records.InScope != c.wantInScope {
				t.Errorf("Records.InScope = %+v, want %+v", report.Records.InScope, c.wantInScope)
			}
			if got := reasons(report.Completeness); !reflect.DeepEqual(got, c.wantReasons) {
				t.Errorf("incomplete reasons = %v, want %v", got, c.wantReasons)
			}
			if got, want := report.Completeness.Complete, c.wantReasons == nil; got != want {
				t.Errorf("Complete = %v, want %v", got, want)
			}
		})
	}
}

// Each reason names the metrics it leaves undefined, trivial, or partial.
func TestIncompleteReasonsNameTheirMetrics(t *testing.T) {
	all := []string{
		"true_positives", "false_positives", "true_negatives", "false_negatives",
		"precision", "recall", "f1", "false_positive_rate", "false_positives_per_1000_legitimate",
		"pr_auc", "pr_curve",
		"fraud_amount_cents_detected", "fraud_amount_cents_missed", "legitimate_amount_cents_flagged",
		"known_loss_cents_detected", "known_loss_cents_missed",
		"unknown_loss_frauds_detected", "unknown_loss_frauds_missed",
	}

	want := map[string][]string{
		ReasonNoUsableExamples: all,
		ReasonNoFraudExamples: {
			"precision", "recall", "f1", "pr_auc", "pr_curve",
			"fraud_amount_cents_detected", "fraud_amount_cents_missed",
			"known_loss_cents_detected", "known_loss_cents_missed",
			"unknown_loss_frauds_detected", "unknown_loss_frauds_missed",
		},
		ReasonNoLegitimateExamples: {
			"precision", "f1", "false_positive_rate", "false_positives_per_1000_legitimate",
			"pr_auc", "pr_curve", "legitimate_amount_cents_flagged",
		},
		ReasonUnknownLossFrauds: {"known_loss_cents_detected", "known_loss_cents_missed"},
	}

	dataset := read(t, selectionFixture)
	model, _ := trainModel(t, dataset, selectionConfig(t))

	scopes := []Scope{
		WindowScope(utc(7, 1), nil),
		WindowScope(utc(6, 26), nil),
		WindowScope(utc(6, 9), at(6, 15)),
		PartitionScope(evaluation.Test),
	}

	seen := map[string]bool{}
	for _, scope := range scopes {
		report := evaluate(t, model, dataset, Options{MaxRecords: maxRecords, Scope: scope})
		for _, incomplete := range report.Completeness.Incomplete {
			seen[incomplete.Reason] = true
			if !reflect.DeepEqual(incomplete.Metrics, want[incomplete.Reason]) {
				t.Errorf("%s metrics = %v, want %v", incomplete.Reason, incomplete.Metrics, want[incomplete.Reason])
			}
		}
	}

	if len(seen) != len(want) {
		t.Errorf("reasons seen = %v, want all of %v", seen, want)
	}
}

func float(value float64) *float64 {
	return &value
}

// The artifact's threshold is used unless the evaluation overrides it. An
// override is labeled in the report, measures every metric at its value,
// and leaves the model's threshold unchanged.
func TestEvaluateLabelsAThresholdOverride(t *testing.T) {
	dataset := read(t, selectionFixture)
	model, result := trainModel(t, dataset, selectionConfig(t))
	selected := result.Report.Selection.Threshold
	stored := modelartifact.Threshold{Policy: selected.Policy, Value: selected.Value}

	report := evaluate(t, model, dataset, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Validation)})
	if want := (Threshold{Source: ThresholdFromArtifact, Value: selected.Value}); report.Threshold != want {
		t.Errorf("Threshold = %+v, want %+v", report.Threshold, want)
	}

	// Every score is at least 0, so a threshold of 0 flags every record,
	// and sigmoid scores stay below 1, so a threshold of 1 flags none.
	cases := []struct {
		threshold                                float64
		truePositives, falsePositives, negatives int
	}{
		{0, 2, 2, 0},
		{1, 0, 0, 4},
	}

	for _, c := range cases {
		report := evaluate(t, model, dataset, Options{MaxRecords: maxRecords,
			Scope:     PartitionScope(evaluation.Validation),
			Threshold: float(c.threshold),
		})

		if want := (Threshold{Source: ThresholdOverride, Value: c.threshold}); report.Threshold != want {
			t.Errorf("Threshold = %+v, want %+v", report.Threshold, want)
		}
		if report.Model.Threshold != stored {
			t.Errorf("Model.Threshold = %+v, want the artifact's %+v", report.Model.Threshold, stored)
		}

		m := report.Metrics
		if m.Threshold != c.threshold || m.TruePositives != c.truePositives ||
			m.FalsePositives != c.falsePositives || m.TrueNegatives+m.FalseNegatives != c.negatives {
			t.Errorf("metrics at %v = %+v", c.threshold, m)
		}
	}

	for _, invalid := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
		_, err := Evaluate(context.Background(), model, bytes.NewReader(dataset), Options{MaxRecords: maxRecords,
			Scope:     PartitionScope(evaluation.Validation),
			Threshold: float(invalid),
		})
		if err == nil || !strings.Contains(err.Error(), "threshold override must be a number from 0 to 1") {
			t.Errorf("override %v: Evaluate() error = %v", invalid, err)
		}
	}
}

// The report identifies the evaluating software, the model and everything
// that produced it, and the evaluated dataset, and states whether that
// dataset is the one the model was trained on.
func TestEvaluateRecordsIdentities(t *testing.T) {
	dataset := read(t, selectionFixture)
	cfg := selectionConfig(t)
	model, result := trainModel(t, dataset, cfg)

	artifact, err := modelartifact.Decode(model)
	if err != nil {
		t.Fatal(err)
	}
	configSHA256, err := cfg.SHA256()
	if err != nil {
		t.Fatal(err)
	}

	report := evaluate(t, model, dataset, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test)})

	want := Model{
		ArtifactVersion:       "pix-risk-model-v1",
		ArtifactSHA256:        digest(model),
		Software:              software,
		Contracts:             Contracts{ScoringInput: "pix-payer-input-v2", TrainingExample: "pix-training-example-v2"},
		FeatureSchema:         "pix-risk-v1",
		ConfigVersion:         "pix-training-config-v2",
		ConfigSHA256:          configSHA256,
		TrainingReportVersion: "pix-training-report-v2",
		TrainingReportSHA256:  artifact.Training.ReportSHA256,
		TrainingDatasetSHA256: digest(dataset),
		TrainEnd:              utc(5, 31),
		ValidationEnd:         utc(6, 15),
		SelectedEpoch:         result.Report.Selection.SelectedEpoch,
		Threshold:             artifact.Threshold,
	}
	if !reflect.DeepEqual(report.Model, want) {
		t.Errorf("Model = %+v, want %+v", report.Model, want)
	}

	if report.Software != modelartifact.SoftwareFromBuild() {
		t.Errorf("Software = %+v, want the running build's %+v", report.Software, modelartifact.SoftwareFromBuild())
	}

	if report.Dataset.DatasetSHA256 != digest(dataset) || report.Dataset.Records.Total != 13 {
		t.Errorf("Dataset = %+v, want the evaluated dataset", report.Dataset)
	}
	if !report.SameDatasetAsTraining {
		t.Error("SameDatasetAsTraining = false for the training dataset")
	}

	other := excludedTestRecords(t, dataset)
	report = evaluate(t, model, other, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test)})
	if report.SameDatasetAsTraining || report.Dataset.DatasetSHA256 != digest(other) {
		t.Errorf("another dataset: SameDatasetAsTraining = %v, DatasetSHA256 = %s",
			report.SameDatasetAsTraining, report.Dataset.DatasetSHA256)
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// The coverage of the records in a partition scope is the coverage the
// training report counted for that partition.
func TestEvaluateCountsTheCoverageOfTheScope(t *testing.T) {
	dataset := read(t, selectionFixture)
	model, result := trainModel(t, dataset, selectionConfig(t))

	cases := []struct {
		partition evaluation.Partition
		want      trainingreport.PartitionCoverage
	}{
		{evaluation.Validation, result.Report.Coverage.Validation},
		{evaluation.Test, result.Report.Coverage.Test},
	}

	for _, c := range cases {
		report := evaluate(t, model, dataset, Options{MaxRecords: maxRecords, Scope: PartitionScope(c.partition)})
		if !reflect.DeepEqual(report.Coverage, c.want) {
			t.Errorf("%s coverage = %+v, want %+v", c.partition, report.Coverage, c.want)
		}
	}
}

func count(value int) *int {
	return &value
}

// overlapping gives the validation fraud (line 6) of the fixture the
// group key of two train records, and the test fraud (line 7), which has
// no key, the key of two others.
func overlapping(t *testing.T, dataset []byte) []byte {
	t.Helper()

	dataset = replaceLine(t, dataset, 6, `"group_key":"g-9e2f10"`, `"group_key":"g-7c1e9a"`)
	return replaceLine(t, dataset, 7, `"group_key":null`, `"group_key":"g-5be27d"`)
}

// Group overlap compares the group keys of the usable records in scope
// with those of the usable train records of the model's training dataset:
// the evaluated dataset when it is that dataset, or a reference dataset
// verified by its SHA-256. Without either, overlap is unavailable.
func TestEvaluateMeasuresGroupOverlap(t *testing.T) {
	original := read(t, fixture)
	changed := overlapping(t, original)

	model, _ := trainModel(t, original, baselineConfig(t))
	changedModel, changedResult := trainModel(t, changed, baselineConfig(t))

	cases := []struct {
		name      string
		model     []byte
		dataset   []byte
		reference []byte
		partition evaluation.Partition
		want      GroupOverlap
	}{
		{"same dataset without overlap", model, original, nil, evaluation.Validation,
			GroupOverlap{Reference: ReferenceEvaluatedDataset, SharedKeys: count(0), RecordsSharingTrainKeys: count(0)}},
		{"same dataset with a record without a key", model, original, nil, evaluation.Test,
			GroupOverlap{Reference: ReferenceEvaluatedDataset, SharedKeys: count(0), RecordsSharingTrainKeys: count(0),
				RecordsWithoutGroupKey: 1}},
		{"same dataset with overlap", changedModel, changed, nil, evaluation.Validation,
			GroupOverlap{Reference: ReferenceEvaluatedDataset, SharedKeys: count(1), RecordsSharingTrainKeys: count(1)}},
		{"another dataset without a reference", model, changed, nil, evaluation.Validation,
			GroupOverlap{Reference: ReferenceUnavailable}},
		{"another dataset with the reference", model, changed, original, evaluation.Validation,
			GroupOverlap{Reference: ReferenceDataset, SharedKeys: count(1), RecordsSharingTrainKeys: count(1)}},
		{"another dataset with the reference, test", model, changed, original, evaluation.Test,
			GroupOverlap{Reference: ReferenceDataset, SharedKeys: count(1), RecordsSharingTrainKeys: count(1)}},
	}

	// Two records in scope with one train key are one shared key and two
	// records.
	sameKey := replaceLine(t, changed, 7, `"group_key":"g-5be27d"`, `"group_key":"g-7c1e9a"`)
	report := evaluate(t, model, sameKey, Options{MaxRecords: maxRecords, Scope: WindowScope(utc(6, 5), nil), Reference: bytes.NewReader(original)})
	want := GroupOverlap{Reference: ReferenceDataset, SharedKeys: count(1), RecordsSharingTrainKeys: count(2)}
	if !reflect.DeepEqual(report.GroupOverlap, want) {
		t.Errorf("one key in two records: GroupOverlap = %+v, want %+v", report.GroupOverlap, want)
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			options := Options{MaxRecords: maxRecords, Scope: PartitionScope(c.partition)}
			if c.reference != nil {
				options.Reference = bytes.NewReader(c.reference)
			}

			report := evaluate(t, c.model, c.dataset, options)
			if !reflect.DeepEqual(report.GroupOverlap, c.want) {
				t.Errorf("GroupOverlap = %+v, want %+v", report.GroupOverlap, c.want)
			}
		})
	}

	// Validation and test together share train keys as often as the
	// training report counted.
	total := 0
	for _, partition := range []evaluation.Partition{evaluation.Validation, evaluation.Test} {
		report := evaluate(t, changedModel, changed, Options{MaxRecords: maxRecords, Scope: PartitionScope(partition)})
		total += *report.GroupOverlap.RecordsSharingTrainKeys
	}
	if want := changedResult.Report.Partitions.GroupOverlap.RecordsSharingTrainKeys; total != want {
		t.Errorf("records sharing train keys = %d, want the training report's %d", total, want)
	}
}

// Only usable train records count as train records: a key shared with an
// immature train record is not an overlap, as in the training report.
func TestEvaluateIgnoresKeysOfExcludedTrainRecords(t *testing.T) {
	dataset := read(t, fixture)
	dataset = replaceLine(t, dataset, 1, `"group_key":"g-7c1e9a"`, `"group_key":"g-1d44f0"`)
	dataset = replaceLine(t, dataset, 2, `"label_as_of":"2026-10-01T03:00:00Z"`, `"label_as_of":"2026-06-01T00:00:00Z"`)
	dataset = replaceLine(t, dataset, 6, `"group_key":"g-9e2f10"`, `"group_key":"g-7c1e9a"`)

	model, result := trainModel(t, dataset, baselineConfig(t))
	if got := result.Report.Partitions.GroupOverlap.RecordsSharingTrainKeys; got != 0 {
		t.Fatalf("the training report counts %d records sharing train keys, want 0", got)
	}

	report := evaluate(t, model, dataset, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Validation)})
	want := GroupOverlap{Reference: ReferenceEvaluatedDataset, SharedKeys: count(0), RecordsSharingTrainKeys: count(0)}
	if !reflect.DeepEqual(report.GroupOverlap, want) {
		t.Errorf("GroupOverlap = %+v, want %+v", report.GroupOverlap, want)
	}
}

// A reference dataset that is not the model's training dataset is refused.
func TestEvaluateRejectsAnotherReferenceDataset(t *testing.T) {
	original := read(t, fixture)
	model, _ := trainModel(t, original, baselineConfig(t))

	_, err := Evaluate(context.Background(), model, bytes.NewReader(original), Options{MaxRecords: maxRecords,
		Scope:     PartitionScope(evaluation.Test),
		Reference: bytes.NewReader(overlapping(t, original)),
	})
	if err == nil || !strings.Contains(err.Error(), "is not the model's training dataset") {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

// A dataset with an invalid record is never evaluated: Evaluate returns
// the dataset report and every diagnostic, and no report.
func TestEvaluateStopsOnInvalidDatasets(t *testing.T) {
	model, _ := trainModel(t, read(t, fixture), baselineConfig(t))

	// The fixture with every record relabeled as the retired version 1.
	retired := bytes.ReplaceAll(read(t, fixture), []byte(`"pix-training-example-v2"`), []byte(`"pix-training-example-v1"`))

	cases := []struct {
		name      string
		dataset   []byte
		wantLines []int
		wantCode  string
	}{
		{"invalid records", read(t, "../docs/contracts/training-examples-v2/dataset.jsonl"), []int{7, 8}, "invalid_enum_value"},
		{"version 1 records", retired, []int{1, 8}, "unsupported_training_example_version"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dataset := c.dataset

			report, err := Evaluate(context.Background(), model, bytes.NewReader(dataset), Options{MaxRecords: maxRecords,
				Scope: PartitionScope(evaluation.Test),
			})

			var invalid *training.InvalidDatasetError
			if !errors.As(err, &invalid) {
				t.Fatalf("Evaluate() error = %v, want *training.InvalidDatasetError", err)
			}
			if !reflect.DeepEqual(report, Report{}) {
				t.Errorf("Evaluate() report = %+v, want none", report)
			}
			if invalid.Report.DatasetSHA256 != digest(dataset) || invalid.Report.Records.Invalid == 0 {
				t.Errorf("dataset report = %+v", invalid.Report)
			}

			lines := map[int]bool{}
			codes := map[string]bool{}
			for _, d := range invalid.Diagnostics {
				lines[d.Line] = true
				codes[d.Code] = true
			}
			for _, line := range c.wantLines {
				if !lines[line] {
					t.Errorf("no diagnostic for line %d in %+v", line, invalid.Diagnostics)
				}
			}
			if !codes[c.wantCode] {
				t.Errorf("no %s diagnostic in %+v", c.wantCode, invalid.Diagnostics)
			}
		})
	}
}

// failingReader fails the test when it is read.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("a dataset was read before the model, scope, and threshold were validated")
	return 0, errors.New("unexpected read")
}

// An artifact that does not decode, an invalid scope, or an invalid
// threshold override is refused before any dataset is read.
func TestEvaluateValidatesBeforeReading(t *testing.T) {
	model, _ := trainModel(t, read(t, fixture), baselineConfig(t))
	tampered := bytes.Replace(model, []byte(`"selected_epoch": 300`), []byte(`"selected_epoch": 0`), 1)
	if bytes.Equal(tampered, model) {
		t.Fatal("the artifact does not record the selected epoch 300")
	}

	cases := []struct {
		name    string
		model   []byte
		options Options
		want    string
	}{
		{"truncated artifact", model[:len(model)/2], Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test)}, "decode model artifact"},
		{"invalid artifact", tampered, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test)}, "selected epoch 0 is out of range"},
		{"train partition", model, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Train)}, "never evaluated"},
		{"threshold override", model, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test), Threshold: float(2)}, "threshold override"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.options.Reference = failingReader{t}

			_, err := Evaluate(context.Background(), c.model, failingReader{t}, c.options)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Evaluate() error = %v, want %q", err, c.want)
			}
		})
	}
}

// cancellingReader serves data and cancels a context on its first read.
type cancellingReader struct {
	data   io.Reader
	cancel context.CancelFunc
	read   int
}

func (r *cancellingReader) Read(p []byte) (int, error) {
	n, err := r.data.Read(p)
	r.read += n
	r.cancel()
	return n, err
}

// Evaluation checks the context before reading and every 1,024 records,
// in the evaluated and in the reference dataset.
func TestEvaluateStopsWhenCanceled(t *testing.T) {
	data := read(t, fixture)
	model, _ := trainModel(t, data, baselineConfig(t))
	large := bytes.Repeat(data, 400) // 3,200 records
	limit := 1024*len(data)/8 + 128*1024

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Evaluate(ctx, model, failingReader{t}, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test)}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled before reading: Evaluate() error = %v, want context.Canceled", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	reader := &cancellingReader{data: bytes.NewReader(large), cancel: cancel}
	if _, err := Evaluate(ctx, model, reader, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test)}); !errors.Is(err, context.Canceled) {
		t.Errorf("dataset: Evaluate() error = %v, want context.Canceled", err)
	}
	if reader.read > limit {
		t.Errorf("read %d dataset bytes after cancellation, want at most %d", reader.read, limit)
	}

	ctx, cancel = context.WithCancel(context.Background())
	reader = &cancellingReader{data: bytes.NewReader(large), cancel: cancel}
	_, err := Evaluate(ctx, model, failingReader{t}, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test), Reference: reader})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("reference: Evaluate() error = %v, want context.Canceled", err)
	}
	if reader.read > limit {
		t.Errorf("read %d reference bytes after cancellation, want at most %d", reader.read, limit)
	}
}

// Evaluation only reads: the artifact and dataset bytes are unchanged, an
// override does not reach the next evaluation, and repeated evaluations
// give the same report.
func TestEvaluateOnlyReads(t *testing.T) {
	dataset := read(t, selectionFixture)
	model, _ := trainModel(t, dataset, selectionConfig(t))
	modelCopy, datasetCopy := bytes.Clone(model), bytes.Clone(dataset)

	options := Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test)}
	first := evaluate(t, model, dataset, options)
	evaluate(t, model, dataset, Options{MaxRecords: maxRecords, Scope: PartitionScope(evaluation.Test), Threshold: float(0)})
	again := evaluate(t, model, dataset, options)

	if !bytes.Equal(model, modelCopy) || !bytes.Equal(dataset, datasetCopy) {
		t.Error("evaluation changed the artifact or dataset bytes")
	}
	if !reflect.DeepEqual(first, again) {
		t.Errorf("repeated evaluation = %+v, want %+v", again, first)
	}
	if first.Threshold.Source != ThresholdFromArtifact {
		t.Errorf("Threshold = %+v, want the artifact's", first.Threshold)
	}
}

// Each dataset is read up to the record limit, and the limit is checked
// before anything is read.
func TestEvaluateStopsAtTheRecordLimit(t *testing.T) {
	dataset := read(t, fixture)
	model, _ := trainModel(t, dataset, baselineConfig(t))

	// The first seven records, within the limit.
	seven := bytes.Join(bytes.SplitAfter(dataset, []byte("\n"))[:7], nil)

	cases := map[string]struct {
		dataset []byte
		options Options
	}{
		"dataset":   {dataset, Options{MaxRecords: 7, Scope: PartitionScope(evaluation.Test)}},
		"reference": {seven, Options{MaxRecords: 7, Scope: PartitionScope(evaluation.Test), Reference: bytes.NewReader(dataset)}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Evaluate(context.Background(), model, bytes.NewReader(c.dataset), c.options)

			var limit *trainingdata.RecordLimitError
			if !errors.As(err, &limit) || limit.Limit != 7 {
				t.Fatalf("Evaluate() error = %v, want a *trainingdata.RecordLimitError", err)
			}
		})
	}

	evaluate(t, model, dataset, Options{MaxRecords: 8, Scope: PartitionScope(evaluation.Test), Reference: bytes.NewReader(dataset)})

	_, err := Evaluate(context.Background(), model, failingReader{t}, Options{Scope: PartitionScope(evaluation.Test)})
	if err == nil || !strings.Contains(err.Error(), "record limit must be at least 1") {
		t.Errorf("no limit: Evaluate() error = %v", err)
	}
}
