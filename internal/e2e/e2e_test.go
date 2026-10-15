// Package e2e runs the documented workflow end to end, with the commands
// built from this checkout: generate a synthetic dataset, train and select
// a model, publish and promote it, evaluate it, and score inputs with it.
package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// root is the repository root, relative to this package.
const root = "../.."

type step struct {
	code   int
	stdout []byte
	stderr string
}

// run runs a built command from the repository root.
func run(t *testing.T, bin, name string, stdin []byte, args ...string) step {
	t.Helper()

	command := exec.Command(filepath.Join(bin, name), args...)
	command.Dir = root
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr

	err := command.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return step{code, stdout.Bytes(), stderr.String()}
}

func decode(t *testing.T, name string, s step, want int, value any) {
	t.Helper()

	if s.code != want {
		t.Fatalf("%s: exit code %d, want %d; stderr:\n%s", name, s.code, want, s.stderr)
	}
	if err := json.Unmarshal(s.stdout, value); err != nil {
		t.Fatalf("%s: stdout %q: %v", name, s.stdout, err)
	}
}

// The quick start of the README, from a clean checkout: no committed model
// and no real data.
func TestDocumentedWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("builds every command")
	}

	bin, work := t.TempDir(), t.TempDir()
	// Without VCS stamping, concurrent builds of other tests never contend
	// for the repository's lock.
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin+string(filepath.Separator), "./cmd/...")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}

	dataset := filepath.Join(work, "data.jsonl")
	synthesized := run(t, bin, "synthesize", nil, "-records", "3000", "-seed", "1")
	if synthesized.code != 0 || bytes.Count(synthesized.stdout, []byte("\n")) != 3000 {
		t.Fatalf("synthesize: exit code %d; stderr:\n%s", synthesized.code, synthesized.stderr)
	}
	if err := os.WriteFile(dataset, synthesized.stdout, 0o644); err != nil {
		t.Fatal(err)
	}

	runDir := filepath.Join(work, "run")
	var trained struct {
		Status      string `json:"status"`
		ModelSHA256 string `json:"model_sha256"`
	}
	decode(t, "train", run(t, bin, "train", nil,
		"-config", "docs/operations/benchmark-config.json", "-dataset", dataset, "-run-dir", runDir), 0, &trained)

	model := filepath.Join(work, "model.json")
	var promoted struct {
		Status      string `json:"status"`
		ModelSHA256 string `json:"model_sha256"`
	}
	decode(t, "promote", run(t, bin, "promote", nil, "-run", runDir, "-output", model), 0, &promoted)
	if promoted.ModelSHA256 != trained.ModelSHA256 {
		t.Errorf("promoted model %s, want the trained %s", promoted.ModelSHA256, trained.ModelSHA256)
	}

	evaluation := filepath.Join(work, "evaluation.json")
	evaluated := run(t, bin, "evaluate", nil,
		"-model", model, "-dataset", dataset, "-partition", "test", "-output", evaluation)
	var summary struct {
		Status      string `json:"status"`
		ModelSHA256 string `json:"model_sha256"`
	}
	if evaluated.code == 3 {
		decode(t, "evaluate", evaluated, 3, &summary)
	} else {
		decode(t, "evaluate", evaluated, 0, &summary)
	}
	if summary.ModelSHA256 != trained.ModelSHA256 {
		t.Errorf("evaluated model %s, want %s", summary.ModelSHA256, trained.ModelSHA256)
	}
	if _, err := os.Stat(evaluation); err != nil {
		t.Errorf("no evaluation report: %v", err)
	}

	var scored struct {
		Status string  `json:"status"`
		Score  float64 `json:"score"`
		Model  struct {
			ArtifactSHA256 string `json:"artifact_sha256"`
		} `json:"model"`
	}
	decode(t, "predict", run(t, bin, "predict", nil,
		"-model", model, "docs/contracts/examples-v2/01-complete.json"), 0, &scored)
	if scored.Status != "scored" || scored.Model.ArtifactSHA256 != trained.ModelSHA256 || scored.Score < 0 || scored.Score > 1 {
		t.Errorf("predict = %+v", scored)
	}

	insufficient, err := os.ReadFile(filepath.Join(root, "docs/contracts/examples-v2/13-required-stale.json"))
	if err != nil {
		t.Fatal(err)
	}
	var blocked struct {
		Status string `json:"status"`
	}
	decode(t, "predict from stdin", run(t, bin, "predict", insufficient, "-model", model), 3, &blocked)
	if blocked.Status != "insufficient_input" {
		t.Errorf("predict from stdin = %+v", blocked)
	}
}
