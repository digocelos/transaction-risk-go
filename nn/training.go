package nn

import (
	"fmt"
	"math"
)

// Sample contains model inputs and their expected outputs.
type Sample struct {
	Inputs  []float64
	Targets []float64
}

// TrainBatch performs one gradient descent update.
// It returns the mean squared error measured before the update.
func (n *Network) TrainBatch(
	samples []Sample,
	learningRate float64,
) (float64, error) {
	if len(samples) == 0 {
		return 0, fmt.Errorf("training batch cannot be empty")
	}

	if learningRate <= 0 ||
		math.IsNaN(learningRate) ||
		math.IsInf(learningRate, 0) {
		return 0, fmt.Errorf("learning rate must be finite and positive")
	}

	outputSize := len(n.layers[len(n.layers)-1].biases)

	// Validate all samples before changing any parameters.
	for index, sample := range samples {
		if len(sample.Inputs) != n.inputSize {
			return 0, fmt.Errorf(
				"sample %d: expected %d inputs, got %d",
				index,
				n.inputSize,
				len(sample.Inputs),
			)
		}

		if len(sample.Targets) != outputSize {
			return 0, fmt.Errorf(
				"sample %d: expected %d targets, got %d",
				index,
				outputSize,
				len(sample.Targets),
			)
		}

		for _, input := range sample.Inputs {
			if math.IsNaN(input) || math.IsInf(input, 0) {
				return 0, fmt.Errorf(
					"sample %d: inputs must be finite",
					index,
				)
			}
		}

		for _, target := range sample.Targets {
			if math.IsNaN(target) || target < 0 || target > 1 {
				return 0, fmt.Errorf(
					"sample %d: targets must be between 0 and 1",
					index,
				)
			}
		}
	}

	// Allocate gradient accumulators with the same shapes as the layers.
	gradients := make([]denseLayer, len(n.layers))

	for layerIndex, layer := range n.layers {
		gradients[layerIndex] = denseLayer{
			weights: make([][]float64, len(layer.weights)),
			biases:  make([]float64, len(layer.biases)),
		}

		for neuron, weights := range layer.weights {
			gradients[layerIndex].weights[neuron] =
				make([]float64, len(weights))
		}
	}

	totalLoss := 0.0

	for _, sample := range samples {
		// Store each layer's activations for backpropagation.
		activations := make([][]float64, len(n.layers)+1)
		activations[0] = sample.Inputs

		for layerIndex, layer := range n.layers {
			outputs := make([]float64, len(layer.biases))

			for neuron, weights := range layer.weights {
				sum := layer.biases[neuron]

				for input, weight := range weights {
					sum += activations[layerIndex][input] * weight
				}

				outputs[neuron] = sigmoid(sum)
			}

			activations[layerIndex+1] = outputs
		}

		// Compute output-layer deltas.
		outputs := activations[len(n.layers)]
		deltas := make([]float64, outputSize)

		for neuron, output := range outputs {
			difference := output - sample.Targets[neuron]

			totalLoss += difference * difference

			deltas[neuron] = 2 * difference *
				output * (1 - output) / float64(outputSize)
		}

		// Walk backward through all layers.
		for layerIndex := len(n.layers) - 1; layerIndex >= 0; layerIndex-- {
			layer := n.layers[layerIndex]
			layerInputs := activations[layerIndex]

			for neuron, delta := range deltas {
				gradients[layerIndex].biases[neuron] += delta

				for input, value := range layerInputs {
					gradients[layerIndex].weights[neuron][input] +=
						delta * value
				}
			}

			if layerIndex > 0 {
				previousDeltas := make([]float64, len(layerInputs))

				for input, activation := range layerInputs {
					influence := 0.0

					for neuron, delta := range deltas {
						influence += layer.weights[neuron][input] * delta
					}

					previousDeltas[input] = influence *
						activation * (1 - activation)
				}

				deltas = previousDeltas
			}
		}
	}

	// Apply averaged gradients after processing the entire batch.
	scale := learningRate / float64(len(samples))

	for layerIndex := range n.layers {
		for neuron := range n.layers[layerIndex].biases {
			n.layers[layerIndex].biases[neuron] -=
				scale * gradients[layerIndex].biases[neuron]

			for input := range n.layers[layerIndex].weights[neuron] {
				n.layers[layerIndex].weights[neuron][input] -=
					scale * gradients[layerIndex].weights[neuron][input]
			}
		}
	}

	return totalLoss /
		(float64(len(samples)) * float64(outputSize)), nil
}
