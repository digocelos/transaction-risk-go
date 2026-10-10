package nn

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

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

// Load reads and validates a model against the expected feature schema.
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
		return nil, fmt.Errorf(
			"unsupported model format version: %d",
			stored.FormatVersion,
		)
	}

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

	if stored.InputSize <= 0 || len(stored.Layers) == 0 {
		return nil, fmt.Errorf("invalid network dimensions")
	}

	network := &Network{
		inputSize: stored.InputSize,
		layers:    make([]denseLayer, len(stored.Layers)),
	}

	expectedInputs := stored.InputSize

	for i, layer := range stored.Layers {
		if len(layer.Biases) == 0 ||
			len(layer.Weights) != len(layer.Biases) {
			return nil, fmt.Errorf(
				"layer %d: invalid neuron count",
				i,
			)
		}

		for neuron, weights := range layer.Weights {
			if len(weights) != expectedInputs {
				return nil, fmt.Errorf(
					"layer %d, neuron %d: expected %d weights, got %d",
					i,
					neuron,
					expectedInputs,
					len(weights),
				)
			}

			for _, weight := range weights {
				if math.IsNaN(weight) || math.IsInf(weight, 0) {
					return nil, fmt.Errorf("weights must be finite")
				}
			}
		}

		for _, bias := range layer.Biases {
			if math.IsNaN(bias) || math.IsInf(bias, 0) {
				return nil, fmt.Errorf("biases must be finite")
			}
		}

		network.layers[i] = denseLayer{
			weights: layer.Weights,
			biases:  layer.Biases,
		}

		expectedInputs = len(layer.Biases)
	}

	return network, nil
}
