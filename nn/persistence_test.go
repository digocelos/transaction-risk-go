package nn

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	committedModel = "../model.json"
	featureSchema  = "transaction-features-v1"
)

// exampleInputs are the features of the README's cmd/predict example, whose
// score the specification fixes.
var exampleInputs = []float64{1, 0.1, 1, 0}

const exampleScore = 0.18734535214416673

func validProvenance() Provenance {
	brasilia := time.FixedZone("", -3*60*60)

	return Provenance{
		Algorithm:              AlgorithmFullBatchGradientDescent,
		Seed:                   42,
		Epochs:                 5000,
		LearningRate:           1,
		TrainingExampleVersion: "pix-training-example-v1",
		DatasetSHA256:          strings.Repeat("ab", 32),
		DatasetBytes:           16688,
		Records:                RecordCounts{Total: 7, Usable: 3, Invalid: 0, Insufficient: 2, Immature: 2},
		TrainEnd:               time.Date(2025, 6, 30, 20, 59, 59, 0, brasilia),
		ValidationEnd:          time.Date(2025, 9, 30, 23, 59, 59, 0, time.UTC),
		Threshold:              0.5,
		ReportSHA256:           strings.Repeat("0f", 32),
	}
}

func score(t *testing.T, network *Network, inputs []float64) float64 {
	t.Helper()

	outputs, err := network.Predict(inputs)
	if err != nil {
		t.Fatal(err)
	}

	return outputs[0]
}

func TestLoadKeepsReadingFormatVersion1(t *testing.T) {
	network, err := Load(committedModel, featureSchema)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := score(t, network, exampleInputs); got != exampleScore {
		t.Errorf("score = %v, want %v", got, exampleScore)
	}

	_, provenance, err := LoadWithProvenance(committedModel, featureSchema)
	if err != nil {
		t.Fatalf("LoadWithProvenance() error = %v", err)
	}

	if provenance != nil {
		t.Errorf("provenance = %+v, want none for format version 1", provenance)
	}
}

func TestSaveStillWritesFormatVersion1(t *testing.T) {
	network, err := Load(committedModel, featureSchema)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "model.json")
	if err := network.Save(path, featureSchema); err != nil {
		t.Fatal(err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	committed, err := os.ReadFile(committedModel)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(saved, committed) {
		t.Error("Save() no longer reproduces the committed format version 1 bytes")
	}
}

func TestProvenanceRoundTrip(t *testing.T) {
	network, err := Load(committedModel, featureSchema)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "trained.json")
	if err := network.SaveWithProvenance(path, featureSchema, validProvenance()); err != nil {
		t.Fatalf("SaveWithProvenance() error = %v", err)
	}

	loaded, provenance, err := LoadWithProvenance(path, featureSchema)
	if err != nil {
		t.Fatalf("LoadWithProvenance() error = %v", err)
	}

	for _, inputs := range [][]float64{exampleInputs, {0.9, 0.45, 1, 1}, {0.09, 0.1, 0, 0}} {
		if got, want := score(t, loaded, inputs), score(t, network, inputs); got != want {
			t.Errorf("score(%v) = %v after the round trip, want %v", inputs, got, want)
		}
	}

	want := validProvenance()
	want.TrainEnd = want.TrainEnd.UTC()

	if provenance == nil || !reflect.DeepEqual(*provenance, want) {
		t.Errorf("provenance = %+v\nwant         %+v", provenance, want)
	}

	if _, err := Load(path, featureSchema); err != nil {
		t.Errorf("Load() of a format version 2 file: %v", err)
	}
}

func TestMarshalWithProvenanceIsDeterministic(t *testing.T) {
	network, err := Load(committedModel, featureSchema)
	if err != nil {
		t.Fatal(err)
	}

	first, err := network.MarshalWithProvenance(featureSchema, validProvenance())
	if err != nil {
		t.Fatal(err)
	}

	second, err := network.MarshalWithProvenance(featureSchema, validProvenance())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Error("two encodings of the same artifact differ")
	}

	for _, want := range []string{
		`"format_version": 2`,
		`"algorithm": "full-batch-gradient-descent"`,
		`"train_end": "2025-06-30T23:59:59Z"`,
		`"report_sha256": "` + strings.Repeat("0f", 32) + `"`,
	} {
		if !strings.Contains(string(first), want) {
			t.Errorf("artifact does not contain %s", want)
		}
	}
}

func TestMarshalWithProvenanceRejectsInvalidMetadata(t *testing.T) {
	network, err := Load(committedModel, featureSchema)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]func(*Provenance){
		"unknown algorithm":       func(p *Provenance) { p.Algorithm = "random-forest" },
		"zero epochs":             func(p *Provenance) { p.Epochs = 0 },
		"zero learning rate":      func(p *Provenance) { p.LearningRate = 0 },
		"infinite learning rate":  func(p *Provenance) { p.LearningRate = inf() },
		"threshold above 1":       func(p *Provenance) { p.Threshold = 1.5 },
		"missing example version": func(p *Provenance) { p.TrainingExampleVersion = "" },
		"uppercase dataset hash":  func(p *Provenance) { p.DatasetSHA256 = strings.Repeat("AB", 32) },
		"short report hash":       func(p *Provenance) { p.ReportSHA256 = "0f" },
		"empty dataset":           func(p *Provenance) { p.DatasetBytes = 0 },
		"counts do not add up":    func(p *Provenance) { p.Records.Total = 8 },
		"negative count":          func(p *Provenance) { p.Records.Immature = -1; p.Records.Total = 4 },
		"no usable record":        func(p *Provenance) { p.Records = RecordCounts{Total: 1, Insufficient: 1} },
		"reversed boundaries":     func(p *Provenance) { p.TrainEnd, p.ValidationEnd = p.ValidationEnd, p.TrainEnd },
		"missing train_end":       func(p *Provenance) { p.TrainEnd = time.Time{} },
	}

	for name, change := range tests {
		provenance := validProvenance()
		change(&provenance)

		if _, err := network.MarshalWithProvenance(featureSchema, provenance); err == nil {
			t.Errorf("%s: MarshalWithProvenance() error = nil, want an error", name)
		}
	}

	if _, err := network.MarshalWithProvenance("", validProvenance()); err == nil {
		t.Error("empty feature schema: MarshalWithProvenance() error = nil, want an error")
	}
}

func TestLoadRejectsMalformedProvenance(t *testing.T) {
	network, err := Load(committedModel, featureSchema)
	if err != nil {
		t.Fatal(err)
	}

	valid, err := network.MarshalWithProvenance(featureSchema, validProvenance())
	if err != nil {
		t.Fatal(err)
	}

	replace := func(old, new string) string {
		t.Helper()
		if strings.Count(string(valid), old) != 1 {
			t.Fatalf("%q must occur exactly once in the artifact", old)
		}
		return strings.Replace(string(valid), old, new, 1)
	}

	tests := map[string]string{
		"unsupported format version": replace(`"format_version": 2`, `"format_version": 3`),
		"unknown provenance member":  replace(`"seed": 42`, `"seed": 42, "operator": "alice"`),
		"unknown top-level member":   replace(`"format_version": 2`, `"format_version": 2, "notes": ""`),
		"duplicate member":           replace(`"seed": 42`, `"seed": 42, "seed": 7`),
		"fractional epochs":          replace(`"epochs": 5000`, `"epochs": 5000.5`),
		"zero epochs":                replace(`"epochs": 5000`, `"epochs": 0`),
		"threshold above 1":          replace(`"threshold": 0.5`, `"threshold": 2`),
		"bad dataset hash":           replace(`"dataset_sha256": "`, `"dataset_sha256": "zz`),
		"unknown algorithm":          replace(`"full-batch-gradient-descent"`, `"genetic"`),
		"invalid timestamp":          replace(`"2025-06-30T23:59:59Z"`, `"yesterday"`),
		"trailing data":              string(valid) + "{}",
	}

	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "model.json")
			if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, _, err := LoadWithProvenance(path, featureSchema); err == nil {
				t.Error("LoadWithProvenance() error = nil, want an error")
			}

			if _, err := Load(path, featureSchema); err == nil {
				t.Error("Load() error = nil, want an error")
			}
		})
	}

	path := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(path, valid, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := LoadWithProvenance(path, "transaction-features-v2"); err == nil {
		t.Error("feature schema mismatch: error = nil, want an error")
	}

	// A format version 2 file without provenance.
	committed, err := os.ReadFile(committedModel)
	if err != nil {
		t.Fatal(err)
	}

	withoutProvenance := strings.Replace(string(committed), `"format_version": 1`, `"format_version": 2`, 1)
	if err := os.WriteFile(path, []byte(withoutProvenance), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := LoadWithProvenance(path, featureSchema); err == nil {
		t.Error("missing provenance: error = nil, want an error")
	}
}

func inf() float64 {
	return math.Inf(1)
}

// Format version 1 keeps its original, lenient decoding: members it does
// not know are ignored, as before format version 2 existed.
func TestLoadFormatVersion1IgnoresUnknownMembers(t *testing.T) {
	committed, err := os.ReadFile(committedModel)
	if err != nil {
		t.Fatal(err)
	}

	extended := strings.Replace(string(committed), `"format_version": 1,`,
		`"format_version": 1, "trained_by": "cmd/demo",`, 1)

	path := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(path, []byte(extended), 0o644); err != nil {
		t.Fatal(err)
	}

	network, err := Load(path, featureSchema)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := score(t, network, exampleInputs); got != exampleScore {
		t.Errorf("score = %v, want %v", got, exampleScore)
	}
}
