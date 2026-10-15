// Package nn implements the dense sigmoid network the toolkit trains:
// creation from a seed, prediction, full-batch gradient descent, and the
// layers a model artifact stores. Its parameters are saved only inside a
// pix-risk-model-v1 artifact (package modelartifact).
package nn

import (
	"fmt"
	"math"
	"math/rand"
)

// denseLayer connects every input to every neuron in the layer.
type denseLayer struct {
	weights [][]float64
	biases  []float64
}

// Network is a fully connected neural network with sigmoid activations.
type Network struct {
	inputSize int
	layers    []denseLayer
}

// NewNetwork creates a network with reproducible initial weights.
func NewNetwork(seed int64, sizes ...int) (*Network, error) {
	if len(sizes) < 2 {
		return nil, fmt.Errorf("at least input and output sizes are required")
	}

	for _, size := range sizes {
		if size <= 0 {
			return nil, fmt.Errorf("layer sizes must be positive")
		}
	}

	rng := rand.New(rand.NewSource(seed))

	network := &Network{
		inputSize: sizes[0],
	}

	for index := 1; index < len(sizes); index++ {
		inputCount := sizes[index-1]
		neuronCount := sizes[index]

		layer := denseLayer{
			weights: make([][]float64, neuronCount),
			biases:  make([]float64, neuronCount),
		}

		for neuron := 0; neuron < neuronCount; neuron++ {
			layer.weights[neuron] = make([]float64, inputCount)

			for input := 0; input < inputCount; input++ {
				layer.weights[neuron][input] = rng.Float64() - 0.5
			}
		}

		network.layers = append(network.layers, layer)
	}

	return network, nil
}

// Predict computes outputs without changing the network parameters. It
// rejects non-finite inputs.
func (n *Network) Predict(inputs []float64) ([]float64, error) {
	if len(inputs) != n.inputSize {
		return nil, fmt.Errorf(
			"expected %d inputs, got %d",
			n.inputSize,
			len(inputs),
		)
	}

	for _, input := range inputs {
		if math.IsNaN(input) || math.IsInf(input, 0) {
			return nil, fmt.Errorf("inputs must be finite")
		}
	}

	current := inputs

	for _, layer := range n.layers {
		outputs := make([]float64, len(layer.biases))

		for neuron, weights := range layer.weights {
			sum := layer.biases[neuron]

			for input, weight := range weights {
				// The explicit conversion rounds the product before the
				// addition. Without it, Go may fuse both operations into
				// one fused multiply-add on some architectures, such as
				// arm64, and scores would differ in their last bits.
				sum += float64(current[input] * weight)
			}

			outputs[neuron] = sigmoid(sum)
		}

		current = outputs
	}

	return current, nil
}

func sigmoid(value float64) float64 {
	return 1 / (1 + math.Exp(-value))
}

// Clone returns an independent copy of the network: training either one
// never changes the other.
func (n *Network) Clone() *Network {
	copied := &Network{inputSize: n.inputSize, layers: make([]denseLayer, len(n.layers))}

	for i, layer := range n.layers {
		copied.layers[i] = denseLayer{
			weights: make([][]float64, len(layer.weights)),
			biases:  append([]float64(nil), layer.biases...),
		}
		for neuron, weights := range layer.weights {
			copied.layers[i].weights[neuron] = append([]float64(nil), weights...)
		}
	}

	return copied
}

// Layer holds the parameters of one dense layer: one row of weights per
// neuron, one weight per input, and one bias per neuron.
type Layer struct {
	Weights [][]float64 `json:"weights"`
	Biases  []float64   `json:"biases"`
}

// InputSize returns the number of inputs the network takes.
func (n *Network) InputSize() int {
	return n.inputSize
}

// Layers returns a copy of the network parameters, in layer order.
func (n *Network) Layers() []Layer {
	layers := make([]Layer, len(n.layers))
	for i, layer := range n.Clone().layers {
		layers[i] = Layer{Weights: layer.weights, Biases: layer.biases}
	}
	return layers
}

// FromLayers builds a network from its parameters. It requires at least one
// layer, at least one neuron per layer, as many weights per neuron as the
// previous layer has outputs, and finite parameters. The network keeps a
// copy of the parameters.
func FromLayers(inputSize int, layers []Layer) (*Network, error) {
	if inputSize <= 0 || len(layers) == 0 {
		return nil, fmt.Errorf("invalid network dimensions")
	}

	network := &Network{inputSize: inputSize, layers: make([]denseLayer, len(layers))}
	expectedInputs := inputSize

	for i, layer := range layers {
		if len(layer.Biases) == 0 || len(layer.Weights) != len(layer.Biases) {
			return nil, fmt.Errorf("layer %d: invalid neuron count", i)
		}

		for neuron, weights := range layer.Weights {
			if len(weights) != expectedInputs {
				return nil, fmt.Errorf("layer %d, neuron %d: expected %d weights, got %d",
					i, neuron, expectedInputs, len(weights))
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

		network.layers[i] = denseLayer{weights: layer.Weights, biases: layer.Biases}
		expectedInputs = len(layer.Biases)
	}

	return network.Clone(), nil
}
