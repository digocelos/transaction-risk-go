package nn

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	committedModel = "../model.json"
	featureSchema  = "transaction-features-v1"
)

// exampleInputs are the features of the README's cmd/predict example, whose
// score the specification fixes.
var exampleInputs = []float64{1, 0.1, 1, 0}

const exampleScore = 0.18734535214416673

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

// Model format version 2, which carried provenance, is replaced by the
// pix-risk-model-v1 artifact (ADR 0016) and is no longer read.
func TestLoadRejectsFormatVersion2(t *testing.T) {
	committed, err := os.ReadFile(committedModel)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "model.json")
	data := strings.Replace(string(committed), `"format_version": 1`, `"format_version": 2`, 1)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path, featureSchema); err == nil || !strings.Contains(err.Error(), "format version") {
		t.Errorf("Load() error = %v, want an unsupported format version", err)
	}
}

func TestLayersRoundTrip(t *testing.T) {
	network, err := Load(committedModel, featureSchema)
	if err != nil {
		t.Fatal(err)
	}

	rebuilt, err := FromLayers(network.InputSize(), network.Layers())
	if err != nil {
		t.Fatalf("FromLayers() error = %v", err)
	}

	if got := score(t, rebuilt, exampleInputs); got != exampleScore {
		t.Errorf("score = %v, want %v", got, exampleScore)
	}

	// Neither the returned layers nor the layers passed in are shared.
	layers := network.Layers()
	layers[0].Weights[0][0] = 99
	if got := score(t, network, exampleInputs); got != exampleScore {
		t.Error("changing Layers() changed the network")
	}

	built, err := FromLayers(network.InputSize(), layers)
	if err != nil {
		t.Fatal(err)
	}
	layers[0].Biases[0] = 99
	if built.Layers()[0].Biases[0] == 99 {
		t.Error("FromLayers() kept a reference to its input")
	}
}

func TestFromLayersRejectsInvalidParameters(t *testing.T) {
	valid := func() []Layer {
		return []Layer{
			{Weights: [][]float64{{0.1, 0.2}, {0.3, 0.4}, {0.5, 0.6}}, Biases: []float64{0, 0, 0}},
			{Weights: [][]float64{{0.7, 0.8, 0.9}}, Biases: []float64{0}},
		}
	}

	if _, err := FromLayers(2, valid()); err != nil {
		t.Fatalf("FromLayers() of valid layers: %v", err)
	}

	tests := map[string]func(*int, *[]Layer){
		"no layer":          func(_ *int, l *[]Layer) { *l = nil },
		"zero inputs":       func(n *int, _ *[]Layer) { *n = 0 },
		"wrong input count": func(n *int, _ *[]Layer) { *n = 3 },
		"missing bias":      func(_ *int, l *[]Layer) { (*l)[0].Biases = (*l)[0].Biases[:2] },
		"empty layer":       func(_ *int, l *[]Layer) { (*l)[1] = Layer{} },
		"short weights":     func(_ *int, l *[]Layer) { (*l)[1].Weights[0] = []float64{0.7, 0.8} },
		"NaN weight":        func(_ *int, l *[]Layer) { (*l)[0].Weights[1][0] = math.NaN() },
		"infinite bias":     func(_ *int, l *[]Layer) { (*l)[1].Biases[0] = math.Inf(-1) },
	}

	for name, change := range tests {
		inputs, layers := 2, valid()
		change(&inputs, &layers)

		if _, err := FromLayers(inputs, layers); err == nil {
			t.Errorf("%s: FromLayers() error = nil, want an error", name)
		}
	}
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
