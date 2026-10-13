package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/nn"
)

const (
	fixture        = "../../training/testdata/dataset.jsonl"
	invalidDataset = "../../docs/contracts/training-examples/dataset.jsonl"
)

type result struct {
	code   int
	stdout string
	stderr string
}

func train(t *testing.T, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)

	return result{code, stdout.String(), stderr.String()}
}

// flags returns a complete, valid set of flags that writes into dir.
func flags(dir string) map[string]string {
	return map[string]string{
		"dataset":        fixture,
		"model-output":   filepath.Join(dir, "trained-model.json"),
		"report-output":  filepath.Join(dir, "training-report.json"),
		"train-end":      "2025-12-31T23:59:59Z",
		"validation-end": "2026-02-28T23:59:59Z",
		"seed":           "42",
		"epochs":         "5000",
		"learning-rate":  "1.0",
		"threshold":      "0.5",
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

	reportSHA256 := fileSHA256(t, f["report-output"])
	want := trainedSummary{
		Status:        "trained",
		DatasetSHA256: fileSHA256(t, fixture),
		Records:       records{Total: 12, Usable: 10, Insufficient: 1, Immature: 1},
		ModelSHA256:   fileSHA256(t, f["model-output"]),
		ReportSHA256:  reportSHA256,
	}
	if summary != want {
		t.Errorf("summary = %+v\nwant      %+v", summary, want)
	}

	network, provenance, err := nn.LoadWithProvenance(f["model-output"], "transaction-features-v1")
	if err != nil {
		t.Fatalf("load trained model: %v", err)
	}

	switch {
	case provenance.ReportSHA256 != reportSHA256:
		t.Errorf("model report_sha256 = %s, want %s", provenance.ReportSHA256, reportSHA256)
	case provenance.DatasetSHA256 != want.DatasetSHA256:
		t.Errorf("model dataset_sha256 = %s, want %s", provenance.DatasetSHA256, want.DatasetSHA256)
	case provenance.Seed != 42 || provenance.Epochs != 5000 || provenance.LearningRate != 1 || provenance.Threshold != 0.5:
		t.Errorf("model configuration = %+v", provenance)
	case provenance.TrainEnd.Format("2006-01-02T15:04:05Z07:00") != "2025-12-31T23:59:59Z":
		t.Errorf("model train_end = %s", provenance.TrainEnd)
	}

	// cmd/predict loads models with nn.Load.
	if _, err := nn.Load(f["model-output"], "transaction-features-v1"); err != nil {
		t.Errorf("nn.Load() of the trained model: %v", err)
	}

	outputs, err := network.Predict([]float64{0.9, 0.45, 1, 1})
	if err != nil || !(outputs[0] >= 0.5) {
		t.Errorf("trained model scores a fraud-like payment %v (%v)", outputs, err)
	}

	var report struct {
		Version    string `json:"training_report_version"`
		Validation struct {
			TruePositives int `json:"true_positives"`
		} `json:"validation"`
		Test struct {
			TrueNegatives int `json:"true_negatives"`
		} `json:"test"`
	}
	data, err := os.ReadFile(f["report-output"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != "pix-training-report-v1" || report.Validation.TruePositives != 1 || report.Test.TrueNegatives != 1 {
		t.Errorf("report = %+v", report)
	}

	if !strings.Contains(r.stderr, "training loss") {
		t.Errorf("stderr has no training loss progress:\n%s", r.stderr)
	}

	if got := entries(t, dir); len(got) != 2 {
		t.Errorf("output directory = %v, want only the model and the report", got)
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

	for _, name := range []string{"model-output", "report-output"} {
		if fileSHA256(t, first[name]) != fileSHA256(t, second[name]) {
			t.Errorf("%s differs between identical runs", name)
		}
	}

	if firstRun.stdout != secondRun.stdout {
		t.Errorf("stdout differs: %q, %q", firstRun.stdout, secondRun.stdout)
	}

	// Running again over existing artifacts replaces them with the same bytes.
	model := fileSHA256(t, first["model-output"])
	if again := train(t, args(first)...); again.code != 0 || again.stdout != firstRun.stdout {
		t.Errorf("rerun: exit code %d, stdout %q", again.code, again.stdout)
	}
	if fileSHA256(t, first["model-output"]) != model {
		t.Error("rerun changed the model")
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
	case invalid.Dataset.DatasetReportVersion != "pix-training-dataset-report-v1":
		t.Errorf("dataset report version = %q", invalid.Dataset.DatasetReportVersion)
	case invalid.Dataset.Records != records{Total: 7, Usable: 3, Invalid: 2, Insufficient: 1, Immature: 1}:
		t.Errorf("records = %+v", invalid.Dataset.Records)
	case len(invalid.Dataset.Exclusions) != 4:
		t.Errorf("exclusions = %s", invalid.Dataset.Exclusions)
	case invalid.Dataset.DatasetSHA256 != fileSHA256(t, invalidDataset):
		t.Errorf("dataset_sha256 = %s", invalid.Dataset.DatasetSHA256)
	}

	for _, want := range []string{"line 4: /outcome/label: invalid_enum_value", "line 6: /scoring_input/pix_payment/amount_cents"} {
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
	f["train-end"] = "2025-09-10T00:00:00Z"

	r := train(t, args(f)...)

	switch {
	case r.code != 1:
		t.Errorf("exit code = %d, want 1", r.code)
	case r.stdout != "":
		t.Errorf("stdout = %q, want empty", r.stdout)
	case !strings.Contains(r.stderr, "0 usable fraud and 1 usable legitimate") || !strings.Contains(r.stderr, "-train-end"):
		t.Errorf("stderr is not actionable:\n%s", r.stderr)
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
		"date-only train-end": func(f map[string]string) []string {
			f["train-end"] = "2025-12-31"
			return args(f)
		},
		"lowercase timestamp": func(f map[string]string) []string {
			f["validation-end"] = "2026-02-28t23:59:59z"
			return args(f)
		},
		"unknown local offset": func(f map[string]string) []string {
			f["validation-end"] = "2026-02-28T23:59:59-00:00"
			return args(f)
		},
		"reversed boundaries": func(f map[string]string) []string {
			f["train-end"], f["validation-end"] = f["validation-end"], f["train-end"]
			return args(f)
		},
		"zero epochs": func(f map[string]string) []string {
			f["epochs"] = "0"
			return args(f)
		},
		"fractional seed": func(f map[string]string) []string {
			f["seed"] = "4.2"
			return args(f)
		},
		"NaN learning rate": func(f map[string]string) []string {
			f["learning-rate"] = "NaN"
			return args(f)
		},
		"threshold above 1": func(f map[string]string) []string {
			f["threshold"] = "1.5"
			return args(f)
		},
		"same output paths": func(f map[string]string) []string {
			f["report-output"] = f["model-output"]
			return args(f)
		},
		"missing dataset": func(f map[string]string) []string {
			f["dataset"] = filepath.Join(filepath.Dir(f["model-output"]), "absent.jsonl")
			return args(f)
		},
		"unknown flag": func(f map[string]string) []string {
			return append(args(f), "--shuffle", "true")
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

func TestTrainNeverOverwritesTheDataset(t *testing.T) {
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	for _, output := range []string{"model-output", "report-output"} {
		dataset := filepath.Join(t.TempDir(), "dataset.jsonl")
		if err := os.WriteFile(dataset, data, 0o644); err != nil {
			t.Fatal(err)
		}

		f := flags(t.TempDir())
		f["dataset"] = dataset
		f[output] = dataset

		if r := train(t, args(f)...); r.code != 1 || r.stdout != "" {
			t.Errorf("-%s is the dataset: exit code %d, stdout %q", output, r.code, r.stdout)
		}

		if after, err := os.ReadFile(dataset); err != nil || !bytes.Equal(after, data) {
			t.Errorf("-%s is the dataset: the dataset was overwritten", output)
		}
	}
}

// A failed write leaves neither artifact nor temporary file behind.
func TestTrainWritesArtifactsAtomically(t *testing.T) {
	dir := t.TempDir()
	f := flags(dir)
	f["model-output"] = filepath.Join(dir, "missing", "trained-model.json")

	r := train(t, args(f)...)

	if r.code != 1 || r.stdout != "" {
		t.Errorf("exit code %d, stdout %q, want 1 and empty", r.code, r.stdout)
	}

	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("output directory = %v, want no artifact and no temporary file", got)
	}
}

func TestTrainHelp(t *testing.T) {
	r := train(t, "-h")

	if r.code != 0 || r.stdout != "" || !strings.Contains(r.stderr, "-validation-end") {
		t.Errorf("exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
}
