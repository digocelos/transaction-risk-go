package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
	"github.com/digocelos/transaction-risk-go/trainingrun"
)

const (
	// selectionFixture has six train records (lines 1–6), two legitimate
	// and two fraud validation records (lines 7–10), and two legitimate
	// test records and a test fraud with an unknown loss (lines 11–13),
	// with the boundaries of the trained model.
	selectionFixture = "../../training/testdata/pix-risk-v1-selection.jsonl"
	invalidDataset   = "../../docs/contracts/training-examples-v2/dataset.jsonl"
	baseline         = "../../docs/contracts/training-config-examples/01-baseline.json"
)

type result struct {
	code   int
	stdout string
	stderr string
}

func inspectWith(t *testing.T, ctx context.Context, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(ctx, args, &stdout, &stderr)
	return result{code, stdout.String(), stderr.String()}
}

func inspect(t *testing.T, args ...string) result {
	t.Helper()
	return inspectWith(t, context.Background(), args...)
}

func read(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// publishRun trains a model on the selection fixture, with train_end
// 2026-05-31 and validation_end 2026-06-15, and publishes its run into
// dir/run.
func publishRun(t *testing.T, dir string) string {
	t.Helper()

	cfg, err := trainingconfig.Decode(read(t, baseline))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs = 300
	cfg.TrainEnd = time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	cfg.ValidationEnd = time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	result, err := training.Run(context.Background(), bytes.NewReader(read(t, selectionFixture)), cfg, training.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := modelartifact.FromTraining(result.Network, result.State, result.Report,
		digest(report), modelartifact.SoftwareFromBuild())
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

	runDir := filepath.Join(dir, "run")
	if err := trainingrun.Publish(runDir, trainingrun.Bundle{Model: model, Report: report, Config: config}); err != nil {
		t.Fatal(err)
	}
	return runDir
}

// setup publishes a run into a temporary directory and returns that
// directory and a complete set of flags that inspects it into
// dir/report.json.
func setup(t *testing.T) (string, map[string]string) {
	t.Helper()

	dir := t.TempDir()
	return dir, map[string]string{
		"run":     publishRun(t, dir),
		"dataset": selectionFixture,
		"output":  filepath.Join(dir, "report.json"),
	}
}

func args(flags map[string]string) []string {
	var args []string
	for name, value := range flags {
		args = append(args, "-"+name, value)
	}
	return args
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

type inspectedSummary struct {
	Status              string   `json:"status"`
	ReportSHA256        string   `json:"report_sha256"`
	ModelSHA256         string   `json:"model_sha256"`
	DatasetSHA256       string   `json:"dataset_sha256"`
	UnexercisedFeatures []string `json:"unexercised_features"`
	DirectionViolations int      `json:"direction_violations"`
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

func runFiles(t *testing.T, runDir string) map[string]string {
	t.Helper()

	files := map[string]string{}
	for _, name := range entries(t, runDir) {
		files[name] = digest(read(t, filepath.Join(runDir, name)))
	}
	return files
}

// An inspection writes the report and summarizes it on stdout with exit
// code 0. The run is not changed, and a second inspection writes the same
// bytes.
func TestInspectWritesAReport(t *testing.T) {
	dir, f := setup(t)
	before := runFiles(t, f["run"])

	r := inspect(t, args(f)...)
	if r.code != exitInspected {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", r.code, exitInspected, r.stderr)
	}

	var summary inspectedSummary
	output(t, r.stdout, &summary)

	report := read(t, f["output"])
	if summary.Status != "inspected" || summary.ReportSHA256 != digest(report) ||
		summary.ModelSHA256 != before["model.json"] || summary.DatasetSHA256 != digest(read(t, selectionFixture)) ||
		!slices.Contains(summary.UnexercisedFeatures, "limit_zero") || summary.DirectionViolations < 0 {
		t.Errorf("summary = %+v", summary)
	}
	if !strings.HasPrefix(r.stdout, `{"status":"inspected","report_sha256":`) {
		t.Errorf("stdout = %s, want status and report_sha256 first", r.stdout)
	}
	if !strings.Contains(r.stderr, "no train record exercised") || !strings.Contains(r.stderr, "inspect: wrote ") {
		t.Errorf("stderr:\n%s", r.stderr)
	}

	var written struct {
		Version string `json:"inspection_report_version"`
		Model   struct {
			ArtifactSHA256 string `json:"artifact_sha256"`
		} `json:"model"`
		Unexercised []string `json:"unexercised_features"`
	}
	if err := json.Unmarshal(report, &written); err != nil {
		t.Fatal(err)
	}
	if written.Version != "pix-model-inspection-v1" || written.Model.ArtifactSHA256 != summary.ModelSHA256 ||
		!reflect.DeepEqual(written.Unexercised, summary.UnexercisedFeatures) {
		t.Errorf("report = %+v", written)
	}

	if after := runFiles(t, f["run"]); !reflect.DeepEqual(after, before) {
		t.Error("inspection changed the run")
	}
	if got := entries(t, dir); !reflect.DeepEqual(got, []string{"report.json", "run"}) {
		t.Errorf("directory holds %v, want only the report and the run", got)
	}

	again := filepath.Join(dir, "again.json")
	f["output"] = again
	if r := inspect(t, args(f)...); r.code != exitInspected || digest(read(t, again)) != summary.ReportSHA256 {
		t.Errorf("a second inspection wrote another report; exit code %d", r.code)
	}
}

// A dataset with invalid records is reported with exit code 2 and its
// diagnostics, and no report is written.
func TestInspectStopsOnInvalidDataset(t *testing.T) {
	dir, f := setup(t)
	f["dataset"] = invalidDataset

	r := inspect(t, args(f)...)
	if r.code != exitInvalidDataset {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", r.code, exitInvalidDataset, r.stderr)
	}

	var invalid struct {
		Status  string `json:"status"`
		Dataset struct {
			DatasetSHA256 string `json:"dataset_sha256"`
			Records       struct {
				Invalid int `json:"invalid"`
			} `json:"records"`
		} `json:"dataset"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &invalid); err != nil {
		t.Fatalf("stdout %q: %v", r.stdout, err)
	}
	if invalid.Status != "invalid_dataset" || invalid.Dataset.Records.Invalid != 2 ||
		invalid.Dataset.DatasetSHA256 != digest(read(t, invalidDataset)) {
		t.Errorf("stdout = %s", r.stdout)
	}
	if !strings.Contains(r.stderr, "inspect: line 7: /outcome/label: invalid_enum_value:") {
		t.Errorf("stderr does not list the diagnostics:\n%s", r.stderr)
	}

	if got := entries(t, dir); !reflect.DeepEqual(got, []string{"run"}) {
		t.Errorf("directory holds %v, want no report", got)
	}
}

// Another dataset, a tampered run, missing flags, an output that is an
// input, and an existing output without -replace fail with exit code 1,
// nothing on stdout, and nothing written.
func TestInspectRejectsInvalidArguments(t *testing.T) {
	dir, f := setup(t)

	other := filepath.Join(dir, "other.jsonl")
	data := read(t, selectionFixture)
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if err := os.WriteFile(other, slices.Concat(data, lines[0], []byte("\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	tampered := filepath.Join(dir, "tampered")
	if err := os.CopyFS(tampered, os.DirFS(f["run"])); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(tampered, "config.json")
	if err := os.WriteFile(config, bytes.Replace(read(t, config), []byte(`"seed":42`), []byte(`"seed":43`), 1), 0o644); err != nil {
		t.Fatal(err)
	}

	existing := filepath.Join(dir, "existing.json")
	if err := os.WriteFile(existing, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		args []string
		want string
	}{
		"another dataset": {[]string{"-run", f["run"], "-dataset", other, "-output", f["output"]}, "not the model's training dataset"},
		"tampered run":    {[]string{"-run", tampered, "-dataset", selectionFixture, "-output", f["output"]}, "config.json"},
		"missing run":     {[]string{"-dataset", selectionFixture, "-output", f["output"]}, "missing required flags: -run"},
		"missing all":     {nil, "missing required flags: -run, -dataset, -output"},
		"positional":      {[]string{"-run", f["run"], "-dataset", selectionFixture, "-output", f["output"], "extra"}, `unexpected argument "extra"`},
		"output is the dataset": {[]string{"-run", f["run"], "-dataset", existing, "-output", existing},
			"-output must not be the dataset"},
		"output is the model": {[]string{"-run", f["run"], "-dataset", selectionFixture, "-output", filepath.Join(f["run"], "model.json")},
			"-output must not be the run's model.json"},
		"existing output": {[]string{"-run", f["run"], "-dataset", selectionFixture, "-output", existing},
			"already exists; inspect with -replace"},
		"missing output directory": {[]string{"-run", f["run"], "-dataset", selectionFixture, "-output", filepath.Join(dir, "none", "r.json")},
			"does not exist"},
		"missing dataset file": {[]string{"-run", f["run"], "-dataset", filepath.Join(dir, "none.jsonl"), "-output", f["output"]},
			"no such file"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := inspect(t, c.args...)
			if r.code != exitFailure || r.stdout != "" || !strings.Contains(r.stderr, c.want) {
				t.Errorf("exit code %d, stdout %q, stderr:\n%s\nwant exit 1 and %q", r.code, r.stdout, r.stderr, c.want)
			}
		})
	}

	if _, err := os.Stat(f["output"]); err == nil {
		t.Error("a failure wrote the report")
	}
	if got := read(t, existing); string(got) != "keep\n" {
		t.Error("a failure changed an existing output")
	}
	if got := entries(t, f["run"]); !reflect.DeepEqual(got, []string{"config.json", "manifest.json", "model.json", "report.json"}) {
		t.Errorf("the run holds %v", got)
	}
}

// -replace overwrites an existing output atomically.
func TestInspectReplacesWithReplace(t *testing.T) {
	_, f := setup(t)
	if err := os.WriteFile(f["output"], []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := inspect(t, append(args(f), "-replace")...)
	if r.code != exitInspected {
		t.Fatalf("exit code = %d; stderr:\n%s", r.code, r.stderr)
	}

	var summary inspectedSummary
	output(t, r.stdout, &summary)
	if digest(read(t, f["output"])) != summary.ReportSHA256 {
		t.Error("the output was not replaced by the report")
	}
}

// A canceled context writes nothing and fails.
func TestInspectStopsWhenCanceled(t *testing.T) {
	dir, f := setup(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := inspectWith(t, ctx, args(f)...)
	if r.code != exitFailure || r.stdout != "" || !strings.Contains(r.stderr, "canceled; nothing was written") {
		t.Errorf("exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if got := entries(t, dir); !reflect.DeepEqual(got, []string{"run"}) {
		t.Errorf("directory holds %v, want no report", got)
	}
}

// -h prints the usage with exit code 0 and nothing on stdout.
func TestInspectHelp(t *testing.T) {
	r := inspect(t, "-h")
	if r.code != exitInspected || r.stdout != "" || !strings.Contains(r.stderr, "usage: inspect -run DIR") {
		t.Errorf("exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
}

// -max-records refuses a dataset beyond the limit before anything is
// written.
func TestInspectEnforcesTheRecordLimit(t *testing.T) {
	dir, f := setup(t)
	f["max-records"] = "5"

	r := inspect(t, args(f)...)
	if r.code != exitFailure || r.stdout != "" || !strings.Contains(r.stderr, "more than 5 records") {
		t.Errorf("exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if got := entries(t, dir); !reflect.DeepEqual(got, []string{"run"}) {
		t.Errorf("directory holds %v, want no report", got)
	}
}
