package trainingrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

const (
	fixture  = "../training/testdata/pix-risk-v1.jsonl"
	baseline = "../docs/contracts/training-config-examples/01-baseline.json"
)

var software = modelartifact.Software{Module: "github.com/digocelos/transaction-risk-go", Version: "(devel)", Revision: "0123abc"}

// newBundle trains a small model on the synthetic fixture with seed and
// returns its run files.
func newBundle(t *testing.T, seed int64) Bundle {
	t.Helper()

	data, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := trainingconfig.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs, cfg.Seed = 200, seed

	dataset, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	result, err := training.Run(context.Background(), bytes.NewReader(dataset), cfg, training.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}

	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := modelartifact.FromTraining(result.Network, result.State, result.Report, sha256Hex(report), software)
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

	return Bundle{Model: model, Report: report, Config: config}
}

func names(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var result []string
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}

func TestPublishWritesAVerifiableRun(t *testing.T) {
	bundle := newBundle(t, 42)
	dir := filepath.Join(t.TempDir(), "run-1")

	if err := Publish(dir, bundle); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	run, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	if !bytes.Equal(run.Bundle.Model, bundle.Model) || !bytes.Equal(run.Bundle.Report, bundle.Report) ||
		!bytes.Equal(run.Bundle.Config, bundle.Config) {
		t.Error("the published files differ from the bundle")
	}

	manifest := NewManifest(bundle)
	if run.Manifest.ManifestVersion != "pix-risk-run-v1" || len(run.Manifest.Files) != 3 ||
		run.Manifest.Files[0] != manifest.Files[0] {
		t.Errorf("manifest = %+v", run.Manifest)
	}

	if names(t, filepath.Dir(dir))[0] != "run-1" || len(names(t, filepath.Dir(dir))) != 1 {
		t.Errorf("parent directory = %v, want only the run", names(t, filepath.Dir(dir)))
	}

	// An existing run is never overwritten, nor even staged.
	original := filesystem
	filesystem.mkdirTemp = func(string, string) (string, error) {
		t.Error("publishing over an existing run staged files")
		return "", errors.New("unexpected staging")
	}
	defer func() { filesystem = original }()

	before := names(t, dir)
	if err := Publish(dir, newBundle(t, 7)); !errors.Is(err, ErrExists) {
		t.Errorf("Publish() over an existing run: error = %v, want ErrExists", err)
	}
	if again, err := Verify(dir); err != nil || !bytes.Equal(again.Bundle.Model, bundle.Model) || len(names(t, dir)) != len(before) {
		t.Error("publishing over an existing run changed it")
	}
}

// A bundle whose files do not bind each other is never written.
func TestPublishRefusesInconsistentBundles(t *testing.T) {
	bundle := newBundle(t, 42)
	other := newBundle(t, 7)

	tests := map[string]Bundle{
		"model of another report": {Model: other.Model, Report: bundle.Report, Config: bundle.Config},
		"edited report":           {Model: bundle.Model, Report: append(append([]byte{}, bundle.Report...), ' '), Config: bundle.Config},
		"non-canonical config":    {Model: bundle.Model, Report: bundle.Report, Config: append(append([]byte{}, bundle.Config...), '\n')},
		"invalid model":           {Model: []byte("{}"), Report: bundle.Report, Config: bundle.Config},
		"config of another seed": func() Bundle {
			return Bundle{Model: bundle.Model, Report: bundle.Report,
				Config: []byte(strings.Replace(string(bundle.Config), `"seed":42`, `"seed":7`, 1))}
		}(),
	}

	original := filesystem
	defer func() { filesystem = original }()
	filesystem.mkdirTemp = func(string, string) (string, error) {
		t.Error("an inconsistent bundle was staged")
		return "", errors.New("unexpected staging")
	}

	for name, inconsistent := range tests {
		parent := t.TempDir()
		if err := Publish(filepath.Join(parent, "run"), inconsistent); err == nil {
			t.Errorf("%s: Publish() error = nil, want an error", name)
		}
		if got := names(t, parent); len(got) != 0 {
			t.Errorf("%s: parent directory = %v, want nothing written", name, got)
		}
	}
}

// A failure at any step leaves no published run and no staging directory.
func TestPublishLeavesNothingBehindOnFailure(t *testing.T) {
	bundle := newBundle(t, 42)
	original := filesystem
	defer func() { filesystem = original }()

	injected := errors.New("injected failure")

	failures := map[string]func(){
		"create staging": func() {
			filesystem.mkdirTemp = func(string, string) (string, error) { return "", injected }
		},
		"rename": func() {
			filesystem.rename = func(string, string) error { return injected }
		},
	}
	for call := 1; call <= 4; call++ {
		failAt := call
		failures["write file "+string(rune('0'+call))] = func() {
			count := 0
			filesystem.writeFile = func(path string, data []byte) error {
				count++
				if count == failAt {
					// Leave a partial file behind, as a crash would.
					os.WriteFile(path, data[:len(data)/2], 0o644)
					return injected
				}
				return original.writeFile(path, data)
			}
		}
	}

	// A write that reports success but stores other bytes is caught by
	// reading the staged run back.
	failures["silently corrupted write"] = func() {
		filesystem.writeFile = func(path string, data []byte) error {
			if filepath.Base(path) == ReportFile {
				data = bytes.Replace(data, []byte(`"requested_epochs": 200`), []byte(`"requested_epochs": 201`), 1)
			}
			return original.writeFile(path, data)
		}
	}

	for name, inject := range failures {
		filesystem = original
		inject()

		parent := t.TempDir()
		dir := filepath.Join(parent, "run")

		err := Publish(dir, bundle)
		if name == "silently corrupted write" {
			if err == nil {
				t.Errorf("%s: Publish() error = nil, want a verification failure", name)
			}
		} else if !errors.Is(err, injected) {
			t.Errorf("%s: Publish() error = %v, want the injected failure", name, err)
		}

		if got := names(t, parent); len(got) != 0 {
			t.Errorf("%s: parent directory = %v, want no run and no staging directory", name, got)
		}
	}
}

// rewrite publishes a valid run, applies change to its files, and rewrites
// the manifest when fixManifest is set, so only the bindings can fail.
func rewrite(t *testing.T, change func(dir string), fixManifest bool) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "run")
	if err := Publish(dir, newBundle(t, 42)); err != nil {
		t.Fatal(err)
	}

	change(dir)

	if fixManifest {
		var bundle Bundle
		for name, target := range map[string]*[]byte{ModelFile: &bundle.Model, ReportFile: &bundle.Report, ConfigFile: &bundle.Config} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			*target = data
		}

		manifest, err := json.MarshalIndent(NewManifest(bundle), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ManifestFile), manifest, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func edit(t *testing.T, path, old, new string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsTamperedRuns(t *testing.T) {
	other := newBundle(t, 7)

	tests := map[string]string{
		"edited report": rewrite(t, func(dir string) {
			edit(t, filepath.Join(dir, ReportFile), `"requested_epochs": 200`, `"requested_epochs": 201`)
		}, false),
		"edited report with a matching manifest": rewrite(t, func(dir string) {
			edit(t, filepath.Join(dir, ReportFile), `"requested_epochs": 200`, `"requested_epochs": 201`)
		}, true),
		"model of another run": rewrite(t, func(dir string) {
			os.WriteFile(filepath.Join(dir, ModelFile), other.Model, 0o644)
		}, true),
		"truncated model": rewrite(t, func(dir string) {
			data, _ := os.ReadFile(filepath.Join(dir, ModelFile))
			os.WriteFile(filepath.Join(dir, ModelFile), data[:len(data)/2], 0o644)
		}, true),
		"manifest hash changed": rewrite(t, func(dir string) {
			manifest, _ := os.ReadFile(filepath.Join(dir, ManifestFile))
			var value Manifest
			json.Unmarshal(manifest, &value)
			value.Files[0].SHA256 = strings.Repeat("0", 64)
			data, _ := json.Marshal(value)
			os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o644)
		}, false),
		"missing config": rewrite(t, func(dir string) {
			os.Remove(filepath.Join(dir, ConfigFile))
		}, false),
		"extra file": rewrite(t, func(dir string) {
			os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
		}, false),
		"unknown manifest member": rewrite(t, func(dir string) {
			edit(t, filepath.Join(dir, ManifestFile), `"manifest_version"`, `"signed_by": "nobody", "manifest_version"`)
		}, false),
		"unsupported manifest version": rewrite(t, func(dir string) {
			edit(t, filepath.Join(dir, ManifestFile), `pix-risk-run-v1`, `pix-risk-run-v2`)
		}, false),
	}

	for name, dir := range tests {
		if _, err := Verify(dir); err == nil {
			t.Errorf("%s: Verify() error = nil, want an error", name)
		}
	}
}

func TestPromoteIsExplicitAndAtomic(t *testing.T) {
	bundle := newBundle(t, 42)
	dir := filepath.Join(t.TempDir(), "run")
	if err := Publish(dir, bundle); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "model.json")

	sum, err := Promote(dir, target, false)
	if err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	if data, _ := os.ReadFile(target); !bytes.Equal(data, bundle.Model) || sum != sha256Hex(bundle.Model) {
		t.Error("the promoted file is not the run's model")
	}

	// Without replace, the promoted model is kept.
	otherDir := filepath.Join(t.TempDir(), "run")
	other := newBundle(t, 7)
	if err := Publish(otherDir, other); err != nil {
		t.Fatal(err)
	}
	original := filesystem
	filesystem.mkdirTemp = func(string, string) (string, error) {
		t.Error("a refused promotion staged files")
		return "", errors.New("unexpected staging")
	}
	if _, err := Promote(otherDir, target, false); !errors.Is(err, ErrExists) {
		t.Errorf("Promote() over a promoted model: error = %v, want ErrExists", err)
	}
	filesystem = original
	if data, _ := os.ReadFile(target); !bytes.Equal(data, bundle.Model) {
		t.Error("a refused promotion changed the promoted model")
	}

	if _, err := Promote(otherDir, target, true); err != nil {
		t.Fatalf("Promote() with replace: %v", err)
	}
	if data, _ := os.ReadFile(target); !bytes.Equal(data, other.Model) {
		t.Error("replace did not promote the other model")
	}

	// A tampered run is never promoted.
	edit(t, filepath.Join(dir, ReportFile), `"requested_epochs": 200`, `"requested_epochs": 201`)
	fresh := filepath.Join(t.TempDir(), "model.json")
	if _, err := Promote(dir, fresh, false); err == nil {
		t.Error("Promote() of a tampered run: error = nil")
	}
	if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused promotion wrote a file")
	}
}

func TestPromoteLeavesNothingBehindOnFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	if err := Publish(dir, newBundle(t, 42)); err != nil {
		t.Fatal(err)
	}

	original := filesystem
	defer func() { filesystem = original }()
	filesystem.rename = func(string, string) error { return errors.New("injected failure") }

	parent := t.TempDir()
	if _, err := Promote(dir, filepath.Join(parent, "model.json"), false); err == nil {
		t.Fatal("Promote() error = nil, want the injected failure")
	}

	if got := names(t, parent); len(got) != 0 {
		t.Errorf("target directory = %v, want nothing written", got)
	}
}

// A manifest that declares a model or configuration above its size limit
// is refused before the file is read, and a file longer than its manifest
// entry is never read past that entry.
func TestVerifyBoundsWhatItReads(t *testing.T) {
	declare := func(index int, bytes int64) func(dir string) {
		return func(dir string) {
			manifest, _ := os.ReadFile(filepath.Join(dir, ManifestFile))
			var value Manifest
			json.Unmarshal(manifest, &value)
			value.Files[index].Bytes = bytes
			data, _ := json.Marshal(value)
			os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o644)
		}
	}

	tests := map[string]struct {
		dir  string
		want string
	}{
		"model above the limit":  {rewrite(t, declare(0, modelartifact.MaxArtifactSize+1), false), "above the limit"},
		"config above the limit": {rewrite(t, declare(2, trainingconfig.MaxDocumentSize+1), false), "above the limit"},
		"longer report": {rewrite(t, func(dir string) {
			f, _ := os.OpenFile(filepath.Join(dir, ReportFile), os.O_APPEND|os.O_WRONLY, 0)
			f.Write([]byte("  "))
			f.Close()
		}, false), "does not match its manifest entry"},
	}

	for name, test := range tests {
		if _, err := Verify(test.dir); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: Verify() error = %v, want %q", name, err, test.want)
		}
	}
}
