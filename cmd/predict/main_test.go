package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const examplesDir = "docs/contracts/examples"

type result struct {
	code   int
	stdout string
	stderr string
}

// predict runs the command from the repository root, where model.json and
// the examples live.
func predict(t *testing.T, stdin io.Reader, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(args, stdin, &stdout, &stderr)

	return result{code, stdout.String(), stderr.String()}
}

// output parses stdout, which must hold exactly one compact JSON object
// followed by a newline.
func output(t *testing.T, stdout string) map[string]any {
	t.Helper()

	if !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout = %q, want one compact JSON line", stdout)
	}

	decoder := json.NewDecoder(strings.NewReader(stdout))

	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		t.Fatalf("stdout is not a JSON object: %v", err)
	}

	if decoder.More() {
		t.Fatalf("stdout = %q, want exactly one JSON object", stdout)
	}

	return object
}

// withoutMessages drops issue messages, which are not part of the contract.
func withoutMessages(t *testing.T, object map[string]any) map[string]any {
	t.Helper()

	issues, ok := object["issues"].([]any)
	if !ok {
		return object
	}

	for _, issue := range issues {
		fields := issue.(map[string]any)
		if message, _ := fields["message"].(string); message == "" {
			t.Errorf("issue %v has no message", fields)
		}
		delete(fields, "message")
	}

	return object
}

func TestPredictSpecificationExamples(t *testing.T) {
	t.Chdir("../..")

	exitCodes := map[string]int{"scored": 0, "invalid_input": 2, "insufficient_input": 3}

	inputs, err := filepath.Glob(filepath.Join(examplesDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}

	count := 0

	for _, input := range inputs {
		if strings.HasSuffix(input, ".expected.json") {
			continue
		}
		count++

		t.Run(filepath.Base(input), func(t *testing.T) {
			data, err := os.ReadFile(strings.TrimSuffix(input, ".json") + ".expected.json")
			if err != nil {
				t.Fatal(err)
			}

			var want map[string]any
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}

			got := predict(t, strings.NewReader(""), input)

			if wantCode := exitCodes[want["status"].(string)]; got.code != wantCode {
				t.Errorf("exit code = %d, want %d (stderr %q)", got.code, wantCode, got.stderr)
			}

			if got.stderr != "" {
				t.Errorf("stderr = %q, want empty", got.stderr)
			}

			if object := withoutMessages(t, output(t, got.stdout)); !reflect.DeepEqual(object, want) {
				t.Errorf("stdout = %v\nwant     %v", object, want)
			}
		})
	}

	if count != 17 {
		t.Errorf("found %d examples, want 17", count)
	}
}

func TestPredictReadsStandardInput(t *testing.T) {
	t.Chdir("../..")

	for _, args := range [][]string{nil, {"-"}} {
		data, err := os.ReadFile(filepath.Join(examplesDir, "01-complete.json"))
		if err != nil {
			t.Fatal(err)
		}

		got := predict(t, bytes.NewReader(data), args...)
		if got.code != 0 {
			t.Fatalf("args %v: exit code = %d, stderr %q", args, got.code, got.stderr)
		}

		if score := output(t, got.stdout)["score"]; score != 0.18734535214416673 {
			t.Errorf("args %v: score = %v, want 0.18734535214416673", args, score)
		}
	}
}

func TestPredictRejectsAnOversizedInput(t *testing.T) {
	t.Chdir("../..")

	got := predict(t, strings.NewReader("{"+strings.Repeat(" ", 65536)))

	if got.code != 2 {
		t.Errorf("exit code = %d, want 2", got.code)
	}

	want := map[string]any{
		"status": "invalid_input",
		"issues": []any{map[string]any{"path": "", "code": "document_too_large"}},
	}
	if object := withoutMessages(t, output(t, got.stdout)); !reflect.DeepEqual(object, want) {
		t.Errorf("stdout = %v, want %v", object, want)
	}
}

func TestPredictOperationalFailures(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	example := func(name string) string {
		return filepath.Join(root, examplesDir, name)
	}

	tests := []struct {
		name string
		dir  string
		args []string
	}{
		{"missing input file", root, []string{"does-not-exist.json"}},
		{"too many arguments", root, []string{example("01-complete.json"), example("02-dict-group-missing.json")}},
		{"missing model", t.TempDir(), []string{example("01-complete.json")}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(test.dir)

			got := predict(t, strings.NewReader(""), test.args...)

			if got.code != 1 {
				t.Errorf("exit code = %d, want 1", got.code)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want empty", got.stdout)
			}
			if got.stderr == "" {
				t.Error("stderr is empty, want a message")
			}
		})
	}
}

func TestPredictReportsContractProblemsWithoutAModel(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	tests := map[string]int{
		"12-invalid-negative-zero.json":          2,
		"06-integrator-observation-missing.json": 3,
	}

	for name, wantCode := range tests {
		got := predict(t, strings.NewReader(""), filepath.Join(root, examplesDir, name))
		if got.code != wantCode {
			t.Errorf("%s: exit code = %d, want %d (stderr %q)", name, got.code, wantCode, got.stderr)
		}
	}
}

// predict reads pix-payer-input-v1 only until it migrates to version 2.
func TestPredictRejectsVersion2Inputs(t *testing.T) {
	t.Chdir("../..")

	r := predict(t, nil, "docs/contracts/examples-v2/01-complete.json")
	if r.code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr: %s", r.code, r.stderr)
	}

	got := withoutMessages(t, output(t, r.stdout))
	want := map[string]any{
		"status": "invalid_input",
		"issues": []any{map[string]any{"path": "/contract_version", "code": "unsupported_contract_version"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output = %v, want %v", got, want)
	}
}
