package nn

import (
	"context"
	"fmt"
	"math"
)

// cancellationInterval is the number of samples between two checks of the
// context in TrainBatchContext.
const cancellationInterval = 1024

// Sample contains model inputs and their expected outputs.
type Sample struct {
	Inputs  []float64
	Targets []float64
}

// Loss is the function a training step minimizes, averaged over samples
// and outputs.
type Loss string

const (
	// MeanSquaredError is the mean of (output − target)².
	MeanSquaredError Loss = "mean-squared-error"
	// BinaryCrossEntropy is the mean of −(target·ln(output) +
	// (1 − target)·ln(1 − output)), the negative log-likelihood of a
	// Bernoulli target. Its gradient with respect to a sigmoid
	// pre-activation is output − target, which does not vanish when the
	// output saturates. For the reported value, outputs are clamped to
	// [LossEpsilon, 1 − LossEpsilon], so a saturated output gives a large
	// finite loss instead of infinity.
	BinaryCrossEntropy Loss = "binary-cross-entropy"
)

// LossEpsilon bounds the outputs inside the logarithms of the binary
// cross-entropy value.
const LossEpsilon = 1e-15

// Options configure a training step. The zero value is the mean squared
// error without weight decay, the step TrainBatch performs.
type Options struct {
	// Loss is the function to minimize; "" is MeanSquaredError.
	Loss Loss
	// L2 is the weight decay coefficient: each weight, not bias, also
	// receives the gradient of (L2 / 2)·weight², that is L2·weight, so the
	// update is weight − learningRate·(gradient + L2·weight). It must be
	// finite and not negative; 0 is no decay.
	L2 float64
}

func (o Options) validate() error {
	switch {
	case o.Loss != "" && o.Loss != MeanSquaredError && o.Loss != BinaryCrossEntropy:
		return fmt.Errorf("unsupported loss %q", o.Loss)
	case o.L2 < 0 || math.IsNaN(o.L2) || math.IsInf(o.L2, 0):
		return fmt.Errorf("the weight decay must be finite and not negative")
	}
	return nil
}

// LossValue returns the loss of one output against its target, before
// averaging: the squared difference, or the clamped binary cross-entropy.
func LossValue(loss Loss, output, target float64) float64 {
	if loss == BinaryCrossEntropy {
		clamped := math.Min(math.Max(output, LossEpsilon), 1-LossEpsilon)
		return -(target*math.Log(clamped) + (1-target)*math.Log(1-clamped))
	}
	difference := output - target
	return difference * difference
}

// TrainBatch performs one gradient descent update on the mean squared
// error without weight decay. It returns the loss measured before the
// update.
//
// TrainBatch changes no parameter unless the whole step is finite: it
// fails when a parameter, an input, a gradient, or an updated parameter is
// not finite, and the network keeps its previous parameters.
func (n *Network) TrainBatch(samples []Sample, learningRate float64) (float64, error) {
	return n.Train(context.Background(), samples, learningRate, Options{})
}

// TrainBatchContext is TrainBatch with cancellation: it checks ctx before
// the first sample and every 1,024 samples, and a canceled step returns
// ctx's error without changing any parameter.
func (n *Network) TrainBatchContext(
	ctx context.Context,
	samples []Sample,
	learningRate float64,
) (float64, error) {
	return n.Train(ctx, samples, learningRate, Options{})
}

// Train performs one full-batch gradient descent update with the given
// options: the configured loss, averaged over samples and outputs, plus
// weight decay. It returns the loss measured before the update, without
// the decay term, checks ctx before the first sample and every 1,024
// samples, and changes no parameter unless the whole step is finite.
func (n *Network) Train(
	ctx context.Context,
	samples []Sample,
	learningRate float64,
	options Options,
) (float64, error) {
	if len(samples) == 0 {
		return 0, fmt.Errorf("training batch cannot be empty")
	}

	if learningRate <= 0 ||
		math.IsNaN(learningRate) ||
		math.IsInf(learningRate, 0) {
		return 0, fmt.Errorf("learning rate must be finite and positive")
	}

	if err := options.validate(); err != nil {
		return 0, err
	}
	crossEntropy := options.Loss == BinaryCrossEntropy

	outputSize := len(n.layers[len(n.layers)-1].biases)

	for layerIndex, layer := range n.layers {
		if !allFinite(layer) {
			return 0, fmt.Errorf("layer %d: a parameter is not finite", layerIndex)
		}
	}

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

	for index, sample := range samples {
		if index%cancellationInterval == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}

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
			target := sample.Targets[neuron]
			totalLoss += LossValue(options.Loss, output, target)

			// The derivative of the loss with respect to the neuron's
			// pre-activation, divided by the number of outputs the loss
			// averages over.
			if crossEntropy {
				deltas[neuron] = (output - target) / float64(outputSize)
			} else {
				deltas[neuron] = 2 * (output - target) *
					output * (1 - output) / float64(outputSize)
			}
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

	// A non-finite output makes every gradient of its neuron non-finite,
	// so finite gradients also guarantee a finite loss.
	for layerIndex, gradient := range gradients {
		if !allFinite(gradient) {
			return 0, fmt.Errorf("training diverged: a gradient of layer %d is not finite", layerIndex)
		}
	}

	// Compute averaged updates for the entire batch, and apply them only
	// when every updated parameter is finite.
	scale := learningRate / float64(len(samples))
	updated := make([]denseLayer, len(n.layers))

	for layerIndex, layer := range n.layers {
		updated[layerIndex] = denseLayer{
			weights: make([][]float64, len(layer.weights)),
			biases:  make([]float64, len(layer.biases)),
		}

		for neuron := range layer.biases {
			updated[layerIndex].biases[neuron] = layer.biases[neuron] -
				scale*gradients[layerIndex].biases[neuron]

			updated[layerIndex].weights[neuron] = make([]float64, len(layer.weights[neuron]))
			for input, weight := range layer.weights[neuron] {
				updated[layerIndex].weights[neuron][input] = weight -
					scale*gradients[layerIndex].weights[neuron][input] -
					learningRate*options.L2*weight
			}
		}

		if !allFinite(updated[layerIndex]) {
			return 0, fmt.Errorf("training diverged: an update of layer %d is not finite", layerIndex)
		}
	}

	n.layers = updated

	return totalLoss / (float64(len(samples)) * float64(outputSize)), nil
}

// allFinite reports whether every weight and bias of a layer is finite.
func allFinite(layer denseLayer) bool {
	for neuron, weights := range layer.weights {
		if math.IsNaN(layer.biases[neuron]) || math.IsInf(layer.biases[neuron], 0) {
			return false
		}
		for _, weight := range weights {
			if math.IsNaN(weight) || math.IsInf(weight, 0) {
				return false
			}
		}
	}
	return true
}
