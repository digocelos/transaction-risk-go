package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/scoring"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

const (
	examplesDir = "../../docs/contracts/examples-v2"
	fixture     = "../../training/testdata/pix-risk-v1.jsonl"
	baseline    = "../../docs/contracts/training-config-examples/01-baseline.json"
)

type result struct {
	code   int
	stdout string
	stderr string
}

func predict(t *testing.T, stdin io.Reader, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(args, stdin, &stdout, &stderr)
	return result{code, stdout.String(), stderr.String()}
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

// writeModel trains a small model on the synthetic fixture and writes its
// artifact into a temporary directory.
func writeModel(t *testing.T) string {
	t.Helper()

	cfg, err := trainingconfig.Decode(read(t, baseline))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs = 300

	trained, err := training.Run(context.Background(), bytes.NewReader(read(t, fixture)), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := trainingreport.Marshal(trained.Report)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := modelartifact.FromTraining(trained.Network, trained.State, trained.Report,
		digest(report), modelartifact.SoftwareFromBuild())
	if err != nil {
		t.Fatal(err)
	}
	model, err := modelartifact.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "pix-risk.json")
	if err := os.WriteFile(path, model, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// output parses stdout, which must hold exactly one compact JSON object
// followed by a newline, and returns its member names in order and its
// members.
func output(t *testing.T, stdout string) ([]string, map[string]any) {
	t.Helper()

	if !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout = %q, want one compact JSON line", stdout)
	}

	var object map[string]any
	if err := json.Unmarshal([]byte(stdout), &object); err != nil {
		t.Fatalf("stdout is not a JSON object: %v", err)
	}

	decoder := json.NewDecoder(strings.NewReader(stdout))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	var names []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, token.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}

	return names, object
}

// asJSON converts a value to its generic JSON form.
func asJSON(t *testing.T, value any) any {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	return generic
}

func inputs(t *testing.T) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(examplesDir, "[0-9][0-9]-*.json"))
	if err != nil {
		t.Fatal(err)
	}

	var inputs []string
	for _, path := range paths {
		if !strings.HasSuffix(path, ".expected.json") {
			inputs = append(inputs, path)
		}
	}
	if len(inputs) != 20 {
		t.Fatalf("found %d examples, want 20", len(inputs))
	}
	return inputs
}

// Every specification example is scored, invalid, or insufficient with its
// exit code, and every output identifies the artifact given by -model.
func TestPredictSpecificationExamples(t *testing.T) {
	model := writeModel(t)
	artifact, err := modelartifact.Load(model)
	if err != nil {
		t.Fatal(err)
	}
	scorer, err := scoring.Load(model)
	if err != nil {
		t.Fatal(err)
	}

	wantModel := map[string]any{
		"artifact_version": "pix-risk-model-v1",
		"artifact_sha256":  digest(read(t, model)),
		"feature_schema":   "pix-risk-v1",
		"threshold":        map[string]any{"policy": artifact.Threshold.Policy, "value": artifact.Threshold.Value},
	}

	for _, input := range inputs(t) {
		t.Run(filepath.Base(input), func(t *testing.T) {
			var expected map[string]any
			if err := json.Unmarshal(read(t, strings.TrimSuffix(input, ".json")+".expected.json"), &expected); err != nil {
				t.Fatal(err)
			}

			r := predict(t, nil, "-model", model, input)
			names, got := output(t, r.stdout)

			if !reflect.DeepEqual(got["model"], wantModel) {
				t.Errorf("model = %v, want %v", got["model"], wantModel)
			}

			switch expected["status"] {
			case "prepared":
				want, err := scorer.Score(read(t, input))
				if err != nil {
					t.Fatal(err)
				}

				if r.code != exitScored || got["status"] != "scored" || got["contract_version"] != "pix-payer-input-v2" {
					t.Fatalf("exit code %d, stdout %s", r.code, r.stdout)
				}
				if score, ok := got["score"].(float64); !ok || score != *want.Score {
					t.Errorf("score = %v, want the scoring API's %v", got["score"], *want.Score)
				}
				if !reflect.DeepEqual(got["notes"], expected["notes"]) {
					t.Errorf("notes = %v, want %v", got["notes"], expected["notes"])
				}
				if want := []string{"status", "contract_version", "model", "score", "notes"}; !reflect.DeepEqual(names, want) {
					t.Errorf("members = %v, want %v", names, want)
				}
			case "insufficient_input":
				if r.code != exitInsufficientInput || got["status"] != "insufficient_input" ||
					got["contract_version"] != "pix-payer-input-v2" {
					t.Fatalf("exit code %d, stdout %s", r.code, r.stdout)
				}
				if !reflect.DeepEqual(got["blockers"], expected["blockers"]) {
					t.Errorf("blockers = %v, want %v", got["blockers"], expected["blockers"])
				}
				if want := []string{"status", "contract_version", "model", "blockers"}; !reflect.DeepEqual(names, want) {
					t.Errorf("members = %v, want %v", names, want)
				}
			case "invalid_input":
				if r.code != exitInvalidInput || got["status"] != "invalid_input" {
					t.Fatalf("exit code %d, stdout %s", r.code, r.stdout)
				}
				issues, _ := got["issues"].([]any)
				for _, issue := range issues {
					fields := issue.(map[string]any)
					if message, _ := fields["message"].(string); message == "" {
						t.Errorf("issue %v has no message", fields)
					}
					delete(fields, "message")
				}
				if !reflect.DeepEqual(asJSON(t, issues), expected["issues"]) {
					t.Errorf("issues = %v, want %v", issues, expected["issues"])
				}
				if want := []string{"status", "model", "issues"}; !reflect.DeepEqual(names, want) {
					t.Errorf("members = %v, want %v", names, want)
				}
			}

			if r.stderr != "" {
				t.Errorf("stderr = %q, want nothing", r.stderr)
			}
		})
	}
}

// Without an input argument, or with "-", the input is read from stdin.
func TestPredictReadsStandardInput(t *testing.T) {
	model := writeModel(t)
	input := filepath.Join(examplesDir, "01-complete.json")
	want := predict(t, nil, "-model", model, input)

	for _, args := range [][]string{{"-model", model}, {"-model", model, "-"}} {
		got := predict(t, bytes.NewReader(read(t, input)), args...)
		if got != want {
			t.Errorf("args %v: %+v, want %+v", args, got, want)
		}
	}
}

func TestPredictRejectsAnOversizedInput(t *testing.T) {
	model := writeModel(t)

	r := predict(t, strings.NewReader("{"+strings.Repeat(" ", 65536)), "-model", model)
	if r.code != exitInvalidInput {
		t.Fatalf("exit code = %d, want %d", r.code, exitInvalidInput)
	}

	_, got := output(t, r.stdout)
	issues := got["issues"].([]any)
	if len(issues) != 1 || issues[0].(map[string]any)["code"] != "document_too_large" {
		t.Errorf("issues = %v, want document_too_large", issues)
	}
}

// Only pix-payer-input-v2 documents are scored: a version 1 input and a
// training record, which carries an outcome and a group key, are invalid.
func TestPredictRejectsOtherDocuments(t *testing.T) {
	model := writeModel(t)
	record := bytes.SplitN(read(t, fixture), []byte("\n"), 2)[0]

	cases := []struct {
		name  string
		input []byte
		issue map[string]any
	}{
		{"version 1", read(t, "../../docs/contracts/examples/01-complete.json"),
			map[string]any{"path": "/contract_version", "code": "unsupported_contract_version"}},
		{"training record", record,
			map[string]any{"path": "/contract_version", "code": "required"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := predict(t, bytes.NewReader(c.input), "-model", model)
			if r.code != exitInvalidInput {
				t.Fatalf("exit code = %d, want %d; stdout %s", r.code, exitInvalidInput, r.stdout)
			}

			_, got := output(t, r.stdout)
			issues := got["issues"].([]any)
			if len(issues) != 1 {
				t.Fatalf("issues = %v, want one", issues)
			}
			issue := issues[0].(map[string]any)
			delete(issue, "message")
			if !reflect.DeepEqual(issue, c.issue) {
				t.Errorf("issue = %v, want %v", issue, c.issue)
			}
		})
	}
}

// unreadable fails the test when it is read.
type unreadable struct{ t *testing.T }

func (r unreadable) Read([]byte) (int, error) {
	r.t.Error("the input was read before the model was loaded")
	return 0, errors.New("unexpected read")
}

// Operational failures exit with code 1, write nothing to stdout, and
// explain themselves on stderr. The model is loaded and validated before
// the input is read.
func TestPredictOperationalFailures(t *testing.T) {
	model := writeModel(t)
	input := filepath.Join(examplesDir, "01-complete.json")
	dir := t.TempDir()

	cases := []struct {
		name  string
		stdin io.Reader
		args  []string
		want  string
	}{
		{"missing -model", unreadable{t}, []string{input}, "missing required flag -model"},
		{"missing model file", unreadable{t}, []string{"-model", filepath.Join(dir, "absent.json")}, "read model artifact"},
		{"educational model", unreadable{t}, []string{"-model", "../../model.json"}, "decode model artifact"},
		{"dataset as model", unreadable{t}, []string{"-model", fixture}, "decode model artifact"},
		{"missing input file", nil, []string{"-model", model, filepath.Join(dir, "absent.json")}, "absent.json"},
		{"too many arguments", unreadable{t}, []string{"-model", model, input, input}, "unexpected argument"},
		{"unknown flag", unreadable{t}, []string{"-model", model, "-threshold", "0.5", input}, "flag provided but not defined"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := predict(t, c.stdin, c.args...)

			if r.code != exitFailure || r.stdout != "" {
				t.Fatalf("exit code = %d, stdout %q, want 1 and nothing", r.code, r.stdout)
			}
			if !strings.Contains(r.stderr, c.want) {
				t.Errorf("stderr does not contain %q:\n%s", c.want, r.stderr)
			}
		})
	}
}

// predict uses only the model given by -model: a model.json in the working
// directory is ignored.
func TestPredictIgnoresModelFilesInTheWorkingDirectory(t *testing.T) {
	model := writeModel(t)
	input, err := filepath.Abs(filepath.Join(examplesDir, "01-complete.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := predict(t, nil, "-model", model, input)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.json"), []byte("not a model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if got := predict(t, nil, "-model", model, input); got != want || got.code != exitScored {
		t.Errorf("in a directory with model.json: %+v, want %+v", got, want)
	}
	if r := predict(t, nil, input); r.code != exitFailure || !strings.Contains(r.stderr, "missing required flag -model") {
		t.Errorf("without -model: %+v", r)
	}
}

// Repeated runs score the same input identically and leave the model file
// unchanged.
func TestPredictIsRepeatable(t *testing.T) {
	model := writeModel(t)
	before := read(t, model)
	input := filepath.Join(examplesDir, "02-partial-dict.json")

	first := predict(t, nil, "-model", model, input)
	for range 3 {
		if again := predict(t, nil, "-model", model, input); again != first {
			t.Fatalf("repeated run = %+v, want %+v", again, first)
		}
	}
	if !bytes.Equal(read(t, model), before) {
		t.Error("predict changed the model file")
	}
}

func TestPredictHelp(t *testing.T) {
	r := predict(t, nil, "-h")
	if r.code != exitScored || r.stdout != "" || !strings.Contains(r.stderr, "usage: predict -model FILE") {
		t.Errorf("exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
}
