package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
	"github.com/digocelos/transaction-risk-go/trainingrun"
)

type result struct {
	code   int
	stdout string
	stderr string
}

func promote(t *testing.T, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return result{code, stdout.String(), stderr.String()}
}

// publish trains a small run with seed and publishes it in a temporary
// directory, as cmd/train does.
func publish(t *testing.T, seed int64) string {
	t.Helper()

	data, err := os.ReadFile("../../docs/contracts/training-config-examples/01-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := trainingconfig.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs, cfg.Seed = 100, seed

	dataset, err := os.ReadFile("../../training/testdata/pix-risk-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	result, err := training.Run(context.Background(), bytes.NewReader(dataset), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(report)

	artifact, err := modelartifact.FromTraining(result.Network, result.State, result.Report,
		hex.EncodeToString(sum[:]), modelartifact.SoftwareFromBuild())
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

	dir := filepath.Join(t.TempDir(), "run")
	if err := trainingrun.Publish(dir, trainingrun.Bundle{Model: model, Report: report, Config: config}); err != nil {
		t.Fatal(err)
	}

	return dir
}

func sha256File(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestPromote(t *testing.T) {
	runDir, otherDir := publish(t, 42), publish(t, 7)
	output := filepath.Join(t.TempDir(), "model.json")

	r := promote(t, "-run", runDir, "-output", output)
	if r.code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", r.code, r.stderr)
	}

	model := sha256File(t, filepath.Join(runDir, "model.json"))
	if r.stdout != `{"status":"promoted","model_sha256":"`+model+`"}`+"\n" || sha256File(t, output) != model {
		t.Errorf("stdout = %q, promoted file %s, want the run's model %s", r.stdout, sha256File(t, output), model)
	}

	if r := promote(t, "-run", otherDir, "-output", output); r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "already exists") {
		t.Errorf("promotion over an existing file: exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if sha256File(t, output) != model {
		t.Error("a refused promotion changed the promoted file")
	}

	if r := promote(t, "-run", otherDir, "-output", output, "-replace"); r.code != 0 {
		t.Errorf("promotion with -replace: exit code %d, stderr:\n%s", r.code, r.stderr)
	}
	if sha256File(t, output) != sha256File(t, filepath.Join(otherDir, "model.json")) {
		t.Error("-replace did not promote the other run")
	}
}

func TestPromoteRejectsTamperedRuns(t *testing.T) {
	runDir := publish(t, 42)

	report := filepath.Join(runDir, "report.json")
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, bytes.Replace(data, []byte(`"seed": 42`), []byte(`"seed": 43`), 1), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	r := promote(t, "-run", runDir, "-output", filepath.Join(dir, "model.json"))

	if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "verify run") {
		t.Errorf("exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Error("a refused promotion wrote a file")
	}
}

func TestPromoteArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"missing run":    {"-output", "model.json"},
		"missing output": {"-run", "run"},
		"positional":     {"-run", "run", "-output", "model.json", "extra"},
		"unknown flag":   {"-run", "run", "-output", "model.json", "-force"},
		"absent run":     {"-run", filepath.Join(t.TempDir(), "absent"), "-output", filepath.Join(t.TempDir(), "model.json")},
	} {
		if r := promote(t, args...); r.code != 1 || r.stdout != "" || r.stderr == "" {
			t.Errorf("%s: exit code %d, stdout %q, stderr %q", name, r.code, r.stdout, r.stderr)
		}
	}

	if r := promote(t, "-h"); r.code != 0 || r.stdout != "" || !strings.Contains(r.stderr, "-replace") {
		t.Errorf("help: exit code %d, stdout %q, stderr:\n%s", r.code, r.stdout, r.stderr)
	}
}
