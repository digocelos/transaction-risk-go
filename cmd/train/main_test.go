package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/trainingrun"
)

const (
	fixture        = "../../training/testdata/pix-risk-v1.jsonl"
	invalidDataset = "../../docs/contracts/training-examples-v2/dataset.jsonl"
	baseline       = "../../docs/contracts/training-config-examples/01-baseline.json"
)

type result struct {
	code   int
	stdout string
	stderr string
}

func trainWith(t *testing.T, ctx context.Context, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(ctx, args, &stdout, &stderr)

	return result{code, stdout.String(), stderr.String()}
}

func train(t *testing.T, args ...string) result {
	t.Helper()
	return trainWith(t, context.Background(), args...)
}

// flags returns a complete, valid set of flags that writes into dir.
func flags(dir string) map[string]string {
	return map[string]string{
		"config":  baseline,
		"dataset": fixture,
		"run-dir": filepath.Join(dir, "run"),
	}
}

func args(flags map[string]string) []string {
	var args []string
	for name, value := range flags {
		args = append(args, "--"+name, value)
	}
	return args
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeConfig writes the baseline configuration with one replacement into
// dir and returns its path.
func writeConfig(t *testing.T, dir, old, new string) string {
	t.Helper()

	data, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Count(string(data), old) != 1 {
		t.Fatalf("%q must occur exactly once in the baseline configuration", old)
	}

	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

// output parses stdout, which must hold exactly one compact JSON object
// followed by a newline.
func output(t *testing.T, stdout string, value any) {
	t.Helper()

	if !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout = %q, want one compact JSON line", stdout)
	}

	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(value); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
}

type records struct {
	Total        int `json:"total"`
	Usable       int `json:"usable"`
	Invalid      int `json:"invalid"`
	Insufficient int `json:"insufficient"`
	Immature     int `json:"immature"`
}

type trainedSummary struct {
	Status        string  `json:"status"`
	DatasetSHA256 string  `json:"dataset_sha256"`
	Records       records `json:"records"`
	ModelSHA256   string  `json:"model_sha256"`
	ReportSHA256  string  `json:"report_sha256"`
	ConfigSHA256  string  `json:"config_sha256"`
}

// runFile returns the path of a file of the run in f.
func runFile(f map[string]string, name string) string {
	return filepath.Join(f["run-dir"], name)
}

// entries lists the names in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()

	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, entry := range list {
		names = append(names, entry.Name())
	}
	return names
}

func TestTrainWritesTraceableArtifacts(t *testing.T) {
	dir := t.TempDir()
	f := flags(dir)

	r := train(t, args(f)...)
	if r.code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", r.code, r.stderr)
	}

	var summary trainedSummary
	output(t, r.stdout, &summary)

	reportSHA256 := fileSHA256(t, runFile(f, "report.json"))
	want := trainedSummary{
		Status:        "trained",
		DatasetSHA256: fileSHA256(t, fixture),
		Records:       records{Total: 8, Usable: 6, Insufficient: 1, Immature: 1},
		ModelSHA256:   fileSHA256(t, runFile(f, "model.json")),
		ReportSHA256:  reportSHA256,
		ConfigSHA256:  fileSHA256(t, runFile(f, "config.json")),
	}
	if summary != want {
		t.Errorf("summary = %+v\nwant      %+v", summary, want)
	}

	run, err := trainingrun.Verify(f["run-dir"])
	if err != nil {
		t.Fatalf("the published run does not verify: %v", err)
	}

	artifact := run.Artifact
	switch {
	case artifact.Training.ReportSHA256 != reportSHA256:
		t.Errorf("model report_sha256 = %s, want %s", artifact.Training.ReportSHA256, reportSHA256)
	case artifact.Training.DatasetSHA256 != want.DatasetSHA256:
		t.Errorf("model dataset_sha256 = %s", artifact.Training.DatasetSHA256)
	case artifact.SelectedEpoch != 5000 || artifact.Threshold.Value != 0.5 || artifact.Training.ConfigSHA256 != want.ConfigSHA256:
		t.Errorf("model selection = %d %+v", artifact.SelectedEpoch, artifact.Threshold)
	case run.Config.Seed != 42 || run.Config.LearningRate != 1:
		t.Errorf("configuration = %+v", run.Config)
	}

	if _, err := artifact.Network.Predict(make([]float64, 62)); err != nil {
		t.Errorf("the trained network does not take the 62 pix-risk-v1 features: %v", err)
	}

	if !strings.Contains(r.stderr, "selected epoch 5000 of 5000 trained") {
		t.Errorf("stderr does not describe the selection:\n%s", r.stderr)
	}

	if !strings.Contains(r.stderr, "training loss") {
		t.Errorf("stderr has no training loss progress:\n%s", r.stderr)
	}

	if got := entries(t, dir); len(got) != 1 || len(entries(t, f["run-dir"])) != 4 {
		t.Errorf("output directory = %v, want only the run with its four files", got)
	}
}

func TestTrainIsReproducible(t *testing.T) {
	first := flags(t.TempDir())
	second := flags(t.TempDir())

	firstRun := train(t, args(first)...)
	secondRun := train(t, args(second)...)
	if firstRun.code != 0 || secondRun.code != 0 {
		t.Fatalf("exit codes = %d, %d", firstRun.code, secondRun.code)
	}

	for _, name := range []string{"model.json", "report.json", "config.json", "manifest.json"} {
		if fileSHA256(t, runFile(first, name)) != fileSHA256(t, runFile(second, name)) {
			t.Errorf("%s differs between identical runs", name)
		}
	}

	if firstRun.stdout != secondRun.stdout {
		t.Errorf("stdout differs: %q, %q", firstRun.stdout, secondRun.stdout)
	}
}

func TestTrainStopsOnInvalidDataset(t *testing.T) {
	dir := t.TempDir()
	f := flags(dir)
	f["dataset"] = invalidDataset

	r := train(t, args(f)...)
	if r.code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr:\n%s", r.code, r.stderr)
	}

	var invalid struct {
		Status  string `json:"status"`
		Dataset struct {
			DatasetReportVersion   string            `json:"dataset_report_version"`
			TrainingExampleVersion string            `json:"training_example_version"`
			FeatureSchema          string            `json:"feature_schema"`
			LabelMaturityDays      int               `json:"label_maturity_days"`
			DatasetSHA256          string            `json:"dataset_sha256"`
			DatasetBytes           int64             `json:"dataset_bytes"`
			Records                records           `json:"records"`
			UsableLabels           map[string]int    `json:"usable_labels"`
			Exclusions             []json.RawMessage `json:"exclusions"`
		} `json:"dataset"`
	}
	output(t, r.stdout, &invalid)

	switch {
	case invalid.Status != "invalid_dataset":
		t.Errorf("status = %q", invalid.Status)
	case invalid.Dataset.FeatureSchema != "pix-risk-v1" || invalid.Dataset.TrainingExampleVersion != "pix-training-example-v2":
		t.Errorf("dataset report = %+v", invalid.Dataset)
	case invalid.Dataset.Records != records{Total: 10, Usable: 6, Invalid: 2, Insufficient: 1, Immature: 1}:
		t.Errorf("records = %+v", invalid.Dataset.Records)
	}

	for _, want := range []string{"line 7: /outcome/label: invalid_enum_value", "line 8: /scoring_input/pix_payment/reference_time_offset"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr does not report %q:\n%s", want, r.stderr)
		}
	}

	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("output directory = %v, want no artifact", got)
	}
}

func TestTrainRejectsTrainPartitionWithOneClass(t *testing.T) {
	dir := t.TempDir()
	f := flags(dir)
	f["config"] = writeConfig(t, t.TempDir(), `"train_end": "2026-06-05T00:00:00Z"`, `"train_end": "2026-05-31T00:00:00Z"`)

	r := train(t, args(f)...)

	switch {
	case r.code != 1:
		t.Errorf("exit code = %d, want 1", r.code)
	case r.stdout != "":
		t.Errorf("stdout = %q, want empty", r.stdout)
	case !strings.Contains(r.stderr, "0 usable fraud and 3 usable legitimate") || !strings.Contains(r.stderr, "train_end"):
		t.Errorf("stderr is not actionable:\n%s", r.stderr)
	}

	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("output directory = %v, want no artifact", got)
	}
}

func TestTrainRejectsInvalidConfigurations(t *testing.T) {
	dir := t.TempDir()
	f := flags(dir)
	f["config"] = "../../docs/contracts/training-config-examples/11-invalid-dimensions-and-ranges.json"

	r := train(t, args(f)...)

	if r.code != 1 || r.stdout != "" {
		t.Errorf("exit code %d, stdout %q, want 1 and empty", r.code, r.stdout)
	}

	for _, want := range []string{"/learning_rate: out_of_range", "/max_epochs: out_of_range", "/architecture/hidden_layers/1: out_of_range"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr does not report %q:\n%s", want, r.stderr)
		}
	}

	if strings.Contains(r.stderr, "epoch 0") {
		t.Errorf("an invalid configuration must stop before training:\n%s", r.stderr)
	}

	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("output directory = %v, want no artifact", got)
	}
}

func TestTrainRequiresEveryFlag(t *testing.T) {
	for name := range flags("") {
		dir := t.TempDir()
		f := flags(dir)
		delete(f, name)

		r := train(t, args(f)...)

		if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "-"+name) {
			t.Errorf("without -%s: exit code %d, stdout %q, stderr:\n%s", name, r.code, r.stdout, r.stderr)
		}

		if got := entries(t, dir); len(got) != 0 {
			t.Errorf("without -%s: output directory = %v, want no artifact", name, got)
		}
	}
}

func TestTrainRejectsInvalidArguments(t *testing.T) {
	tests := map[string]func(f map[string]string) []string{
		"missing dataset": func(f map[string]string) []string {
			f["dataset"] = filepath.Join(filepath.Dir(f["run-dir"]), "absent.jsonl")
			return args(f)
		},
		"missing configuration": func(f map[string]string) []string {
			f["config"] = filepath.Join(filepath.Dir(f["run-dir"]), "absent.json")
			return args(f)
		},
		"removed output flags": func(f map[string]string) []string {
			return append(args(f), "--model-output", filepath.Join(filepath.Dir(f["run-dir"]), "model.json"))
		},
		"version 1 flags": func(f map[string]string) []string {
			return append(args(f), "--seed", "42")
		},
		"positional argument": func(f map[string]string) []string {
			return append(args(f), "extra")
		},
	}

	for name, build := range tests {
		dir := t.TempDir()

		r := train(t, build(flags(dir))...)

		if r.code != 1 || r.stdout != "" || r.stderr == "" {
			t.Errorf("%s: exit code %d, stdout %q, stderr %q", name, r.code, r.stdout, r.stderr)
		}

		if got := entries(t, dir); len(got) != 0 {
			t.Errorf("%s: output directory = %v, want no artifact", name, got)
		}
	}
}

// A run directory is never written over an existing file or directory,
// such as the dataset, the configuration, or an earlier run.
func TestTrainNeverOverwritesExistingPaths(t *testing.T) {
	for _, input := range []string{"dataset", "config"} {
		f := flags(t.TempDir())

		data, err := os.ReadFile(f[input])
		if err != nil {
			t.Fatal(err)
		}

		copied := filepath.Join(t.TempDir(), input)
		if err := os.WriteFile(copied, data, 0o644); err != nil {
			t.Fatal(err)
		}

		f[input] = copied
		f["run-dir"] = copied

		if r := train(t, args(f)...); r.code != 1 || r.stdout != "" {
			t.Errorf("-run-dir is the %s: exit code %d, stdout %q", input, r.code, r.stdout)
		}

		if after, err := os.ReadFile(copied); err != nil || !bytes.Equal(after, data) {
			t.Errorf("-run-dir is the %s: the %s was overwritten", input, input)
		}
	}

	f := flags(t.TempDir())
	if r := train(t, args(f)...); r.code != 0 {
		t.Fatalf("first run: exit code %d", r.code)
	}
	model := fileSHA256(t, runFile(f, "model.json"))

	f["config"] = writeConfig(t, t.TempDir(), `"seed": 42`, `"seed": 7`)
	if r := train(t, args(f)...); r.code != 1 || !strings.Contains(r.stderr, "already exists") {
		t.Errorf("second run into the same directory: exit code %d, stderr:\n%s", r.code, r.stderr)
	}
	if fileSHA256(t, runFile(f, "model.json")) != model {
		t.Error("a second run changed an existing run")
	}
}

// A failed publication leaves neither a run nor a staging directory.
func TestTrainWritesArtifactsAtomically(t *testing.T) {
	dir := t.TempDir()
	f := flags(dir)
	f["run-dir"] = filepath.Join(dir, "missing", "run")

	r := train(t, args(f)...)

	if r.code != 1 || r.stdout != "" {
		t.Errorf("exit code %d, stdout %q, want 1 and empty", r.code, r.stdout)
	}

	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("output directory = %v, want no artifact and no temporary file", got)
	}
}

func TestTrainStopsWhenCanceled(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := trainWith(t, ctx, args(flags(dir))...)

	if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "canceled") {
		t.Errorf("exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}

	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("output directory = %v, want no artifact", got)
	}
}

func TestTrainHelp(t *testing.T) {
	r := train(t, "-h")

	if r.code != 0 || r.stdout != "" || !strings.Contains(r.stderr, "-config") {
		t.Errorf("exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
}

const selectionDataset = "../../training/testdata/pix-risk-v1-selection.jsonl"

// writeFile writes data into a new temporary directory and returns its path.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTrainSelectsOnValidation(t *testing.T) {
	dir := t.TempDir()
	f := flags(dir)
	f["dataset"] = selectionDataset
	f["config"] = writeConfig(t, t.TempDir(), `"train_end": "2026-06-05T00:00:00Z",
    "validation_end": "2026-06-12T00:00:00Z"`, `"train_end": "2026-05-31T00:00:00Z",
    "validation_end": "2026-06-15T00:00:00Z"`)

	data, err := os.ReadFile(f["config"])
	if err != nil {
		t.Fatal(err)
	}
	selection := strings.Replace(string(data), `"checkpoint": {
      "policy": "final_epoch"
    },`, `"checkpoint": {"policy": "best_validation", "objective": "validation_loss", "evaluation_interval": 10, "min_improvement": 0},`, 1)
	selection = strings.Replace(selection, `"early_stopping": {
      "policy": "none"
    },`, `"early_stopping": {"policy": "patience", "patience": 5},`, 1)
	f["config"] = writeFile(t, "config.json", []byte(selection))

	r := train(t, args(f)...)
	if r.code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", r.code, r.stderr)
	}

	var report struct {
		Selection struct {
			ActualEpochs  int    `json:"actual_epochs"`
			SelectedEpoch int    `json:"selected_epoch"`
			StopReason    string `json:"stop_reason"`
		} `json:"selection"`
	}
	data, err = os.ReadFile(runFile(f, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}

	if report.Selection.StopReason != "early_stopping" || report.Selection.ActualEpochs != report.Selection.SelectedEpoch+50 {
		t.Errorf("selection = %+v", report.Selection)
	}

	artifact, err := modelartifact.Load(runFile(f, "model.json"))
	if err != nil {
		t.Fatal(err)
	}
	if artifact.SelectedEpoch != report.Selection.SelectedEpoch {
		t.Errorf("model selected epoch = %d, want %d", artifact.SelectedEpoch, report.Selection.SelectedEpoch)
	}
}

// A threshold policy that no candidate satisfies writes nothing and
// reports why on standard output.
func TestTrainReportsAnInfeasibleThresholdPolicy(t *testing.T) {
	data, err := os.ReadFile(selectionDataset)
	if err != nil {
		t.Fatal(err)
	}

	// The two validation records have the same scoring input and opposite
	// labels, so every candidate flags the legitimate one.
	lines := strings.SplitAfter(string(data), "\n")
	fraud := lines[7]
	legitimate := strings.NewReplacer(`"label":"fraud"`, `"label":"legitimate"`,
		`"loss_amount_cents":240000`, `"loss_amount_cents":0`).Replace(fraud)

	dir := t.TempDir()
	f := flags(dir)
	f["dataset"] = writeFile(t, "dataset.jsonl", []byte(strings.Join(append(append(lines[:6:6], fraud, legitimate), lines[10:]...), "")))

	config, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	selection := strings.NewReplacer(
		`"train_end": "2026-06-05T00:00:00Z"`, `"train_end": "2026-05-31T00:00:00Z"`,
		`"validation_end": "2026-06-12T00:00:00Z"`, `"validation_end": "2026-06-15T00:00:00Z"`,
		`"policy": "fixed",
      "value": 0.5`, `"policy": "max_recall_at_max_false_positive_rate",
      "max_false_positive_rate": 0`,
	).Replace(string(config))
	f["config"] = writeFile(t, "config.json", []byte(selection))

	r := train(t, args(f)...)
	if r.code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr:\n%s", r.code, r.stderr)
	}

	var infeasible struct {
		Status                            string  `json:"status"`
		ThresholdPolicy                   string  `json:"threshold_policy"`
		MaxFalsePositiveRate              float64 `json:"max_false_positive_rate"`
		LowestAchievableFalsePositiveRate float64 `json:"lowest_achievable_false_positive_rate"`
	}
	output(t, r.stdout, &infeasible)

	if infeasible.Status != "selection_infeasible" || infeasible.ThresholdPolicy != "max_recall_at_max_false_positive_rate" ||
		infeasible.MaxFalsePositiveRate != 0 || infeasible.LowestAchievableFalsePositiveRate != 1 {
		t.Errorf("output = %+v", infeasible)
	}

	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("output directory = %v, want no artifact", got)
	}
}

func TestTrainRejectsAValidationPartitionTheSelectionCannotUse(t *testing.T) {
	dir := t.TempDir()
	f := flags(dir)

	// The baseline boundaries leave one fraud and no legitimate record in
	// the fixture's validation partition.
	f["config"] = writeConfig(t, t.TempDir(), `"policy": "fixed",
      "value": 0.5`, `"policy": "max_recall_at_max_false_positive_rate",
      "max_false_positive_rate": 0.1`)

	r := train(t, args(f)...)

	if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "validation partition has 1 usable fraud and 0 usable legitimate") {
		t.Errorf("exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}

	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("output directory = %v, want no artifact", got)
	}
}
