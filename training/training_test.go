package training

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// The fixture holds 12 synthetic records: 3 legitimate and 3 fraud records
// from September to December 2025, 1 of each in January and February 2026,
// 1 of each in March and April 2026, then 1 insufficient record (May 2026)
// and 1 immature record (August 2026).
const fixture = "testdata/dataset.jsonl"

func config() Config {
	return Config{
		Boundaries: evaluation.Boundaries{
			TrainEnd:      time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC),
			ValidationEnd: time.Date(2026, 2, 28, 23, 59, 59, 0, time.UTC),
		},
		Seed:         42,
		Epochs:       5000,
		LearningRate: 1,
		Threshold:    0.5,
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func run(t *testing.T, dataset []byte, config Config) Result {
	t.Helper()

	result, err := Run(bytes.NewReader(dataset), config, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	return result
}

// probes are feature vectors that expose the network parameters.
var probes = [][]float64{
	{0.1, 0.1, 1, 0},
	{0.9, 0.45, 1, 1},
	{0.5, 0.25, 0, 1},
	{0, 0, 0, 0},
}

func scores(t *testing.T, network *nn.Network) []float64 {
	t.Helper()

	var scores []float64
	for _, inputs := range probes {
		outputs, err := network.Predict(inputs)
		if err != nil {
			t.Fatal(err)
		}
		scores = append(scores, outputs[0])
	}

	return scores
}

// replaceLine replaces one substring of one line of the fixture.
func replaceLine(t *testing.T, dataset []byte, line int, old, new string) []byte {
	t.Helper()

	lines := strings.SplitAfter(string(dataset), "\n")
	if strings.Count(lines[line-1], old) != 1 {
		t.Fatalf("line %d must contain %q exactly once", line, old)
	}

	lines[line-1] = strings.Replace(lines[line-1], old, new, 1)
	return []byte(strings.Join(lines, ""))
}

func TestRunReportsEveryRecordAndPartition(t *testing.T) {
	report := run(t, read(t, fixture), config()).Report

	wantRecords := trainingdata.Counts{Total: 12, Usable: 10, Insufficient: 1, Immature: 1}
	if report.Dataset.Records != wantRecords {
		t.Errorf("records = %+v, want %+v", report.Dataset.Records, wantRecords)
	}

	if len(report.Dataset.Exclusions) != 2 {
		t.Errorf("exclusions = %+v, want the insufficient and immature records", report.Dataset.Exclusions)
	}

	partitions := report.Partitions
	want := map[string][2]evaluation.PartitionCounts{
		"train":      {partitions.Train, {Fraud: 3, Legitimate: 3}},
		"validation": {partitions.Validation, {Fraud: 1, Legitimate: 1}},
		"test":       {partitions.Test, {Fraud: 1, Legitimate: 1, Insufficient: 1, Immature: 1}},
	}
	for name, counts := range want {
		if counts[0] != counts[1] {
			t.Errorf("%s = %+v, want %+v", name, counts[0], counts[1])
		}
	}

	// g-a1 is in train and validation, g-a2 in train and test.
	wantOverlap := evaluation.GroupOverlap{SharedKeys: 2, RecordsSharingTrainKeys: 2}
	if partitions.GroupOverlap != wantOverlap {
		t.Errorf("group overlap = %+v, want %+v", partitions.GroupOverlap, wantOverlap)
	}

	if !partitions.TrainEnd.Equal(config().Boundaries.TrainEnd) ||
		!partitions.ValidationEnd.Equal(config().Boundaries.ValidationEnd) {
		t.Errorf("boundaries = %s, %s", partitions.TrainEnd, partitions.ValidationEnd)
	}
}

func TestRunRecordsTheConfiguration(t *testing.T) {
	report := run(t, read(t, fixture), config()).Report

	want := trainingreport.Configuration{
		Algorithm:     "full-batch-gradient-descent",
		FeatureSchema: "transaction-features-v1",
		Seed:          42,
		Epochs:        5000,
		LearningRate:  1,
		Threshold:     0.5,
	}

	if report.Configuration != want {
		t.Errorf("configuration = %+v, want %+v", report.Configuration, want)
	}
}

func TestRunEvaluatesValidationAndTestAtTheThreshold(t *testing.T) {
	report := run(t, read(t, fixture), config()).Report

	for name, metrics := range map[string]evaluation.Metrics{"validation": report.Validation, "test": report.Test} {
		if metrics.Threshold != 0.5 {
			t.Errorf("%s threshold = %v, want 0.5", name, metrics.Threshold)
		}

		// One fraud and one legitimate record, both easy to separate.
		if metrics.TruePositives != 1 || metrics.TrueNegatives != 1 ||
			metrics.FalsePositives != 0 || metrics.FalseNegatives != 0 {
			t.Errorf("%s confusion matrix = %d TP, %d FP, %d TN, %d FN", name,
				metrics.TruePositives, metrics.FalsePositives, metrics.TrueNegatives, metrics.FalseNegatives)
		}
	}

	if report.Validation.FraudAmountCentsDetected != 200000 || report.Validation.KnownLossCentsDetected != 200000 {
		t.Errorf("validation amounts = %+v", report.Validation)
	}

	if report.Test.FraudAmountCentsDetected != 170000 || report.Test.UnknownLossFraudsDetected != 1 {
		t.Errorf("test amounts = %+v", report.Test)
	}

	cfg := config()
	cfg.Threshold = 0.3
	report = run(t, read(t, fixture), cfg).Report

	if report.Validation.Threshold != 0.3 || report.Test.Threshold != 0.3 {
		t.Errorf("thresholds = %v and %v, want the configured 0.3", report.Validation.Threshold, report.Test.Threshold)
	}
}

// Labels and features of validation and test records never reach the
// network parameters.
func TestRunTrainsOnTheTrainPartitionOnly(t *testing.T) {
	dataset := read(t, fixture)
	baseline := scores(t, run(t, dataset, config()).Network)

	changed := dataset
	for _, line := range []int{7, 9} {
		changed = replaceLine(t, changed, line, `"count":14`, `"count":0`)
		changed = replaceLine(t, changed, line, `"label":"legitimate"`, `"label":"fraud"`)
	}
	changed = replaceLine(t, changed, 8, `"label":"fraud"`, `"label":"legitimate"`)
	changed = replaceLine(t, changed, 8, `"loss_amount_cents":200000`, `"loss_amount_cents":0`)

	if got := scores(t, run(t, changed, config()).Network); !reflect.DeepEqual(got, baseline) {
		t.Errorf("changing validation and test records changed the model: %v, want %v", got, baseline)
	}

	// A change to one train record does change the model.
	changedTrain := replaceLine(t, dataset, 5, `"count":14`, `"count":0`)
	if got := scores(t, run(t, changedTrain, config()).Network); reflect.DeepEqual(got, baseline) {
		t.Error("changing a train record did not change the model")
	}
}

func TestRunIsDeterministic(t *testing.T) {
	dataset := read(t, fixture)

	first := run(t, dataset, config())
	second := run(t, dataset, config())

	if !reflect.DeepEqual(first.Report, second.Report) {
		t.Error("two runs produced different reports")
	}

	if !reflect.DeepEqual(scores(t, first.Network), scores(t, second.Network)) {
		t.Error("two runs produced different models")
	}

	other := config()
	other.Seed = 7
	if reflect.DeepEqual(scores(t, run(t, dataset, other).Network), scores(t, first.Network)) {
		t.Error("the seed does not change the model")
	}
}

func TestRunReportsTrainingLoss(t *testing.T) {
	cfg := config()
	cfg.Epochs = 50

	var epochs []int
	var losses []float64
	_, err := Run(bytes.NewReader(read(t, fixture)), cfg, func(epoch int, loss float64) {
		epochs = append(epochs, epoch)
		losses = append(losses, loss)
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(epochs) != 50 || epochs[0] != 0 || epochs[49] != 49 {
		t.Fatalf("progress epochs = %v, want 0 to 49", epochs)
	}

	if !(losses[49] < losses[0]) {
		t.Errorf("training loss went from %v to %v, want a decrease", losses[0], losses[49])
	}
}

func TestRunStopsOnInvalidRecords(t *testing.T) {
	data := read(t, "../docs/contracts/training-examples/dataset.jsonl")

	_, err := Run(bytes.NewReader(data), config(), func(int, float64) {
		t.Fatal("a dataset with invalid records must not be trained")
	})

	var invalid *InvalidDatasetError
	if !errors.As(err, &invalid) {
		t.Fatalf("Run() error = %v, want an InvalidDatasetError", err)
	}

	wantRecords := trainingdata.Counts{Total: 7, Usable: 3, Invalid: 2, Insufficient: 1, Immature: 1}
	if invalid.Report.Records != wantRecords {
		t.Errorf("records = %+v, want %+v", invalid.Report.Records, wantRecords)
	}

	if len(invalid.Report.Exclusions) != 4 {
		t.Errorf("exclusions = %+v, want 4", invalid.Report.Exclusions)
	}

	var lines []int
	for _, diagnostic := range invalid.Diagnostics {
		if diagnostic.Message == "" {
			t.Errorf("diagnostic %+v has no message", diagnostic)
		}
		lines = append(lines, diagnostic.Line)
	}

	if !reflect.DeepEqual(lines, []int{4, 6}) {
		t.Errorf("diagnostic lines = %v, want [4 6]", lines)
	}
}

func TestRunRejectsTrainPartitionsWithoutBothClasses(t *testing.T) {
	dataset := read(t, fixture)
	firstIsFraud := replaceLine(t, dataset, 1, `"label":"legitimate"`, `"label":"fraud"`)

	tests := map[string]struct {
		dataset  []byte
		trainEnd time.Time
		want     evaluation.PartitionCounts
	}{
		"empty":           {dataset, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), evaluation.PartitionCounts{}},
		"legitimate only": {dataset, time.Date(2025, 9, 10, 0, 0, 0, 0, time.UTC), evaluation.PartitionCounts{Legitimate: 1}},
		"fraud only":      {firstIsFraud, time.Date(2025, 9, 10, 0, 0, 0, 0, time.UTC), evaluation.PartitionCounts{Fraud: 1}},
	}

	for name, test := range tests {
		cfg := config()
		cfg.Boundaries.TrainEnd = test.trainEnd

		_, err := Run(bytes.NewReader(test.dataset), cfg, nil)

		var untrainable *UntrainableError
		if !errors.As(err, &untrainable) {
			t.Errorf("%s: Run() error = %v, want an UntrainableError", name, err)
			continue
		}

		if untrainable.Train != test.want {
			t.Errorf("%s: train counts = %+v, want %+v", name, untrainable.Train, test.want)
		}
	}
}

// failingReader fails the test when the dataset is read.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("the dataset was read before the configuration was validated")
	return 0, errors.New("unexpected read")
}

func TestRunRejectsInvalidConfigurationsBeforeReading(t *testing.T) {
	tests := map[string]func(*Config){
		"zero epochs":            func(c *Config) { c.Epochs = 0 },
		"zero learning rate":     func(c *Config) { c.LearningRate = 0 },
		"threshold above 1":      func(c *Config) { c.Threshold = 1.5 },
		"negative threshold":     func(c *Config) { c.Threshold = -0.1 },
		"missing train_end":      func(c *Config) { c.Boundaries.TrainEnd = time.Time{} },
		"reversed boundaries":    func(c *Config) { c.Boundaries.TrainEnd = c.Boundaries.ValidationEnd.Add(time.Second) },
		"equal boundaries":       func(c *Config) { c.Boundaries.TrainEnd = c.Boundaries.ValidationEnd },
		"infinite learning rate": func(c *Config) { c.LearningRate = 1 / zero },
	}

	for name, change := range tests {
		cfg := config()
		change(&cfg)

		if _, err := Run(failingReader{t}, cfg, nil); err == nil {
			t.Errorf("%s: Run() error = nil, want an error", name)
		}
	}
}

var zero float64
