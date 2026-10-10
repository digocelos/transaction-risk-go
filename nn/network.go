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

// Predict computes outputs without changing the network parameters.
func (n *Network) Predict(inputs []float64) ([]float64, error) {
	if len(inputs) != n.inputSize {
		return nil, fmt.Errorf(
			"expected %d inputs, got %d",
			n.inputSize,
			len(inputs),
		)
	}

	current := inputs

	for _, layer := range n.layers {
		outputs := make([]float64, len(layer.biases))

		for neuron, weights := range layer.weights {
			sum := layer.biases[neuron]

			for input, weight := range weights {
				sum += current[input] * weight
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
