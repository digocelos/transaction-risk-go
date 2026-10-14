package nn

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// Tolerances of the numerical gradient check. Central differences with
// step h have a truncation error of order h² and a rounding error of order
// ε/h, so with h = 1e-6 a correct gradient agrees to about 1e-9.
const (
	gradientStep              = 1e-6
	gradientAbsoluteTolerance = 1e-8
	gradientRelativeTolerance = 1e-6
)

// smallNetwork is a 3-4-2 network: two layers and two outputs exercise the
// backward pass through a hidden layer and the averaging over outputs.
func smallNetwork(t *testing.T) *Network {
	t.Helper()

	network, err := NewNetwork(7, 3, 4, 2)
	if err != nil {
		t.Fatal(err)
	}

	return network
}

var gradientSamples = []Sample{
	{Inputs: []float64{0.3, -1.2, 2.0}, Targets: []float64{1, 0}},
	{Inputs: []float64{-0.7, 0.4, 0.1}, Targets: []float64{0, 1}},
	{Inputs: []float64{1.5, 0.9, -0.3}, Targets: []float64{0.25, 0.75}},
}

// loss is the mean squared error over samples and outputs, computed with
// Predict.
func loss(t *testing.T, network *Network, samples []Sample) float64 {
	t.Helper()

	total, count := 0.0, 0
	for _, sample := range samples {
		outputs, err := network.Predict(sample.Inputs)
		if err != nil {
			t.Fatal(err)
		}
		for i, output := range outputs {
			difference := output - sample.Targets[i]
			total += difference * difference
			count++
		}
	}

	return total / float64(count)
}

// parameters returns pointers to every weight and bias, in layer order.
func parameters(network *Network) []*float64 {
	var params []*float64
	for l := range network.layers {
		for neuron := range network.layers[l].weights {
			for input := range network.layers[l].weights[neuron] {
				params = append(params, &network.layers[l].weights[neuron][input])
			}
			params = append(params, &network.layers[l].biases[neuron])
		}
	}
	return params
}

func clone(network *Network) *Network {
	copied := &Network{inputSize: network.inputSize}
	for _, layer := range network.layers {
		c := denseLayer{biases: append([]float64(nil), layer.biases...)}
		for _, weights := range layer.weights {
			c.weights = append(c.weights, append([]float64(nil), weights...))
		}
		copied.layers = append(copied.layers, c)
	}
	return copied
}

// The update of one TrainBatch step is −learningRate times the gradient of
// the mean squared error, which central differences estimate independently.
func TestTrainBatchFollowsTheNumericalGradient(t *testing.T) {
	const learningRate = 0.5

	network := smallNetwork(t)
	before := clone(network)

	reported, err := network.TrainBatch(gradientSamples, learningRate)
	if err != nil {
		t.Fatalf("TrainBatch() error = %v", err)
	}

	if want := loss(t, before, gradientSamples); math.Abs(reported-want) > 1e-12 {
		t.Errorf("reported loss = %v, want the loss before the update, %v", reported, want)
	}

	probe := clone(before)
	params, beforeParams, afterParams := parameters(probe), parameters(before), parameters(network)

	for i, param := range params {
		original := *param

		*param = original + gradientStep
		plus := loss(t, probe, gradientSamples)
		*param = original - gradientStep
		minus := loss(t, probe, gradientSamples)
		*param = original

		numerical := (plus - minus) / (2 * gradientStep)
		backpropagated := (*beforeParams[i] - *afterParams[i]) / learningRate

		if math.Abs(numerical-backpropagated) > gradientAbsoluteTolerance+gradientRelativeTolerance*math.Abs(numerical) {
			t.Errorf("parameter %d: gradient = %v, finite differences = %v", i, backpropagated, numerical)
		}
	}
}

func TestTrainBatchRejectsInvalidSamplesWithoutChangingParameters(t *testing.T) {
	tests := map[string][]Sample{
		"NaN input":         {gradientSamples[0], {Inputs: []float64{math.NaN(), 0, 0}, Targets: []float64{0, 1}}},
		"infinite input":    {gradientSamples[0], {Inputs: []float64{0, math.Inf(-1), 0}, Targets: []float64{0, 1}}},
		"target above 1":    {gradientSamples[0], {Inputs: []float64{0, 0, 0}, Targets: []float64{0, 1.5}}},
		"wrong input count": {gradientSamples[0], {Inputs: []float64{0, 0}, Targets: []float64{0, 1}}},
		"empty batch":       {},
	}

	for name, samples := range tests {
		network := smallNetwork(t)
		before := clone(network)

		if _, err := network.TrainBatch(samples, 0.5); err == nil {
			t.Errorf("%s: TrainBatch() error = nil, want an error", name)
		}

		if !reflect.DeepEqual(network, before) {
			t.Errorf("%s: parameters changed", name)
		}
	}
}

// cancelling builds a 3-2-1 network whose hidden neurons both output
// exactly 0.5 and whose output weights are m and −m, so the output is
// exactly 0.5 while the backward pass scales by m.
func cancelling(m float64) *Network {
	return &Network{
		inputSize: 3,
		layers: []denseLayer{
			{weights: [][]float64{{0, 0, 0}, {0, 0, 0}}, biases: []float64{0, 0}},
			{weights: [][]float64{{m, -m}}, biases: []float64{0}},
		},
	}
}

func batch(input float64) []Sample {
	return []Sample{
		{Inputs: []float64{input, input, input}, Targets: []float64{1}},
		{Inputs: []float64{input, input, input}, Targets: []float64{1}},
		{Inputs: []float64{input, input, input}, Targets: []float64{1}},
	}
}

// Divergence is detected before any parameter changes, and the error names
// what diverged.
func TestTrainBatchRejectsNonFiniteTrainingWithoutChangingParameters(t *testing.T) {
	nanParameter := smallNetwork(t)
	nanParameter.layers[1].weights[0][2] = math.NaN()

	tests := map[string]struct {
		network *Network
		samples []Sample
		rate    float64
		want    string
	}{
		"non-finite parameter": {nanParameter, gradientSamples, 0.5, "parameter"},
		// The output is finite, but the hidden deltas reach about 5.6e306
		// and the input of 100 overflows the first layer's gradients.
		"non-finite gradient": {cancelling(math.MaxFloat64 / 2), batch(100), 0.5, "gradient"},
		// Finite gradients of about 18.75 overflow once scaled.
		"non-finite update":     {cancelling(100), batch(1), math.MaxFloat64, "update"},
		"invalid learning rate": {smallNetwork(t), gradientSamples, math.Inf(1), "learning rate"},
	}

	for name, test := range tests {
		before := clone(test.network)

		_, err := test.network.TrainBatch(test.samples, test.rate)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: TrainBatch() error = %v, want one about the %s", name, err, test.want)
		}

		if !sameParameters(test.network, before) {
			t.Errorf("%s: parameters changed", name)
		}
	}
}

// sameParameters compares parameters bit for bit, so NaN equals NaN.
func sameParameters(a, b *Network) bool {
	pa, pb := parameters(a), parameters(b)
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if math.Float64bits(*pa[i]) != math.Float64bits(*pb[i]) {
			return false
		}
	}
	return true
}

func TestPredictRejectsNonFiniteInputs(t *testing.T) {
	network := smallNetwork(t)

	for _, inputs := range [][]float64{
		{math.NaN(), 0, 0},
		{0, math.Inf(1), 0},
		{0, 0, math.Inf(-1)},
	} {
		if _, err := network.Predict(inputs); err == nil {
			t.Errorf("Predict(%v) error = nil, want an error", inputs)
		}
	}
}

// A clone keeps the parameters of the moment it was taken.
func TestCloneIsIndependent(t *testing.T) {
	network := smallNetwork(t)
	snapshot := network.Clone()

	before, err := snapshot.Predict(gradientSamples[0].Inputs)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := network.TrainBatch(gradientSamples, 0.5); err != nil {
		t.Fatal(err)
	}

	after, err := snapshot.Predict(gradientSamples[0].Inputs)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(before, after) {
		t.Error("training the original changed the clone")
	}

	if !sameParameters(snapshot, clone(smallNetwork(t))) {
		t.Error("the clone does not hold the original parameters")
	}
}
