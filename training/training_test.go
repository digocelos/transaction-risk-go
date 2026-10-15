package training

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// config returns the baseline configuration for pixRiskFixture.
func config() trainingconfig.Config {
	return trainingconfig.Config{
		FeatureSchema:  pixrisk.Schema,
		HiddenLayers:   []int{4},
		Activation:     trainingconfig.ActivationSigmoid,
		Algorithm:      trainingconfig.AlgorithmFullBatchGradient,
		Loss:           trainingconfig.LossMeanSquaredError,
		Seed:           42,
		LearningRate:   1,
		MaxEpochs:      2000,
		TrainEnd:       pixRiskBoundaries.TrainEnd,
		ValidationEnd:  pixRiskBoundaries.ValidationEnd,
		Checkpoint:     trainingconfig.Checkpoint{Policy: trainingconfig.CheckpointFinalEpoch},
		EarlyStopping:  trainingconfig.EarlyStopping{Policy: trainingconfig.EarlyStoppingNone},
		Threshold:      trainingconfig.Threshold{Policy: trainingconfig.ThresholdFixed, Value: 0.5},
		TestEvaluation: trainingconfig.TestAfterSelection,
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

func run(t *testing.T, dataset []byte, config trainingconfig.Config) Result {
	t.Helper()

	result, err := Run(context.Background(), bytes.NewReader(dataset), config, DefaultLimits, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	return result
}

// scores exposes a trained model through its scores of the fixture's
// usable records, which together use every feature.
func scores(t *testing.T, result Result) []float64 {
	t.Helper()

	dataset := prepareDataset(t, read(t, pixRiskFixture))
	examples := append(append(append([]PreparedExample{}, dataset.Train...), dataset.Validation...), dataset.Test...)

	var scores []float64
	for _, example := range examples {
		values, err := result.State.Apply(example.Prepared)
		if err != nil {
			t.Fatal(err)
		}

		outputs, err := result.Network.Predict(values)
		if err != nil {
			t.Fatal(err)
		}
		scores = append(scores, outputs[0])
	}

	return scores
}

// replaceLine replaces one substring of one line of a dataset.
func replaceLine(t *testing.T, dataset []byte, line int, old, new string) []byte {
	t.Helper()

	lines := strings.SplitAfter(string(dataset), "\n")
	if strings.Count(lines[line-1], old) != 1 {
		t.Fatalf("line %d must contain %q exactly once", line, old)
	}

	lines[line-1] = strings.Replace(lines[line-1], old, new, 1)
	return []byte(strings.Join(lines, ""))
}

func TestRunReportsTheDatasetAndPartitions(t *testing.T) {
	report := run(t, read(t, pixRiskFixture), config()).Report

	wantRecords := trainingdata.Counts{Total: 8, Usable: 6, Insufficient: 1, Immature: 1}
	if report.Dataset.Records != wantRecords || report.Dataset.FeatureSchema != pixrisk.Schema {
		t.Errorf("dataset = %+v", report.Dataset)
	}

	partitions := report.Partitions
	want := map[string][2]evaluation.PartitionCounts{
		"train":      {partitions.Train, {Fraud: 1, Legitimate: 3, Insufficient: 1}},
		"validation": {partitions.Validation, {Fraud: 1}},
		"test":       {partitions.Test, {Fraud: 1, Immature: 1}},
	}
	for name, counts := range want {
		if counts[0] != counts[1] {
			t.Errorf("%s = %+v, want %+v", name, counts[0], counts[1])
		}
	}
}

func TestRunRecordsTheConfiguration(t *testing.T) {
	result := run(t, read(t, pixRiskFixture), config())

	if !reflect.DeepEqual(result.Report.Configuration.Config, config()) {
		t.Errorf("configuration = %+v, want %+v", result.Report.Configuration.Config, config())
	}

	want := trainingreport.Selection{
		RequestedEpochs:  2000,
		ActualEpochs:     2000,
		SelectedEpoch:    2000,
		StopReason:       trainingreport.StopMaxEpochs,
		CheckpointPolicy: "final_epoch",
		Threshold:        trainingreport.ThresholdChoice{Policy: "fixed", Value: 0.5},
	}
	if !reflect.DeepEqual(result.Report.Selection, want) {
		t.Errorf("selection = %+v, want %+v", result.Report.Selection, want)
	}

	if result.State.Schema != pixrisk.Schema || len(result.State.Features) != pixrisk.FeatureCount {
		t.Errorf("state = %s with %d features", result.State.Schema, len(result.State.Features))
	}

	if _, err := trainingreport.Marshal(result.Report); err != nil {
		t.Errorf("the report does not marshal: %v", err)
	}
}

func TestRunEvaluatesValidationAndTestAtTheThreshold(t *testing.T) {
	cfg := config()
	cfg.Threshold.Value = 0.3
	report := run(t, read(t, pixRiskFixture), cfg).Report

	if report.Validation.Threshold != 0.3 || report.Test.Threshold != 0.3 {
		t.Errorf("thresholds = %v and %v, want 0.3", report.Validation.Threshold, report.Test.Threshold)
	}

	// One fraud in each partition, detected or missed.
	for name, metrics := range map[string]evaluation.Metrics{"validation": report.Validation, "test": *report.Test} {
		if metrics.TruePositives+metrics.FalseNegatives != 1 || metrics.FalsePositives+metrics.TrueNegatives != 0 {
			t.Errorf("%s confusion matrix = %+v", name, metrics)
		}
	}

	if report.Test == nil || report.Test.UnknownLossFraudsDetected+report.Test.UnknownLossFraudsMissed != 1 {
		t.Errorf("test unknown losses = %+v", report.Test)
	}
}

// Validation and test records never reach the network parameters or the
// preparation state.
func TestRunTrainsOnTheTrainPartitionOnly(t *testing.T) {
	data := read(t, pixRiskFixture)
	baseline := run(t, data, config())

	changed := data
	for _, line := range []int{6, 7} {
		changed = replaceLine(t, changed, line, `"label":"fraud"`, `"label":"legitimate"`)
		changed = replaceLine(t, changed, line, `"initiated_outgoing_pix_24h":{"count":9`, `"initiated_outgoing_pix_24h":{"count":900`)
	}
	changed = replaceLine(t, changed, 6, `"loss_amount_cents":250000`, `"loss_amount_cents":0`)
	changed = replaceLine(t, changed, 7, `"loss_amount_cents":null`, `"loss_amount_cents":0`)

	other := run(t, changed, config())
	if !reflect.DeepEqual(other.State, baseline.State) || !reflect.DeepEqual(scores(t, other), scores(t, baseline)) {
		t.Error("changing validation and test records changed the model")
	}

	changedTrain := run(t, replaceLine(t, data, 4, `"amount_cents":15000`, `"amount_cents":990000`), config())
	if reflect.DeepEqual(scores(t, changedTrain), scores(t, baseline)) {
		t.Error("changing a train record did not change the model")
	}
}

func TestRunIsDeterministic(t *testing.T) {
	data := read(t, pixRiskFixture)

	first := run(t, data, config())
	second := run(t, data, config())

	if !reflect.DeepEqual(first.Report, second.Report) || !reflect.DeepEqual(scores(t, first), scores(t, second)) {
		t.Error("two runs produced different results")
	}

	changes := map[string]func(*trainingconfig.Config){
		"seed":          func(c *trainingconfig.Config) { c.Seed = 7 },
		"hidden layers": func(c *trainingconfig.Config) { c.HiddenLayers = []int{8, 3} },
		"no hidden":     func(c *trainingconfig.Config) { c.HiddenLayers = []int{} },
		"learning rate": func(c *trainingconfig.Config) { c.LearningRate = 0.1 },
	}
	for name, change := range changes {
		cfg := config()
		change(&cfg)
		if reflect.DeepEqual(scores(t, run(t, data, cfg)), scores(t, first)) {
			t.Errorf("changing the %s does not change the model", name)
		}
	}
}

func TestRunBuildsTheConfiguredArchitecture(t *testing.T) {
	cfg := config()
	cfg.HiddenLayers = []int{8, 3}
	result := run(t, read(t, pixRiskFixture), cfg)

	sizes := []int{result.Network.InputSize()}
	for _, layer := range result.Network.Layers() {
		sizes = append(sizes, len(layer.Biases))
	}
	if !reflect.DeepEqual(sizes, []int{62, 8, 3, 1}) {
		t.Errorf("layer sizes = %v, want [62 8 3 1]", sizes)
	}
}

func TestRunReportsTrainingLoss(t *testing.T) {
	cfg := config()
	cfg.MaxEpochs = 50

	var epochs []int
	var losses []float64
	_, err := Run(context.Background(), bytes.NewReader(read(t, pixRiskFixture)), cfg, DefaultLimits, func(epoch int, loss float64) {
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
	_, err := Run(context.Background(), bytes.NewReader(read(t, "../docs/contracts/training-examples-v2/dataset.jsonl")),
		config(), DefaultLimits, func(int, float64) { t.Fatal("a dataset with invalid records must not be trained") })

	var invalid *InvalidDatasetError
	if !errors.As(err, &invalid) || invalid.Report.Records.Invalid != 2 {
		t.Fatalf("Run() error = %v, want an InvalidDatasetError with 2 invalid records", err)
	}
}

func TestRunRejectsTrainPartitionsWithoutBothClasses(t *testing.T) {
	tests := map[string]struct {
		trainEnd time.Time
		want     evaluation.PartitionCounts
	}{
		"empty":           {time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), evaluation.PartitionCounts{}},
		"legitimate only": {time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), evaluation.PartitionCounts{Legitimate: 3, Insufficient: 1}},
	}

	for name, test := range tests {
		cfg := config()
		cfg.TrainEnd = test.trainEnd

		_, err := Run(context.Background(), bytes.NewReader(read(t, pixRiskFixture)), cfg, DefaultLimits, nil)

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
	tests := map[string]func(*trainingconfig.Config){
		"other schema":           func(c *trainingconfig.Config) { c.FeatureSchema = "transaction-features-v1" },
		"other algorithm":        func(c *trainingconfig.Config) { c.Algorithm = "adam" },
		"other loss":             func(c *trainingconfig.Config) { c.Loss = "cross-entropy" },
		"other activation":       func(c *trainingconfig.Config) { c.Activation = "relu" },
		"other checkpoint":       func(c *trainingconfig.Config) { c.Checkpoint.Policy = "last_improvement" },
		"other threshold policy": func(c *trainingconfig.Config) { c.Threshold.Policy = "max_f1" },
		"zero epochs":            func(c *trainingconfig.Config) { c.MaxEpochs = 0 },
		"too many epochs":        func(c *trainingconfig.Config) { c.MaxEpochs = trainingconfig.MaxEpochs + 1 },
		"zero learning rate":     func(c *trainingconfig.Config) { c.LearningRate = 0 },
		"threshold above 1":      func(c *trainingconfig.Config) { c.Threshold.Value = 1.5 },
		"empty hidden layer":     func(c *trainingconfig.Config) { c.HiddenLayers = []int{0} },
		"too wide":               func(c *trainingconfig.Config) { c.HiddenLayers = []int{257} },
		"too deep":               func(c *trainingconfig.Config) { c.HiddenLayers = []int{1, 1, 1, 1, 1} },
		"reversed boundaries":    func(c *trainingconfig.Config) { c.TrainEnd = c.ValidationEnd.Add(time.Second) },
		"negative seed":          func(c *trainingconfig.Config) { c.Seed = -1 },
	}

	for name, change := range tests {
		cfg := config()
		change(&cfg)

		if _, err := Run(context.Background(), failingReader{t}, cfg, DefaultLimits, nil); err == nil {
			t.Errorf("%s: Run() error = nil, want an error", name)
		}
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

// Cancellation is checked every 1,024 records while reading and every
// epoch while training.
func TestRunStopsWhenCanceled(t *testing.T) {
	fixture := read(t, pixRiskFixture)
	large := bytes.Repeat(fixture, 400) // 3,200 records

	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancellingReader{data: bytes.NewReader(large), cancel: cancel}

	_, err := Run(ctx, reader, config(), DefaultLimits, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}

	// At most 1,024 records, plus one read buffer, after the cancellation.
	if limit := 1024*len(fixture)/8 + 128*1024; reader.read > limit {
		t.Errorf("read %d bytes after cancellation, want at most %d", reader.read, limit)
	}

	ctx, cancel = context.WithCancel(context.Background())
	var epochs []int
	_, err = Run(ctx, bytes.NewReader(fixture), config(), DefaultLimits, func(epoch int, _ float64) {
		epochs = append(epochs, epoch)
		if epoch == 9 {
			cancel()
		}
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if len(epochs) != 10 {
		t.Errorf("trained %d epochs, want to stop right after epoch 9", len(epochs))
	}
}

// crossArchitectureTolerance bounds the difference between scores trained
// on different CPU architectures. Training is bit-for-bit reproducible only
// on one platform: fused multiply-add and math.Exp differ between arm64 and
// amd64 in their last bits. On this fixture the measured difference is at
// most about 6e-18.
const crossArchitectureTolerance = 1e-9

// Scores trained with the baseline configuration example, recorded on
// arm64. CI runs this test on amd64.
func TestRunMatchesReferenceScoresAcrossArchitectures(t *testing.T) {
	cfg, err := trainingconfig.Decode(read(t, "../docs/contracts/training-config-examples/01-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}

	reference := []float64{
		0.0048212408770026824, // line 1
		0.0060844999361150337, // line 2
		0.0064366893694704549, // line 4
		0.98956233721487585,   // line 5
		0.99093897463876834,   // line 6
		0.97385018870668616,   // line 7
	}

	got := scores(t, run(t, read(t, pixRiskFixture), cfg))
	if len(got) != len(reference) {
		t.Fatalf("got %d scores, want %d", len(got), len(reference))
	}

	for i, want := range reference {
		if diff := got[i] - want; diff > crossArchitectureTolerance || diff < -crossArchitectureTolerance {
			t.Errorf("score %d = %.17g, want %.17g within %g", i, got[i], want, crossArchitectureTolerance)
		}
	}
}
