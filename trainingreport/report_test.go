package trainingreport

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

const (
	examplesDir = "../docs/contracts/training-examples-v2"
	configsDir  = "../docs/contracts/training-config-examples"
)

// dataset joins six usable training examples: legitimate payments on May
// 14, 20, and 25 and a fraud on June 2 (train), a fraud on June 9
// (validation), and a fraud on June 18 (test).
func dataset(t *testing.T) []byte {
	t.Helper()

	var data []byte
	for _, name := range []string{
		"01-usable-legitimate", "04-legitimate-registered-device-pre-registered-recipient",
		"06-legitimate-no-device-no-period-limit", "02-usable-fraud",
		"03-fraud-registered-device-pre-registered-recipient", "05-unknown-loss-no-pix-key",
	} {
		line, err := os.ReadFile(filepath.Join(examplesDir, name+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
	}

	return data
}

func config(t *testing.T, name string) trainingconfig.Config {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(configsDir, name))
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := trainingconfig.Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	return cfg
}

// buildReport runs the dataset through the training data, preparation, and
// evaluation packages, with fixed scores standing in for a model, as a
// final_epoch run would report it.
func buildReport(t *testing.T, cfg trainingconfig.Config) Report {
	t.Helper()

	partitioner, err := evaluation.NewPartitioner(cfg.Boundaries())
	if err != nil {
		t.Fatal(err)
	}

	scores := map[int]float64{1: 0.1, 2: 0.2, 3: 0.3, 4: 0.9, 5: 0.6, 6: 0.4}
	predictions := map[evaluation.Partition][]evaluation.Prediction{}
	prepared := map[evaluation.Partition][]pixrisk.Prepared{}
	var exclusions []trainingdata.Exclusion

	summary, err := trainingdata.Scan(bytes.NewReader(dataset(t)), trainingdata.PixRiskV1,
		func(record trainingdata.Record) error {
			if exclusion, ok := record.Exclusion(); ok {
				exclusions = append(exclusions, exclusion)
			}

			partition, ok := partitioner.Add(record)
			if !ok || record.Category != trainingdata.Usable {
				return nil
			}

			values, err := pixrisk.Prepare(record.Example.ScoringInput)
			if err != nil {
				return err
			}
			prepared[partition] = append(prepared[partition], values)

			predictions[partition] = append(predictions[partition], evaluation.Prediction{
				Score:           scores[record.Line],
				Label:           record.Example.Outcome.Label,
				AmountCents:     record.Example.ScoringInput.PixPayment.AmountCents,
				LossAmountCents: record.Example.Outcome.LossAmountCents,
			})
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	threshold := cfg.Threshold.Value
	validation, err := evaluation.Evaluate(predictions[evaluation.Validation], threshold)
	if err != nil {
		t.Fatal(err)
	}

	report := Report{
		Configuration: Configuration{Config: cfg},
		Dataset:       trainingdata.NewReport(summary, exclusions),
		Partitions:    partitioner.Report(),
		Coverage:      NewCoverage(prepared[evaluation.Train], prepared[evaluation.Validation], prepared[evaluation.Test]),
		Selection: Selection{
			RequestedEpochs:  cfg.MaxEpochs,
			ActualEpochs:     cfg.MaxEpochs,
			SelectedEpoch:    cfg.MaxEpochs,
			StopReason:       StopMaxEpochs,
			CheckpointPolicy: cfg.Checkpoint.Policy,
			Threshold:        ThresholdChoice{Policy: cfg.Threshold.Policy, Value: threshold},
		},
		Validation: validation,
	}

	if cfg.TestEvaluation == trainingconfig.TestAfterSelection {
		test, err := evaluation.Evaluate(predictions[evaluation.Test], threshold)
		if err != nil {
			t.Fatal(err)
		}
		report.TestEvaluated = true
		report.Test = &test
	}

	return report
}

func marshal(t *testing.T, report Report) []byte {
	t.Helper()

	data, err := Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	return data
}

func TestMarshalIsDeterministic(t *testing.T) {
	cfg := config(t, "01-baseline.json")
	first := marshal(t, buildReport(t, cfg))

	if second := marshal(t, buildReport(t, cfg)); !bytes.Equal(first, second) {
		t.Error("two encodings of the same report differ")
	}

	// The same boundaries written with another offset produce the same bytes.
	brasilia := time.FixedZone("", -3*60*60)
	shifted := cfg
	shifted.TrainEnd = cfg.TrainEnd.In(brasilia)
	shifted.ValidationEnd = cfg.ValidationEnd.In(brasilia)

	if got := marshal(t, buildReport(t, shifted)); !bytes.Equal(first, got) {
		t.Error("boundary offsets changed the report bytes")
	}

	if !bytes.HasSuffix(first, []byte("}\n")) {
		t.Error("report must end with a newline")
	}
}

func TestMarshalRecordsEveryPart(t *testing.T) {
	cfg := config(t, "01-baseline.json")
	data := marshal(t, buildReport(t, cfg))

	var report struct {
		Version       string `json:"training_report_version"`
		Configuration struct {
			SHA256 string          `json:"config_sha256"`
			Config json.RawMessage `json:"config"`
		} `json:"configuration"`
		Dataset struct {
			FeatureSchema string `json:"feature_schema"`
		} `json:"dataset"`
		Coverage struct {
			Train struct {
				UsableRecords int                       `json:"usable_records"`
				Populations   map[string]int            `json:"populations"`
				LimitPeriods  map[string]int            `json:"limit_periods"`
				Sources       map[string]map[string]int `json:"sources"`
			} `json:"train"`
			NotExercisedInTrain []SourceState `json:"not_exercised_in_train"`
		} `json:"coverage"`
		Selection struct {
			RequestedEpochs int      `json:"requested_epochs"`
			SelectedEpoch   int      `json:"selected_epoch"`
			StopReason      string   `json:"stop_reason"`
			Objective       *string  `json:"objective"`
			Value           *float64 `json:"selected_objective_value"`
			Threshold       struct {
				Policy string  `json:"policy"`
				Value  float64 `json:"value"`
			} `json:"threshold"`
		} `json:"selection"`
		TestEvaluated bool                `json:"test_evaluated"`
		Test          *evaluation.Metrics `json:"test"`
	}

	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}

	wantSHA256, err := cfg.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := cfg.Canonical()
	if err != nil {
		t.Fatal(err)
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, report.Configuration.Config); err != nil {
		t.Fatal(err)
	}

	train := report.Coverage.Train

	switch {
	case report.Version != "pix-training-report-v2":
		t.Errorf("version = %q", report.Version)
	case report.Configuration.SHA256 != wantSHA256 || !bytes.Equal(compact.Bytes(), canonical):
		t.Errorf("configuration = %s %s", report.Configuration.SHA256, report.Configuration.Config)
	case report.Dataset.FeatureSchema != "pix-risk-v1":
		t.Errorf("dataset feature schema = %q", report.Dataset.FeatureSchema)
	case train.UsableRecords != 4 || train.Populations["no_payer_device"] != 1 || train.Populations["no_period_limit"] != 1:
		t.Errorf("train coverage = %+v", train)
	case train.LimitPeriods["daytime"] != 3 || train.LimitPeriods["no_period_limit"] != 1:
		t.Errorf("train limit periods = %v", train.LimitPeriods)
	case train.Sources["payer_device_registration"]["known"] != 3 || train.Sources["payer_device_registration"]["not_applicable"] != 1:
		t.Errorf("train device registration states = %v", train.Sources["payer_device_registration"])
	case report.Selection.RequestedEpochs != 5000 || report.Selection.SelectedEpoch != 5000 || report.Selection.StopReason != "max_epochs":
		t.Errorf("selection = %+v", report.Selection)
	case report.Selection.Objective != nil || report.Selection.Value != nil:
		t.Errorf("a final_epoch run has no objective: %+v", report.Selection)
	case report.Selection.Threshold.Policy != "fixed" || report.Selection.Threshold.Value != 0.5:
		t.Errorf("threshold = %+v", report.Selection.Threshold)
	case !report.TestEvaluated || report.Test == nil || report.Test.Threshold != 0.5:
		t.Errorf("test = %v %+v", report.TestEvaluated, report.Test)
	}

	if !containsState(report.Coverage.NotExercisedInTrain, SourceState{"period_limit", "stale"}) ||
		containsState(report.Coverage.NotExercisedInTrain, SourceState{"period_limit", "known"}) {
		t.Errorf("not exercised in train = %v", report.Coverage.NotExercisedInTrain)
	}
}

func containsState(states []SourceState, want SourceState) bool {
	for _, state := range states {
		if state == want {
			return true
		}
	}
	return false
}

func TestMarshalOfASkippedTestPartition(t *testing.T) {
	cfg := config(t, "02-no-hidden-layer.json")
	data := marshal(t, buildReport(t, cfg))

	if !bytes.Contains(data, []byte(`"test_evaluated": false`)) || !bytes.Contains(data, []byte(`"test": null`)) {
		t.Errorf("report does not show a skipped test partition:\n%s", data)
	}
}

func TestMarshalRejectsInconsistentReports(t *testing.T) {
	best := config(t, "04-best-validation-loss.json")

	tests := map[string]func(*Report){
		"invalid configuration":   func(r *Report) { r.Configuration.Config.MaxEpochs = 0 },
		"other feature schema":    func(r *Report) { r.Dataset.FeatureSchema = "transaction-features-v1" },
		"invalid records":         func(r *Report) { r.Dataset.Records.Invalid = 1; r.Dataset.Records.Total++ },
		"wrong dataset report":    func(r *Report) { r.Dataset.DatasetReportVersion = "v0" },
		"other boundaries":        func(r *Report) { r.Partitions.TrainEnd = r.Partitions.TrainEnd.Add(time.Second) },
		"other requested epochs":  func(r *Report) { r.Selection.RequestedEpochs = 4000 },
		"more epochs than asked":  func(r *Report) { r.Selection.ActualEpochs = 6000 },
		"selected after actual":   func(r *Report) { r.Selection.SelectedEpoch = 6000 },
		"stopped early w/o rule":  func(r *Report) { r.Selection.StopReason = StopEarlyStopping },
		"short max_epochs run":    func(r *Report) { r.Selection.ActualEpochs = 4000; r.Selection.SelectedEpoch = 4000 },
		"unknown stop reason":     func(r *Report) { r.Selection.StopReason = "canceled" },
		"final epoch not last":    func(r *Report) { r.Selection.SelectedEpoch = 100 },
		"final epoch objective":   func(r *Report) { objective := "validation_loss"; r.Selection.Objective = &objective },
		"other checkpoint policy": func(r *Report) { r.Selection.CheckpointPolicy = "best_validation" },
		"other threshold value":   func(r *Report) { r.Selection.Threshold.Value = 0.4 },
		"other threshold policy":  func(r *Report) { r.Selection.Threshold.Policy = "max_recall_at_max_false_positive_rate" },
		"validation threshold":    func(r *Report) { r.Validation.Threshold = 0.4 },
		"test threshold":          func(r *Report) { r.Test.Threshold = 0.4 },
		"test not evaluated":      func(r *Report) { r.TestEvaluated = false },
		"test without metrics":    func(r *Report) { r.Test = nil },
		"best validation without objective": func(r *Report) {
			r.Configuration.Config = best
			r.Selection.RequestedEpochs, r.Selection.ActualEpochs, r.Selection.SelectedEpoch = 3000, 3000, 3000
			r.Selection.CheckpointPolicy = "best_validation"
		},
	}

	for name, change := range tests {
		report := buildReport(t, config(t, "01-baseline.json"))
		change(&report)

		if _, err := Marshal(report); err == nil {
			t.Errorf("%s: Marshal() error = nil, want an error", name)
		}
	}
}

func TestCoverageListsStatesTheTrainPartitionNeverHas(t *testing.T) {
	coverage := NewCoverage(nil, nil, nil)

	var want []SourceState
	for _, source := range pixrisk.Sources() {
		for _, state := range pixrisk.PossibleStates(source) {
			want = append(want, SourceState{source, state})
		}
	}

	if !reflect.DeepEqual(coverage.NotExercisedInTrain, want) {
		t.Errorf("an empty train partition must list every state, got %v", coverage.NotExercisedInTrain)
	}

	if coverage.Train.UsableRecords != 0 || coverage.Train.Populations["new_payer_account"] != 0 {
		t.Errorf("empty coverage = %+v", coverage.Train)
	}
}
