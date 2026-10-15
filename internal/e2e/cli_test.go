package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const benchmarkConfig = "docs/operations/benchmark-config.json"

// entries lists the names in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()

	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range list {
		names = append(names, entry.Name())
	}
	return names
}

func write(t *testing.T, path string, data []byte) string {
	t.Helper()

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Every specification example scores, is insufficient, or is invalid
// through the command, with its exit code: complete, partial, new-account,
// stale, no-key, no-device, zero-limit, and invalid inputs.
func TestPredictSpecificationExamples(t *testing.T) {
	bin, work := commands(t), t.TempDir()

	dataset := write(t, filepath.Join(work, "data.jsonl"), generate(t, 3000, 1))
	runDir, model := filepath.Join(work, "run"), filepath.Join(work, "model.json")
	if s := run(t, bin, "train", nil, "-config", benchmarkConfig, "-dataset", dataset, "-run-dir", runDir); s.code != 0 {
		t.Fatalf("train: %d\n%s", s.code, s.stderr)
	}
	if s := run(t, bin, "promote", nil, "-run", runDir, "-output", model); s.code != 0 {
		t.Fatalf("promote: %d\n%s", s.code, s.stderr)
	}

	codes := map[string]int{"prepared": 0, "invalid_input": 2, "insufficient_input": 3}
	examples, err := filepath.Glob(filepath.Join(root, "docs/contracts/examples-v2/[0-9][0-9]-*.expected.json"))
	if err != nil || len(examples) != 20 {
		t.Fatalf("found %d examples: %v", len(examples), err)
	}

	for _, expected := range examples {
		var want struct {
			Status string          `json:"status"`
			Notes  json.RawMessage `json:"notes"`
		}
		if err := json.Unmarshal(read(t, expected), &want); err != nil {
			t.Fatal(err)
		}

		input, _ := filepath.Rel(root, strings.TrimSuffix(expected, ".expected.json")+".json")
		s := run(t, bin, "predict", nil, "-model", model, input)
		if s.code != codes[want.Status] {
			t.Errorf("%s: exit code %d, want %d; stdout %s", input, s.code, codes[want.Status], s.stdout)
			continue
		}

		if want.Status == "prepared" {
			var got struct {
				Notes json.RawMessage `json:"notes"`
			}
			if err := json.Unmarshal(s.stdout, &got); err != nil {
				t.Fatal(err)
			}
			var gotNotes, wantNotes any
			json.Unmarshal(got.Notes, &gotNotes)
			json.Unmarshal(want.Notes, &wantNotes)
			if !reflect.DeepEqual(gotNotes, wantNotes) {
				t.Errorf("%s: notes %s, want %s", input, got.Notes, want.Notes)
			}
		}
	}
}

// Failures stop before anything is published, promoted, or reported:
// invalid records, version 1 records, another feature schema, damaged
// artifacts, tampered runs, existing and unwritable run directories.
func TestFailuresWriteNothing(t *testing.T) {
	bin, work := commands(t), t.TempDir()

	data := generate(t, 3000, 2)
	dataset := write(t, filepath.Join(work, "data.jsonl"), data)
	runDir, model := filepath.Join(work, "run"), filepath.Join(work, "model.json")
	if s := run(t, bin, "train", nil, "-config", benchmarkConfig, "-dataset", dataset, "-run-dir", runDir); s.code != 0 {
		t.Fatalf("train: %d\n%s", s.code, s.stderr)
	}
	if s := run(t, bin, "promote", nil, "-run", runDir, "-output", model); s.code != 0 {
		t.Fatalf("promote: %d\n%s", s.code, s.stderr)
	}

	invalid := write(t, filepath.Join(work, "invalid.jsonl"), append(bytes.Clone(data), []byte("{}\n")...))
	retired := write(t, filepath.Join(work, "retired.jsonl"),
		bytes.ReplaceAll(data, []byte(`"pix-training-example-v2"`), []byte(`"pix-training-example-v1"`)))
	otherSchema := write(t, filepath.Join(work, "other-schema.json"),
		bytes.Replace(read(t, filepath.Join(root, benchmarkConfig)), []byte(`"pix-risk-v1"`), []byte(`"transaction-features-v1"`), 1))

	artifact := read(t, model)
	// One weight changes and the artifact stays valid JSON, so only its
	// self-hash can reveal the damage.
	weight := regexp.MustCompile(`"weights": \[\s*\[\s*(-?[0-9.e+-]+)`).FindSubmatchIndex(artifact)
	if weight == nil {
		t.Fatal("the artifact layout changed")
	}
	damaged := write(t, filepath.Join(work, "damaged.json"),
		slices.Concat(artifact[:weight[2]], []byte("0.4242"), artifact[weight[3]:]))

	tampered := filepath.Join(work, "tampered")
	if err := os.CopyFS(tampered, os.DirFS(runDir)); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(tampered, "report.json")
	write(t, report, bytes.Replace(read(t, report), []byte(`"requested_epochs": 100`), []byte(`"requested_epochs": 101`), 1))

	readOnly := filepath.Join(work, "read-only")
	if err := os.Mkdir(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		command string
		args    []string
		code    int
		status  string
		// absent must not exist afterwards.
		absent string
	}{
		{"invalid record, train", "train", []string{"-config", benchmarkConfig, "-dataset", invalid, "-run-dir", filepath.Join(work, "r1")},
			2, "invalid_dataset", filepath.Join(work, "r1")},
		{"invalid record, evaluate", "evaluate", []string{"-model", model, "-dataset", invalid, "-partition", "test", "-output", filepath.Join(work, "e1.json")},
			2, "invalid_dataset", filepath.Join(work, "e1.json")},
		{"version 1 records", "train", []string{"-config", benchmarkConfig, "-dataset", retired, "-run-dir", filepath.Join(work, "r2")},
			2, "invalid_dataset", filepath.Join(work, "r2")},
		{"another feature schema", "train", []string{"-config", otherSchema, "-dataset", dataset, "-run-dir", filepath.Join(work, "r3")},
			1, "", filepath.Join(work, "r3")},
		{"damaged artifact, predict", "predict", []string{"-model", damaged, "docs/contracts/examples-v2/01-complete.json"},
			1, "", ""},
		{"damaged artifact, evaluate", "evaluate", []string{"-model", damaged, "-dataset", dataset, "-partition", "test", "-output", filepath.Join(work, "e2.json")},
			1, "", filepath.Join(work, "e2.json")},
		{"tampered run", "promote", []string{"-run", tampered, "-output", filepath.Join(work, "m1.json")},
			1, "", filepath.Join(work, "m1.json")},
		{"existing run directory", "train", []string{"-config", benchmarkConfig, "-dataset", dataset, "-run-dir", runDir},
			1, "", ""},
		{"unwritable run directory", "train", []string{"-config", benchmarkConfig, "-dataset", dataset, "-run-dir", filepath.Join(readOnly, "run")},
			1, "", filepath.Join(readOnly, "run")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "unwritable run directory" && os.Geteuid() == 0 {
				t.Skip("root writes into read-only directories")
			}

			s := run(t, bin, c.command, nil, c.args...)
			if s.code != c.code {
				t.Fatalf("exit code %d, want %d; stderr:\n%s", s.code, c.code, s.stderr)
			}
			if c.status != "" {
				var out struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal(s.stdout, &out); err != nil || out.Status != c.status {
					t.Errorf("stdout %s, want status %s", s.stdout, c.status)
				}
			} else if len(s.stdout) != 0 {
				t.Errorf("stdout = %s, want nothing", s.stdout)
			}
			if c.absent != "" {
				if _, err := os.Lstat(c.absent); err == nil {
					t.Errorf("%s was written", c.absent)
				}
			}
		})
	}

	if got := entries(t, readOnly); len(got) != 0 {
		t.Errorf("the read-only directory holds %v", got)
	}
	if !bytes.Equal(read(t, model), artifact) {
		t.Error("a failure changed the promoted model")
	}
	if got := entries(t, runDir); !reflect.DeepEqual(got, []string{"config.json", "manifest.json", "model.json", "report.json"}) {
		t.Errorf("the existing run holds %v", got)
	}
}

// An interrupted training run publishes nothing and leaves no staging
// directory behind.
func TestInterruptedTrainingPublishesNothing(t *testing.T) {
	bin, work := commands(t), t.TempDir()

	dataset := write(t, filepath.Join(work, "data.jsonl"), generate(t, 30000, 4))
	config := write(t, filepath.Join(work, "long.json"),
		bytes.Replace(read(t, filepath.Join(root, benchmarkConfig)), []byte(`"max_epochs": 100`), []byte(`"max_epochs": 100000`), 1))
	parent := filepath.Join(work, "runs")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(filepath.Join(bin, "train"), "-config", config, "-dataset", dataset, "-run-dir", filepath.Join(parent, "run"))
	command.Dir = root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond)
	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an interrupted run exited with 0")
		}
	case <-time.After(30 * time.Second):
		command.Process.Kill()
		t.Fatal("the run did not stop within 30 seconds of the interrupt")
	}

	t.Logf("exit code %d; stderr: %s", command.ProcessState.ExitCode(), stderr.String())
	if command.ProcessState.ExitCode() == 1 && !strings.Contains(stderr.String(), "canceled; nothing was written") {
		t.Errorf("stderr:\n%s", stderr.String())
	}
	if got := entries(t, parent); len(got) != 0 {
		t.Errorf("the interrupted run left %v", got)
	}
}

// The training data template converts and trains with the built commands,
// and files in data/, where the guide puts real data, are ignored by git.
func TestTemplateWorkflow(t *testing.T) {
	bin, work := commands(t), t.TempDir()

	dataset := filepath.Join(work, "training.jsonl")
	converted := run(t, bin, "convert", nil,
		"-input", "templates/training-data/pix-training-example-v2.csv", "-output", dataset)
	var summary struct {
		Status  string `json:"status"`
		Records struct {
			Usable int `json:"usable"`
		} `json:"records"`
	}
	decode(t, "convert", converted, 0, &summary)
	if summary.Status != "converted" || summary.Records.Usable != 4 {
		t.Errorf("convert = %+v", summary)
	}

	var trained struct {
		Status string `json:"status"`
	}
	decode(t, "train", run(t, bin, "train", nil,
		"-config", "docs/contracts/training-config-examples/01-baseline.json",
		"-dataset", dataset, "-run-dir", filepath.Join(work, "run")), 0, &trained)

	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Skipf("git does not run here: %v", err)
	}
	for _, path := range []string{"data/training.csv", "data/training.jsonl", "data/run/model.json"} {
		check := exec.Command("git", "check-ignore", "-q", path)
		check.Dir = root
		if err := check.Run(); err != nil {
			t.Errorf("git does not ignore %s: %v", path, err)
		}
	}
}
