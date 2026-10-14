package training

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// selectionFixture holds 13 pix-training-example-v2 records: six train
// records (lines 1–6), two legitimate and two fraud validation records
// (lines 7–10), and three test records (lines 11–13). Two validation
// records contradict the train records: a fraud from a registered device to
// a pre-registered recipient (line 8) and a legitimate payment with the
// profile of the train frauds (line 9), so the validation loss rises once
// the network fits the train partition.
const selectionFixture = "testdata/pix-risk-v1-selection.jsonl"

func selectionConfig() trainingconfig.Config {
	cfg := config()
	cfg.TrainEnd = time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	cfg.ValidationEnd = time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	return cfg
}

func bestValidation(objective string, interval int) trainingconfig.Checkpoint {
	return trainingconfig.Checkpoint{
		Policy:             trainingconfig.CheckpointBestValidation,
		Objective:          objective,
		EvaluationInterval: interval,
	}
}

func runSelection(t *testing.T, data []byte, cfg trainingconfig.Config) Result {
	t.Helper()

	result, err := Run(context.Background(), bytes.NewReader(data), cfg, DefaultLimits, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	return result
}

// predictions scores the prepared records of one partition with a result.
func predictions(t *testing.T, result Result, examples []PreparedExample) []evaluation.Prediction {
	t.Helper()

	var predictions []evaluation.Prediction
	for _, example := range examples {
		values, err := result.State.Apply(example.Prepared)
		if err != nil {
			t.Fatal(err)
		}

		outputs, err := result.Network.Predict(values)
		if err != nil {
			t.Fatal(err)
		}

		predictions = append(predictions, evaluation.Prediction{
			Score: outputs[0], Label: example.Label, AmountCents: example.AmountCents,
			LossAmountCents: example.LossAmountCents,
		})
	}

	return predictions
}

func preparedSelection(t *testing.T, data []byte) PreparedDataset {
	t.Helper()

	dataset, err := PrepareDataset(context.Background(), bytes.NewReader(data), selectionConfig().Boundaries(), DefaultLimits.MaxRecords)
	if err != nil {
		t.Fatal(err)
	}

	return dataset
}

func validationLoss(predictions []evaluation.Prediction) float64 {
	total := 0.0
	for _, p := range predictions {
		target := 0.0
		if p.Label == trainingdata.Fraud {
			target = 1
		}
		total += (p.Score - target) * (p.Score - target)
	}
	return total / float64(len(predictions))
}

// The best validation checkpoint is restored even when later epochs keep
// training: the model is exactly the network after the selected epoch.
func TestRunRestoresTheBestValidationCheckpoint(t *testing.T) {
	data := read(t, selectionFixture)

	cfg := selectionConfig()
	cfg.Checkpoint = bestValidation(trainingconfig.ObjectiveValidationLoss, 10)
	result := runSelection(t, data, cfg)

	selection := result.Report.Selection
	if selection.SelectedEpoch >= selection.ActualEpochs || selection.ActualEpochs != cfg.MaxEpochs {
		t.Fatalf("selection = %+v, want a selected epoch earlier than the %d trained", selection, cfg.MaxEpochs)
	}

	if selection.SelectedEpoch%10 != 0 || selection.Evaluations != cfg.MaxEpochs/10 || selection.StopReason != trainingreport.StopMaxEpochs {
		t.Errorf("selection = %+v", selection)
	}

	// Training only up to the selected epoch gives the same network.
	short := selectionConfig()
	short.MaxEpochs = selection.SelectedEpoch
	if !reflect.DeepEqual(scores(t, runSelection(t, data, short)), scores(t, result)) {
		t.Error("the restored model is not the network after the selected epoch")
	}

	dataset := preparedSelection(t, data)
	loss := validationLoss(predictions(t, result, dataset.Validation))
	if selection.SelectedObjectiveValue == nil || *selection.SelectedObjectiveValue != loss {
		t.Errorf("selected objective value = %v, want the validation loss %v", selection.SelectedObjectiveValue, loss)
	}
}

func TestRunStopsEarlyAfterThePatience(t *testing.T) {
	cfg := selectionConfig()
	cfg.Checkpoint = bestValidation(trainingconfig.ObjectiveValidationLoss, 10)
	cfg.EarlyStopping = trainingconfig.EarlyStopping{Policy: trainingconfig.EarlyStoppingPatience, Patience: 5}

	selection := runSelection(t, read(t, selectionFixture), cfg).Report.Selection

	if selection.StopReason != trainingreport.StopEarlyStopping ||
		selection.ActualEpochs != selection.SelectedEpoch+5*10 ||
		selection.Evaluations != selection.ActualEpochs/10 {
		t.Errorf("selection = %+v, want a stop 5 evaluations after the selected epoch", selection)
	}
}

func TestRunSelectsTheThresholdOnValidation(t *testing.T) {
	data := read(t, selectionFixture)

	cfg := selectionConfig()
	cfg.Threshold = trainingconfig.Threshold{Policy: trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate, MaxFalsePositiveRate: 0.5}
	result := runSelection(t, data, cfg)

	want, err := evaluation.SelectThreshold(predictions(t, result, preparedSelection(t, data).Validation), 0.5)
	if err != nil {
		t.Fatal(err)
	}

	choice := result.Report.Selection.Threshold
	if choice.Value != want.Threshold || *choice.Candidates != want.Candidates || *choice.MaxFalsePositiveRate != 0.5 {
		t.Errorf("threshold = %+v, want %+v", choice, want)
	}

	if result.Report.Validation.Threshold != want.Threshold || result.Report.Test.Threshold != want.Threshold {
		t.Errorf("metrics thresholds = %v, %v", result.Report.Validation.Threshold, result.Report.Test.Threshold)
	}
}

// A validation partition whose highest scores tie a fraud with a
// legitimate payment cannot reach a false positive rate of 0.
func TestRunReportsAnInfeasibleThresholdPolicy(t *testing.T) {
	lines := strings.SplitAfter(string(read(t, selectionFixture)), "\n")
	fraud := lines[7]
	legitimate := strings.NewReplacer(`"label":"fraud"`, `"label":"legitimate"`,
		`"loss_amount_cents":240000`, `"loss_amount_cents":0`).Replace(fraud)
	data := []byte(strings.Join(append(append(lines[:6:6], fraud, legitimate), lines[10:]...), ""))

	cfg := selectionConfig()
	cfg.Threshold = trainingconfig.Threshold{Policy: trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate, MaxFalsePositiveRate: 0}

	_, err := Run(context.Background(), bytes.NewReader(data), cfg, DefaultLimits, nil)

	var infeasible *evaluation.InfeasibleThresholdError
	if !errors.As(err, &infeasible) {
		t.Fatalf("Run() error = %v, want an InfeasibleThresholdError", err)
	}

	if infeasible.LowestAchievableFalsePositiveRate != 1 {
		t.Errorf("lowest achievable rate = %v, want 1", infeasible.LowestAchievableFalsePositiveRate)
	}
}

func TestRunRequiresWhatValidationSelectionNeeds(t *testing.T) {
	data := read(t, selectionFixture)
	withoutLegitimate := []byte(strings.Join(func() []string {
		lines := strings.SplitAfter(string(data), "\n")
		return append(append(lines[:6:6], lines[7], lines[9]), lines[10:]...)
	}(), ""))

	tests := map[string]struct {
		data   []byte
		change func(*trainingconfig.Config)
	}{
		"best validation without validation records": {data, func(c *trainingconfig.Config) {
			c.ValidationEnd = c.TrainEnd.Add(time.Hour)
			c.Checkpoint = bestValidation(trainingconfig.ObjectiveValidationLoss, 10)
		}},
		"PR-AUC without validation fraud": {data, func(c *trainingconfig.Config) {
			c.ValidationEnd = time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
			c.Checkpoint = bestValidation(trainingconfig.ObjectiveValidationPRAUC, 10)
		}},
		"threshold selection without validation legitimate": {withoutLegitimate, func(c *trainingconfig.Config) {
			c.Threshold = trainingconfig.Threshold{Policy: trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate, MaxFalsePositiveRate: 0.5}
		}},
	}

	for name, test := range tests {
		cfg := selectionConfig()
		test.change(&cfg)

		_, err := Run(context.Background(), bytes.NewReader(test.data), cfg, DefaultLimits, func(int, float64) {
			t.Fatalf("%s: training started before the validation requirement was checked", name)
		})

		var requirement *ValidationRequirementError
		if !errors.As(err, &requirement) {
			t.Errorf("%s: Run() error = %v, want a ValidationRequirementError", name, err)
		}
	}

	// A fixed threshold and the final epoch need no validation records:
	// undefined metrics are null.
	cfg := selectionConfig()
	cfg.ValidationEnd = cfg.TrainEnd.Add(time.Hour)
	cfg.MaxEpochs = 10
	result := runSelection(t, data, cfg)
	if result.Report.Validation.Recall != nil || result.Report.Validation.TruePositives != 0 {
		t.Errorf("validation metrics of an empty partition = %+v", result.Report.Validation)
	}
}

// Test records never reach the preparation state, the checkpoint, early
// stopping, or the threshold.
func TestRunSelectsWithoutTheTestPartition(t *testing.T) {
	data := read(t, selectionFixture)

	cfg := selectionConfig()
	cfg.Checkpoint = bestValidation(trainingconfig.ObjectiveValidationPRAUC, 10)
	cfg.EarlyStopping = trainingconfig.EarlyStopping{Policy: trainingconfig.EarlyStoppingPatience, Patience: 20}
	cfg.Threshold = trainingconfig.Threshold{Policy: trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate, MaxFalsePositiveRate: 0.5}

	baseline := runSelection(t, data, cfg)

	changed := data
	for _, line := range []int{11, 13} {
		changed = replaceLine(t, changed, line, `"label":"legitimate"`, `"label":"fraud"`)
		changed = replaceLine(t, changed, line, `"initiated_outgoing_pix_24h":{"count":2`, `"initiated_outgoing_pix_24h":{"count":40`)
	}
	changed = replaceLine(t, changed, 12, `"label":"fraud"`, `"label":"legitimate"`)
	changed = replaceLine(t, changed, 12, `"loss_amount_cents":null`, `"loss_amount_cents":0`)

	other := runSelection(t, changed, cfg)

	if !reflect.DeepEqual(other.State, baseline.State) ||
		!reflect.DeepEqual(other.Report.Selection, baseline.Report.Selection) ||
		!reflect.DeepEqual(other.Report.Validation, baseline.Report.Validation) {
		t.Error("changing test records changed the preparation state, the selection, or the validation metrics")
	}

	if reflect.DeepEqual(other.Report.Test, baseline.Report.Test) {
		t.Error("the test metrics did not follow the changed test records")
	}
}

func TestRunCanSkipTheTestPartition(t *testing.T) {
	cfg := selectionConfig()
	cfg.TestEvaluation = trainingconfig.TestSkip

	report := runSelection(t, read(t, selectionFixture), cfg).Report

	if report.TestEvaluated || report.Test != nil {
		t.Errorf("test = %v %+v, want no test evaluation", report.TestEvaluated, report.Test)
	}

	if _, err := trainingreport.Marshal(report); err != nil {
		t.Errorf("the report does not marshal: %v", err)
	}
}

// Equal objective values keep the earliest checkpoint: with a learning rate
// too small to change any score, every evaluation ties.
func TestRunKeepsTheEarliestOfEqualCheckpoints(t *testing.T) {
	cfg := selectionConfig()
	cfg.LearningRate = 1e-300
	cfg.MaxEpochs = 50
	cfg.Checkpoint = bestValidation(trainingconfig.ObjectiveValidationLoss, 10)

	selection := runSelection(t, read(t, selectionFixture), cfg).Report.Selection
	if selection.SelectedEpoch != 10 || selection.Evaluations != 5 {
		t.Errorf("selection = %+v, want the first of five equal evaluations", selection)
	}
}

// The selected checkpoint is the best of every evaluation, lowest loss or
// highest PR-AUC, computed here by training to each evaluated epoch.
func TestRunSelectsTheBestEvaluation(t *testing.T) {
	data := read(t, selectionFixture)
	validation := preparedSelection(t, data).Validation

	measure := func(objective string, epochs int) float64 {
		cfg := selectionConfig()
		cfg.MaxEpochs = epochs
		p := predictions(t, runSelection(t, data, cfg), validation)

		if objective == trainingconfig.ObjectiveValidationLoss {
			return validationLoss(p)
		}
		metrics, err := evaluation.Evaluate(p, 0.5)
		if err != nil {
			t.Fatal(err)
		}
		return *metrics.PRAUC
	}

	// With seed 42, the validation PR-AUC rises from 0.5 to 0.75 at epoch 8
	// and to about 0.83 at epoch 10.
	tests := []struct {
		objective          string
		interval, maxEpoch int
	}{
		{trainingconfig.ObjectiveValidationLoss, 10, 205},
		{trainingconfig.ObjectiveValidationPRAUC, 10, 205},
		{trainingconfig.ObjectiveValidationPRAUC, 1, 12},
	}

	for _, test := range tests {
		cfg := selectionConfig()
		cfg.MaxEpochs = test.maxEpoch
		cfg.Checkpoint = bestValidation(test.objective, test.interval)
		selection := runSelection(t, data, cfg).Report.Selection

		var epochs []int
		for epoch := test.interval; epoch <= test.maxEpoch; epoch += test.interval {
			epochs = append(epochs, epoch)
		}
		if epochs[len(epochs)-1] != test.maxEpoch {
			epochs = append(epochs, test.maxEpoch)
		}

		bestEpoch, best := 0, 0.0
		for _, epoch := range epochs {
			value := measure(test.objective, epoch)
			better := value < best
			if test.objective == trainingconfig.ObjectiveValidationPRAUC {
				better = value > best
			}
			if bestEpoch == 0 || better {
				bestEpoch, best = epoch, value
			}
		}

		if selection.SelectedEpoch != bestEpoch || *selection.SelectedObjectiveValue != best || selection.Evaluations != len(epochs) {
			t.Errorf("%s every %d of %d epochs: selection = epoch %d value %v after %d evaluations, want epoch %d value %v after %d",
				test.objective, test.interval, test.maxEpoch, selection.SelectedEpoch, *selection.SelectedObjectiveValue,
				selection.Evaluations, bestEpoch, best, len(epochs))
		}
	}
}
