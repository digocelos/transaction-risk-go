package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/internal/synthetic"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/modelevaluation"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/scoring"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
	"github.com/digocelos/transaction-risk-go/trainingrun"
)

// selectionConfig is the supported benchmark configuration with the
// checkpoint chosen on validation: the best validation loss, evaluated
// every 10 epochs, and the threshold with the highest validation recall at
// a false positive rate of at most 0.05.
func selectionConfig(t *testing.T) trainingconfig.Config {
	t.Helper()

	cfg, err := trainingconfig.Decode(read(t, filepath.Join(root, "docs/operations/benchmark-config.json")))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Checkpoint = trainingconfig.Checkpoint{
		Policy:             trainingconfig.CheckpointBestValidation,
		Objective:          trainingconfig.ObjectiveValidationLoss,
		EvaluationInterval: 10,
	}
	return cfg
}

func generate(t *testing.T, records int, seed uint64) []byte {
	t.Helper()

	var data bytes.Buffer
	if err := synthetic.Generate(&data, synthetic.DefaultOptions(records, seed)); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

// trained is a model trained, published, and promoted through the Go API.
type trained struct {
	result    training.Result
	report    []byte
	model     []byte
	runDir    string
	modelPath string
}

func train(t *testing.T, dataset []byte, cfg trainingconfig.Config) trained {
	t.Helper()

	result, err := training.Run(context.Background(), bytes.NewReader(dataset), cfg, training.DefaultLimits, nil)
	if err != nil {
		t.Fatalf("training.Run() error = %v", err)
	}
	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := modelartifact.FromTraining(result.Network, result.State, result.Report, digest(report),
		modelartifact.SoftwareFromBuild())
	if err != nil {
		t.Fatal(err)
	}
	model, err := modelartifact.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	config, err := cfg.Canonical()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	runDir, modelPath := filepath.Join(dir, "run"), filepath.Join(dir, "pix-risk.json")
	if err := trainingrun.Publish(runDir, trainingrun.Bundle{Model: model, Report: report, Config: config}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if _, err := trainingrun.Promote(runDir, modelPath, false); err != nil {
		t.Fatalf("Promote() error = %v", err)
	}

	return trained{result, report, model, runDir, modelPath}
}

// scoringInputs returns the exact scoring input bytes of every record.
func scoringInputs(t *testing.T, dataset []byte) [][]byte {
	t.Helper()

	var inputs [][]byte
	for _, line := range bytes.Split(bytes.TrimSuffix(dataset, []byte("\n")), []byte("\n")) {
		var record struct {
			ScoringInput json.RawMessage `json:"scoring_input"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, record.ScoringInput)
	}
	return inputs
}

// A synthetic dataset with skewed labels, immature and insufficient records
// is trained with validation-selected checkpoint and threshold, published,
// promoted, evaluated independently, and scored through the Go API. Every
// identity can be traced from a score back to the configuration and the
// dataset, the evaluation reproduces the training report, and scoring
// matches the training path bit for bit.
func TestFrozenModelThroughTheGoAPI(t *testing.T) {
	commands(t)

	dataset := generate(t, 6000, 7)
	cfg := selectionConfig(t)
	model := train(t, dataset, cfg)
	report := model.result.Report

	// The dataset has what the protocol must handle.
	records := report.Dataset.Records
	labels := report.Dataset.UsableLabels
	if records.Invalid != 0 || records.Immature == 0 || records.Insufficient == 0 {
		t.Errorf("records = %+v, want immature and insufficient records", records)
	}
	if share := float64(labels.Fraud) / float64(labels.Fraud+labels.Legitimate); share > 0.03 || labels.Fraud == 0 {
		t.Errorf("usable labels = %+v, want about 1%% fraud", labels)
	}
	for _, p := range []evaluation.PartitionCounts{report.Partitions.Train, report.Partitions.Validation, report.Partitions.Test} {
		if p.Fraud == 0 || p.Legitimate == 0 {
			t.Errorf("partition %+v lacks a class", p)
		}
	}
	if report.Partitions.Test.Immature != records.Immature {
		t.Errorf("immature records outside the test period: %+v", report.Partitions)
	}

	// The checkpoint and the threshold were chosen on validation.
	selection := report.Selection
	if selection.CheckpointPolicy != trainingconfig.CheckpointBestValidation || selection.Evaluations == 0 ||
		selection.Threshold.Policy != trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate || selection.Threshold.Candidates == nil {
		t.Errorf("selection = %+v", selection)
	}

	// Identities: the promoted file is the published artifact, which binds
	// the exact report, configuration, and dataset.
	promoted := read(t, model.modelPath)
	run, err := trainingrun.Verify(model.runDir)
	if err != nil {
		t.Fatal(err)
	}
	configSHA256, err := cfg.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	trainedOn := run.Artifact.Training
	if !bytes.Equal(promoted, model.model) || trainedOn.ReportSHA256 != digest(read(t, filepath.Join(model.runDir, trainingrun.ReportFile))) ||
		trainedOn.ConfigSHA256 != configSHA256 || trainedOn.DatasetSHA256 != digest(dataset) {
		t.Errorf("artifact identities = %+v", trainedOn)
	}

	// The frozen model, evaluated independently, reproduces the training
	// report and names everything that produced it.
	for _, c := range []struct {
		partition evaluation.Partition
		want      evaluation.Metrics
	}{
		{evaluation.Validation, report.Validation},
		{evaluation.Test, *report.Test},
	} {
		evaluated, err := modelevaluation.Evaluate(context.Background(), promoted, bytes.NewReader(dataset), modelevaluation.Options{
			Scope: modelevaluation.PartitionScope(c.partition), MaxRecords: trainingdata.DefaultMaxRecords,
		})
		if err != nil {
			t.Fatalf("Evaluate(%s) error = %v", c.partition, err)
		}
		if !reflect.DeepEqual(evaluated.Metrics, c.want) {
			t.Errorf("%s metrics differ from the training report", c.partition)
		}
		m := evaluated.Model
		if m.ArtifactSHA256 != digest(promoted) || m.ConfigSHA256 != configSHA256 ||
			m.TrainingDatasetSHA256 != digest(dataset) || m.TrainingReportSHA256 != digest(model.report) ||
			!evaluated.SameDatasetAsTraining {
			t.Errorf("evaluation identities = %+v", m)
		}
	}

	// Scoring: every record that is not insufficient is scored, with the
	// training path's score for usable records, and every result names the
	// promoted artifact.
	scorer, err := scoring.Load(model.modelPath)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := training.PrepareDataset(context.Background(), bytes.NewReader(dataset), cfg.Boundaries(), training.DefaultLimits.MaxRecords)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[int]float64{}
	for _, examples := range [][]training.PreparedExample{prepared.Train, prepared.Validation, prepared.Test} {
		for _, example := range examples {
			outputs, err := model.result.Network.Predict(example.Values)
			if err != nil {
				t.Fatal(err)
			}
			expected[example.Line] = outputs[0]
		}
	}

	inputs := scoringInputs(t, dataset)
	statuses := map[string]int{}
	results := make([]scoring.Result, len(inputs))
	for i, input := range inputs {
		result, err := scorer.Score(input)
		if err != nil {
			t.Fatal(err)
		}
		results[i] = result
		statuses[result.Status]++

		if result.Model.ArtifactSHA256 != digest(promoted) {
			t.Fatalf("line %d: scored with %s", i+1, result.Model.ArtifactSHA256)
		}
		if want, ok := expected[i+1]; ok && (result.Score == nil || *result.Score != want) {
			t.Errorf("line %d: score %v, want the training path's %v", i+1, result.Score, want)
		}
	}
	if statuses[scoring.StatusInsufficientInput] != records.Insufficient ||
		statuses[scoring.StatusScored] != records.Usable+records.Immature || statuses[scoring.StatusInvalidInput] != 0 {
		t.Errorf("statuses = %v, want the dataset's %+v", statuses, records)
	}

	// One scorer shared by goroutines gives the same results.
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := g; i < len(inputs); i += 8 {
				result, err := scorer.Score(inputs[i])
				if err != nil || !reflect.DeepEqual(result, results[i]) {
					t.Errorf("line %d: concurrent result differs", i+1)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// relabel rewrites the outcomes and group keys of the records that match,
// keeping their scoring inputs byte for byte: frauds become legitimate,
// legitimate payments become frauds with an unknown loss, and every key
// changes.
func relabel(t *testing.T, dataset []byte, match func(decisionTime string) bool) []byte {
	t.Helper()

	var out bytes.Buffer
	for _, line := range bytes.Split(bytes.TrimSuffix(dataset, []byte("\n")), []byte("\n")) {
		var record struct {
			TrainingExampleVersion string          `json:"training_example_version"`
			ScoringInput           json.RawMessage `json:"scoring_input"`
			Outcome                struct {
				Label           string `json:"label"`
				LabelAsOf       string `json:"label_as_of"`
				LossAmountCents *int64 `json:"loss_amount_cents"`
			} `json:"outcome"`
			GroupKey *string `json:"group_key"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		var input struct {
			DecisionTime string `json:"decision_time"`
		}
		if err := json.Unmarshal(record.ScoringInput, &input); err != nil {
			t.Fatal(err)
		}

		if match(input.DecisionTime) {
			zero := int64(0)
			if record.Outcome.Label == "fraud" {
				record.Outcome.Label, record.Outcome.LossAmountCents = "legitimate", &zero
			} else {
				record.Outcome.Label, record.Outcome.LossAmountCents = "fraud", nil
			}
			key := "relabeled"
			record.GroupKey = &key
		}

		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(append(data, '\n'))
	}
	return out.Bytes()
}

// Outcomes and group keys never reach features or selection: changing
// every test outcome leaves the preparation state, the parameters, the
// selected epoch, and the threshold unchanged, and changing every outcome
// leaves every prepared feature unchanged.
func TestOutcomesNeverReachTheModel(t *testing.T) {
	commands(t)

	dataset := generate(t, 3000, 3)
	cfg := selectionConfig(t)
	validationEnd := cfg.ValidationEnd

	afterValidation := func(decisionTime string) bool {
		parsed, err := time.Parse(time.RFC3339Nano, decisionTime)
		if err != nil {
			t.Fatal(err)
		}
		return parsed.After(validationEnd)
	}

	original := train(t, dataset, cfg)
	changed := train(t, relabel(t, dataset, afterValidation), cfg)

	a, err := modelartifact.Decode(original.model)
	if err != nil {
		t.Fatal(err)
	}
	b, err := modelartifact.Decode(changed.model)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.State, b.State) || !reflect.DeepEqual(a.Network.Layers(), b.Network.Layers()) ||
		a.SelectedEpoch != b.SelectedEpoch || a.Threshold != b.Threshold {
		t.Error("test outcomes changed the preparation state, the parameters, the selected epoch, or the threshold")
	}
	if reflect.DeepEqual(original.result.Report.Test, changed.result.Report.Test) {
		t.Error("the relabeled test partition has the same metrics; the test did not change anything")
	}

	everything := relabel(t, dataset, func(string) bool { return true })
	before, err := training.PrepareDataset(context.Background(), bytes.NewReader(dataset), cfg.Boundaries(), training.DefaultLimits.MaxRecords)
	if err != nil {
		t.Fatal(err)
	}
	after, err := training.PrepareDataset(context.Background(), bytes.NewReader(everything), cfg.Boundaries(), training.DefaultLimits.MaxRecords)
	if err != nil {
		t.Fatal(err)
	}
	features := func(p training.PreparedDataset) map[int][pixrisk.FeatureCount]float64 {
		values := map[int][pixrisk.FeatureCount]float64{}
		for _, examples := range [][]training.PreparedExample{p.Train, p.Validation, p.Test} {
			for _, example := range examples {
				values[example.Line] = example.Prepared.Values
			}
		}
		return values
	}
	if !reflect.DeepEqual(features(before), features(after)) {
		t.Error("changing outcomes changed prepared features")
	}
}
