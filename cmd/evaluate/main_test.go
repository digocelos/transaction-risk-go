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
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
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

func evaluateWith(t *testing.T, ctx context.Context, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(ctx, args, &stdout, &stderr)
	return result{code, stdout.String(), stderr.String()}
}

func evaluate(t *testing.T, args ...string) result {
	t.Helper()
	return evaluateWith(t, context.Background(), args...)
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

// writeModel trains a model on the selection fixture, with train_end
// 2026-05-31 and validation_end 2026-06-15, and writes its artifact into
// dir.
func writeModel(t *testing.T, dir string) string {
	t.Helper()

	cfg, err := trainingconfig.Decode(read(t, baseline))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs = 300
	cfg.TrainEnd = time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	cfg.ValidationEnd = time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	result, err := training.Run(context.Background(), bytes.NewReader(read(t, selectionFixture)), cfg, nil)
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

	path := filepath.Join(dir, "model.json")
	if err := os.WriteFile(path, model, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// setup writes a model into a temporary directory and returns that
// directory and a complete set of flags that evaluates the validation
// partition into dir/report.json.
func setup(t *testing.T) (string, map[string]string) {
	t.Helper()

	dir := t.TempDir()
	return dir, map[string]string{
		"model":     writeModel(t, dir),
		"dataset":   selectionFixture,
		"partition": "validation",
		"output":    filepath.Join(dir, "report.json"),
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

type evaluatedSummary struct {
	Status        string   `json:"status"`
	ReportSHA256  string   `json:"report_sha256"`
	ModelSHA256   string   `json:"model_sha256"`
	DatasetSHA256 string   `json:"dataset_sha256"`
	Incomplete    []string `json:"incomplete"`
}

// report decodes the parts of an evaluation report the tests check.
type report struct {
	Version string `json:"evaluation_report_version"`
	Model   struct {
		ArtifactSHA256 string `json:"artifact_sha256"`
	} `json:"model"`
	Scope struct {
		Partition *string `json:"partition"`
		After     string  `json:"after"`
		Through   *string `json:"through"`
	} `json:"scope"`
	GroupOverlap struct {
		Reference string `json:"reference"`
	} `json:"group_overlap"`
	Threshold struct {
		Source string  `json:"source"`
		Value  float64 `json:"value"`
	} `json:"threshold"`
	Metrics struct {
		TruePositives  int `json:"true_positives"`
		FalsePositives int `json:"false_positives"`
	} `json:"metrics"`
}

func readReport(t *testing.T, path string) report {
	t.Helper()

	var r report
	if err := json.Unmarshal(read(t, path), &r); err != nil {
		t.Fatal(err)
	}
	return r
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

// A complete evaluation writes the report and summarizes it on stdout with
// exit code 0. The model file is not changed.
func TestEvaluateWritesAReport(t *testing.T) {
	dir, f := setup(t)
	model := read(t, f["model"])

	r := evaluate(t, args(f)...)
	if r.code != exitEvaluated {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", r.code, exitEvaluated, r.stderr)
	}

	var summary evaluatedSummary
	output(t, r.stdout, &summary)

	want := evaluatedSummary{
		Status:        "evaluated",
		ReportSHA256:  digest(read(t, f["output"])),
		ModelSHA256:   digest(model),
		DatasetSHA256: digest(read(t, selectionFixture)),
		Incomplete:    []string{},
	}
	if !reflect.DeepEqual(summary, want) {
		t.Errorf("summary = %+v, want %+v", summary, want)
	}
	if !strings.HasPrefix(r.stdout, `{"status":"evaluated","report_sha256":`) {
		t.Errorf("stdout = %s, want status and report_sha256 first", r.stdout)
	}

	written := readReport(t, f["output"])
	if written.Version != "pix-evaluation-report-v1" || written.Model.ArtifactSHA256 != want.ModelSHA256 ||
		written.Scope.Partition == nil || *written.Scope.Partition != "validation" ||
		written.Threshold.Source != "artifact" || written.GroupOverlap.Reference != "evaluated_dataset" {
		t.Errorf("report = %+v", written)
	}

	if !bytes.Equal(read(t, f["model"]), model) {
		t.Error("evaluation changed the model file")
	}
	if got := entries(t, dir); !reflect.DeepEqual(got, []string{"model.json", "report.json"}) {
		t.Errorf("directory holds %v, want only the model and the report", got)
	}

	again := filepath.Join(dir, "again.json")
	f["output"] = again
	if r := evaluate(t, args(f)...); r.code != exitEvaluated || digest(read(t, again)) != want.ReportSHA256 {
		t.Errorf("a second evaluation wrote another report; exit code %d", r.code)
	}
}

// An incomplete evaluation still writes its report, names the reasons, and
// exits with code 3, so scripts cannot take it for a complete one.
func TestEvaluateReportsIncompleteEvaluations(t *testing.T) {
	_, f := setup(t)
	f["partition"] = "test"

	r := evaluate(t, args(f)...)
	if r.code != exitIncomplete {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", r.code, exitIncomplete, r.stderr)
	}

	var summary evaluatedSummary
	output(t, r.stdout, &summary)
	if summary.Status != "incomplete" || !reflect.DeepEqual(summary.Incomplete, []string{"unknown_loss_frauds"}) ||
		summary.ReportSHA256 != digest(read(t, f["output"])) {
		t.Errorf("summary = %+v", summary)
	}
	if !strings.Contains(r.stderr, "incomplete: unknown_loss_frauds") {
		t.Errorf("stderr does not name the reason:\n%s", r.stderr)
	}
}

// A dataset with invalid records is reported with exit code 2 and its
// diagnostics, and no report is written.
func TestEvaluateStopsOnInvalidDataset(t *testing.T) {
	dir, f := setup(t)
	f["dataset"] = invalidDataset

	r := evaluate(t, args(f)...)
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

	for _, want := range []string{
		"evaluate: line 7: /outcome/label: invalid_enum_value:",
		"evaluate: line 8: /scoring_input/pix_payment/reference_time_offset: invalid_format:",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, r.stderr)
		}
	}

	if got := entries(t, dir); !reflect.DeepEqual(got, []string{"model.json"}) {
		t.Errorf("directory holds %v, want no report", got)
	}
}

// A window scope, a threshold override, and a reference dataset reach the
// report.
func TestEvaluateWindowOverrideAndReference(t *testing.T) {
	_, f := setup(t)
	delete(f, "partition")
	f["after"] = "2026-06-05T21:00:00-03:00"
	f["through"] = "2026-06-21T00:00:00Z"
	f["threshold"] = "0"
	f["reference-dataset"] = selectionFixture

	r := evaluate(t, args(f)...)
	if r.code != exitEvaluated {
		t.Fatalf("exit code = %d; stderr:\n%s", r.code, r.stderr)
	}

	written := readReport(t, f["output"])
	if written.Scope.Partition != nil || written.Scope.After != "2026-06-06T00:00:00Z" ||
		written.Scope.Through == nil || *written.Scope.Through != "2026-06-21T00:00:00Z" {
		t.Errorf("scope = %+v", written.Scope)
	}
	if written.Threshold.Source != "override" || written.Threshold.Value != 0 ||
		written.Metrics.TruePositives != 1 || written.Metrics.FalsePositives != 2 {
		t.Errorf("threshold = %+v, metrics = %+v", written.Threshold, written.Metrics)
	}
	if written.GroupOverlap.Reference != "reference_dataset" {
		t.Errorf("group overlap reference = %s", written.GroupOverlap.Reference)
	}
	if !strings.Contains(r.stderr, "threshold override 0 (the model's threshold is") {
		t.Errorf("stderr does not label the override:\n%s", r.stderr)
	}
}

// Every failure exits with code 1 and writes no report.
func TestEvaluateRejectsInvalidArguments(t *testing.T) {
	cases := []struct {
		name   string
		change func(dir string, f map[string]string)
		want   string
	}{
		{"missing flags", func(_ string, f map[string]string) { delete(f, "model"); delete(f, "output") },
			"missing required flags: -model, -output"},
		{"no scope", func(_ string, f map[string]string) { delete(f, "partition") },
			"set -partition or -after"},
		{"partition and window", func(_ string, f map[string]string) { f["after"] = "2026-06-01T00:00:00Z" },
			"set either -partition or -after"},
		{"through without after", func(_ string, f map[string]string) { f["through"] = "2026-06-01T00:00:00Z" },
			"-through needs -after"},
		{"timestamp without offset", func(_ string, f map[string]string) {
			delete(f, "partition")
			f["after"] = "2026-06-01T00:00:00"
		}, "-after must be an RFC 3339 timestamp with an offset"},
		{"invalid threshold", func(_ string, f map[string]string) { f["threshold"] = "high" },
			"invalid value \"high\" for flag -threshold"},
		{"threshold out of range", func(_ string, f map[string]string) { f["threshold"] = "1.5" },
			"threshold override must be a number from 0 to 1"},
		{"train partition", func(_ string, f map[string]string) { f["partition"] = "train" },
			"the train partition is never evaluated"},
		{"window before train_end", func(_ string, f map[string]string) {
			delete(f, "partition")
			f["after"] = "2026-05-01T00:00:00Z"
		}, "must not be earlier than the model's train_end"},
		{"missing model", func(dir string, f map[string]string) { f["model"] = filepath.Join(dir, "absent.json") },
			"absent.json"},
		{"invalid model", func(dir string, f map[string]string) { f["model"] = selectionFixture },
			"decode model artifact"},
		{"another reference dataset", func(_ string, f map[string]string) { f["reference-dataset"] = invalidDataset },
			"is not the model's training dataset"},
		{"missing output directory", func(dir string, f map[string]string) {
			f["output"] = filepath.Join(dir, "absent", "report.json")
		}, "absent"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, f := setup(t)
			c.change(dir, f)

			r := evaluate(t, args(f)...)
			if r.code != exitFailure {
				t.Fatalf("exit code = %d, want %d; stdout %q", r.code, exitFailure, r.stdout)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want nothing", r.stdout)
			}
			if !strings.Contains(r.stderr, c.want) {
				t.Errorf("stderr does not contain %q:\n%s", c.want, r.stderr)
			}
			if got := entries(t, dir); !reflect.DeepEqual(got, []string{"model.json"}) {
				t.Errorf("directory holds %v, want no report", got)
			}
		})
	}

	_, f := setup(t)
	if r := evaluate(t, append(args(f), "extra")...); r.code != exitFailure || !strings.Contains(r.stderr, `unexpected argument "extra"`) {
		t.Errorf("extra argument: exit code %d, stderr:\n%s", r.code, r.stderr)
	}
}

// An existing report is replaced only with -replace, and the output can
// never be the model or a dataset.
func TestEvaluateNeverOverwritesWithoutReplace(t *testing.T) {
	dir, f := setup(t)
	existing := []byte("existing\n")
	if err := os.WriteFile(f["output"], existing, 0o644); err != nil {
		t.Fatal(err)
	}

	r := evaluate(t, args(f)...)
	if r.code != exitFailure || !strings.Contains(r.stderr, "already exists") {
		t.Fatalf("exit code = %d, stderr:\n%s", r.code, r.stderr)
	}
	if !bytes.Equal(read(t, f["output"]), existing) {
		t.Error("the existing report was overwritten")
	}
	if strings.Contains(r.stderr, "in scope") {
		t.Errorf("the model was evaluated before the existing output was refused:\n%s", r.stderr)
	}

	r = evaluate(t, append(args(f), "-replace")...)
	if r.code != exitEvaluated || bytes.Equal(read(t, f["output"]), existing) {
		t.Fatalf("-replace: exit code = %d, stderr:\n%s", r.code, r.stderr)
	}

	model := read(t, f["model"])
	dataset := filepath.Join(dir, "dataset.jsonl")
	if err := os.WriteFile(dataset, read(t, selectionFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, target := range map[string]string{
		"model":             f["model"],
		"dataset":           dataset,
		"reference dataset": dataset,
	} {
		g := map[string]string{"model": f["model"], "dataset": selectionFixture, "partition": "validation", "output": target}
		if name == "dataset" {
			g["dataset"] = dataset
		}
		if name == "reference dataset" {
			g["reference-dataset"] = dataset
		}

		r := evaluate(t, append(args(g), "-replace")...)
		if r.code != exitFailure || !strings.Contains(r.stderr, "-output must not be the") {
			t.Errorf("%s: exit code = %d, stderr:\n%s", name, r.code, r.stderr)
		}
	}

	if !bytes.Equal(read(t, f["model"]), model) || !bytes.Equal(read(t, dataset), read(t, selectionFixture)) {
		t.Error("the model or the dataset was overwritten")
	}
}

// lateCancel is a context that is canceled from its second check on: the
// evaluation checks it once before reading a small dataset, so the
// cancellation arrives after the evaluation and before the report is
// written.
type lateCancel struct {
	context.Context
	checks int
}

func (c *lateCancel) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}

// A canceled evaluation writes nothing, even when the cancellation arrives
// after the evaluation.
func TestEvaluateStopsWhenCanceled(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	for name, ctx := range map[string]context.Context{
		"before":           canceled,
		"after evaluating": &lateCancel{Context: context.Background()},
	} {
		t.Run(name, func(t *testing.T) {
			dir, f := setup(t)

			r := evaluateWith(t, ctx, args(f)...)
			if r.code != exitFailure || r.stdout != "" || !strings.Contains(r.stderr, "canceled; nothing was written") {
				t.Fatalf("exit code = %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
			}
			if got := entries(t, dir); !reflect.DeepEqual(got, []string{"model.json"}) {
				t.Errorf("directory holds %v, want no report", got)
			}
		})
	}
}

// A report that cannot be renamed into place leaves no temporary file.
func TestEvaluateLeavesNothingBehindOnFailure(t *testing.T) {
	dir, f := setup(t)
	if err := os.Mkdir(f["output"], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f["output"], "kept"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	r := evaluate(t, append(args(f), "-replace")...)
	if r.code != exitFailure || r.stdout != "" {
		t.Fatalf("exit code = %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if got := entries(t, dir); !reflect.DeepEqual(got, []string{"model.json", "report.json"}) {
		t.Errorf("directory holds %v, want the model and the untouched directory", got)
	}
	if got := entries(t, f["output"]); !reflect.DeepEqual(got, []string{"kept"}) {
		t.Errorf("output directory holds %v", got)
	}
}

func TestEvaluateHelp(t *testing.T) {
	r := evaluate(t, "-h")
	if r.code != exitEvaluated || r.stdout != "" || !strings.Contains(r.stderr, "usage: evaluate") {
		t.Errorf("exit code = %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
}
