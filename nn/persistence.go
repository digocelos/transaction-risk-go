package nn

import (
	"encoding/json"
	"fmt"
	"os"
)

// AlgorithmFullBatchGradientDescent identifies TrainBatch: full-batch
// gradient descent on mean squared error.
const AlgorithmFullBatchGradientDescent = "full-batch-gradient-descent"

// storedLayer contains the parameters of a dense layer.
type storedLayer struct {
	Weights [][]float64 `json:"weights"`
	Biases  []float64   `json:"biases"`
}

// storedModel describes the serialized network format.
type storedModel struct {
	FormatVersion int           `json:"format_version"`
	FeatureSchema string        `json:"feature_schema"`
	Activation    string        `json:"activation"`
	InputSize     int           `json:"input_size"`
	Layers        []storedLayer `json:"layers"`
}

// Save writes network parameters and compatibility metadata to a file.
func (n *Network) Save(path, featureSchema string) error {
	if n.inputSize <= 0 || len(n.layers) == 0 {
		return fmt.Errorf("cannot save an empty network")
	}

	if featureSchema == "" {
		return fmt.Errorf("feature schema cannot be empty")
	}

	stored := storedModel{
		FormatVersion: 1,
		FeatureSchema: featureSchema,
		Activation:    "sigmoid",
		InputSize:     n.inputSize,
		Layers:        make([]storedLayer, len(n.layers)),
	}

	for i, layer := range n.layers {
		stored.Layers[i] = storedLayer{
			Weights: layer.weights,
			Biases:  layer.biases,
		}
	}

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode model: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write model: %w", err)
	}

	return nil
}

// Load reads and validates a model in model format version 1 against the
// expected feature schema. Trained pix-risk-v1 models use the artifact of
// the modelartifact package instead (ADR 0016).
func Load(path, expectedFeatureSchema string) (*Network, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read model: %w", err)
	}

	var stored storedModel
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode model: %w", err)
	}

	if stored.FormatVersion != 1 {
		return nil, fmt.Errorf("unsupported model format version: %d", stored.FormatVersion)
	}

	return buildNetwork(stored, expectedFeatureSchema)
}

// buildNetwork validates stored parameters against the expected feature
// schema.
func buildNetwork(stored storedModel, expectedFeatureSchema string) (*Network, error) {
	if stored.Activation != "sigmoid" {
		return nil, fmt.Errorf(
			"unsupported activation: %q",
			stored.Activation,
		)
	}

	if expectedFeatureSchema == "" ||
		stored.FeatureSchema != expectedFeatureSchema {
		return nil, fmt.Errorf(
			"feature schema mismatch: expected %q, got %q",
			expectedFeatureSchema,
			stored.FeatureSchema,
		)
	}

	layers := make([]Layer, len(stored.Layers))
	for i, layer := range stored.Layers {
		layers[i] = Layer(layer)
	}

	return FromLayers(stored.InputSize, layers)
}
