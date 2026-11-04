package nn

import (
	"context"
	"errors"
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

// countingContext counts its checks and is canceled from check cancelAt
// on, or never when cancelAt is 0.
type countingContext struct {
	context.Context
	checks, cancelAt int
}

func (c *countingContext) Err() error {
	c.checks++
	if c.cancelAt > 0 && c.checks >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func cancellationSamples(count int) []Sample {
	samples := make([]Sample, count)
	for i := range samples {
		samples[i] = Sample{Inputs: []float64{float64(i%7) / 7, 1}, Targets: []float64{float64(i % 2)}}
	}
	return samples
}

// TrainBatchContext checks its context before the first sample and every
// 1,024 samples, so a canceled step stops within 1,024 samples.
func TestTrainBatchContextChecksEvery1024Samples(t *testing.T) {
	network, err := NewNetwork(1, 2, 3, 1)
	if err != nil {
		t.Fatal(err)
	}

	ctx := &countingContext{Context: context.Background()}
	if _, err := network.TrainBatchContext(ctx, cancellationSamples(3000), 0.5); err != nil {
		t.Fatal(err)
	}
	if ctx.checks != 3 {
		t.Errorf("checked the context %d times for 3,000 samples, want 3", ctx.checks)
	}
}

// A canceled step returns the context's error and changes no parameter.
func TestTrainBatchContextStopsWithoutChanges(t *testing.T) {
	for name, cancelAt := range map[string]int{"before the first sample": 1, "within the batch": 2} {
		t.Run(name, func(t *testing.T) {
			network, err := NewNetwork(1, 2, 3, 1)
			if err != nil {
				t.Fatal(err)
			}
			before := network.Layers()

			ctx := &countingContext{Context: context.Background(), cancelAt: cancelAt}
			if _, err := network.TrainBatchContext(ctx, cancellationSamples(3000), 0.5); !errors.Is(err, context.Canceled) {
				t.Fatalf("TrainBatchContext() error = %v, want context.Canceled", err)
			}
			if !reflect.DeepEqual(network.Layers(), before) {
				t.Error("a canceled step changed the parameters")
			}
		})
	}
}

// TrainBatch is TrainBatchContext without cancellation.
func TestTrainBatchIsTrainBatchContext(t *testing.T) {
	first, err := NewNetwork(4, 2, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	second := first.Clone()
	samples := cancellationSamples(50)

	lossA, errA := first.TrainBatch(samples, 0.5)
	lossB, errB := second.TrainBatchContext(context.Background(), samples, 0.5)
	if errA != nil || errB != nil || lossA != lossB || !reflect.DeepEqual(first.Layers(), second.Layers()) {
		t.Errorf("TrainBatch = %v, %v; TrainBatchContext = %v, %v", lossA, errA, lossB, errB)
	}
}

// crossEntropy is the mean binary cross-entropy over samples and outputs,
// computed with Predict.
func crossEntropy(t *testing.T, network *Network, samples []Sample) float64 {
	t.Helper()

	total, count := 0.0, 0
	for _, sample := range samples {
		outputs, err := network.Predict(sample.Inputs)
		if err != nil {
			t.Fatal(err)
		}
		for i, output := range outputs {
			total += LossValue(BinaryCrossEntropy, output, sample.Targets[i])
			count++
		}
	}

	return total / float64(count)
}

// decay is the weight decay term (l2 / 2)·Σ weight², over weights only.
func decay(network *Network, l2 float64) float64 {
	total := 0.0
	for _, layer := range network.layers {
		for _, weights := range layer.weights {
			for _, weight := range weights {
				total += weight * weight
			}
		}
	}
	return l2 / 2 * total
}

// With the binary cross-entropy and weight decay, the update of one Train
// step is −learningRate times the gradient of the cross-entropy plus the
// decay term, which central differences estimate independently; biases
// receive no decay.
func TestTrainFollowsTheNumericalGradientOfEveryLoss(t *testing.T) {
	const learningRate, l2 = 0.5, 0.01

	cases := map[string]struct {
		options   Options
		objective func(*Network) float64
	}{
		"cross-entropy": {Options{Loss: BinaryCrossEntropy}, func(n *Network) float64 { return crossEntropy(t, n, gradientSamples) }},
		"cross-entropy with decay": {Options{Loss: BinaryCrossEntropy, L2: l2}, func(n *Network) float64 {
			return crossEntropy(t, n, gradientSamples) + decay(n, l2)
		}},
		"squared error with decay": {Options{Loss: MeanSquaredError, L2: l2}, func(n *Network) float64 {
			return loss(t, n, gradientSamples) + decay(n, l2)
		}},
		"default options": {Options{}, func(n *Network) float64 { return loss(t, n, gradientSamples) }},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			network := smallNetwork(t)
			before := clone(network)

			reported, err := network.Train(context.Background(), gradientSamples, learningRate, c.options)
			if err != nil {
				t.Fatalf("Train() error = %v", err)
			}

			// The reported loss excludes the decay term.
			want := c.objective(before) - decay(before, c.options.L2)
			if math.Abs(reported-want) > 1e-12 {
				t.Errorf("reported loss = %v, want %v", reported, want)
			}

			probe := clone(before)
			params, beforeParams, afterParams := parameters(probe), parameters(before), parameters(network)

			for i, param := range params {
				original := *param

				*param = original + gradientStep
				plus := c.objective(probe)
				*param = original - gradientStep
				minus := c.objective(probe)
				*param = original

				numerical := (plus - minus) / (2 * gradientStep)
				backpropagated := (*beforeParams[i] - *afterParams[i]) / learningRate

				if math.Abs(numerical-backpropagated) > gradientAbsoluteTolerance+gradientRelativeTolerance*math.Abs(numerical) {
					t.Errorf("parameter %d: gradient = %v, finite differences = %v", i, backpropagated, numerical)
				}
			}
		})
	}
}

// The cross-entropy value stays finite at saturated outputs, and its
// gradient does not vanish there.
func TestBinaryCrossEntropyIsFiniteWhenSaturated(t *testing.T) {
	if v := LossValue(BinaryCrossEntropy, 0, 1); math.IsInf(v, 0) || v < 30 {
		t.Errorf("LossValue(0, 1) = %v, want a large finite value", v)
	}
	if v := LossValue(BinaryCrossEntropy, 1, 0); math.IsInf(v, 0) || v < 30 {
		t.Errorf("LossValue(1, 0) = %v, want a large finite value", v)
	}
	if v := LossValue(BinaryCrossEntropy, 0.5, 1); math.Abs(v-math.Ln2) > 1e-15 {
		t.Errorf("LossValue(0.5, 1) = %v, want ln 2", v)
	}
	if v := LossValue(MeanSquaredError, 0.25, 1); v != 0.5625 {
		t.Errorf("LossValue squared = %v", v)
	}

	// A network whose output is saturated still moves under the
	// cross-entropy, while the squared error barely does.
	saturated, err := FromLayers(1, []Layer{{Weights: [][]float64{{40}}, Biases: []float64{0}}})
	if err != nil {
		t.Fatal(err)
	}
	sample := []Sample{{Inputs: []float64{1}, Targets: []float64{0}}}

	squared := clone(saturated)
	if _, err := squared.Train(context.Background(), sample, 1, Options{}); err != nil {
		t.Fatal(err)
	}
	entropy := clone(saturated)
	if _, err := entropy.Train(context.Background(), sample, 1, Options{Loss: BinaryCrossEntropy}); err != nil {
		t.Fatal(err)
	}
	if moved := 40 - entropy.layers[0].weights[0][0]; math.Abs(moved-1) > 1e-9 {
		t.Errorf("cross-entropy moved the weight by %v, want 1 (output − target)", moved)
	}
	if moved := 40 - squared.layers[0].weights[0][0]; moved > 1e-12 {
		t.Errorf("squared error moved the saturated weight by %v, want about 0", moved)
	}
}

// Invalid options are rejected before any parameter changes.
func TestTrainRejectsInvalidOptions(t *testing.T) {
	for name, options := range map[string]Options{
		"unknown loss":   {Loss: "hinge"},
		"negative decay": {L2: -0.1},
		"infinite decay": {L2: math.Inf(1)},
		"NaN decay":      {L2: math.NaN()},
	} {
		network := smallNetwork(t)
		before := clone(network)
		if _, err := network.Train(context.Background(), gradientSamples, 0.5, options); err == nil {
			t.Errorf("%s: Train() error = nil", name)
		}
		if !reflect.DeepEqual(network.layers, before.layers) {
			t.Errorf("%s: a rejected step changed the parameters", name)
		}
	}
}

// The scaled initialization bounds each layer's weights by √(3/fan_in), is
// reproducible, and differs from the uniform-0.5 one; NewNetwork keeps
// the uniform-0.5 initialization.
func TestNewNetworkWithInitializations(t *testing.T) {
	scaled, err := NewNetworkWith(42, ScaledUniform, 62, 16, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := NewNetworkWith(42, ScaledUniform, 62, 16, 8, 1)
	if !reflect.DeepEqual(scaled.layers, again.layers) {
		t.Error("the scaled initialization is not reproducible")
	}

	for i, layer := range scaled.layers {
		bound := math.Sqrt(3 / float64(len(layer.weights[0])))
		largest := 0.0
		for neuron, weights := range layer.weights {
			if layer.biases[neuron] != 0 {
				t.Errorf("layer %d: bias %v, want 0", i, layer.biases[neuron])
			}
			for _, weight := range weights {
				largest = math.Max(largest, math.Abs(weight))
			}
		}
		if largest >= bound || largest < bound/2 {
			t.Errorf("layer %d: largest |weight| %v, want below %v and not far below it", i, largest, bound)
		}
	}

	uniform, _ := NewNetwork(42, 62, 16, 8, 1)
	half, _ := NewNetworkWith(42, UniformHalf, 62, 16, 8, 1)
	if !reflect.DeepEqual(uniform.layers, half.layers) {
		t.Error("NewNetwork is not the uniform-0.5 initialization")
	}
	if reflect.DeepEqual(uniform.layers, scaled.layers) {
		t.Error("the initializations do not differ")
	}

	if _, err := NewNetworkWith(42, "xavier", 62, 1); err == nil {
		t.Error("an unsupported initialization was accepted")
	}
}

// Zeroed inputs stay at 0 through training, with and without weight
// decay, because an input that is 0 in every sample never produces a
// gradient.
func TestZeroInputsStayZero(t *testing.T) {
	network := smallNetwork(t)
	if err := network.ZeroInputs([]int{1}); err != nil {
		t.Fatal(err)
	}
	for _, weights := range network.layers[0].weights {
		if weights[1] != 0 {
			t.Fatalf("input 1 is not zeroed: %v", weights)
		}
	}

	samples := []Sample{
		{Inputs: []float64{0.3, 0, 2.0}, Targets: []float64{1, 0}},
		{Inputs: []float64{-0.7, 0, 0.1}, Targets: []float64{0, 1}},
	}
	for range 50 {
		if _, err := network.Train(context.Background(), samples, 0.5, Options{Loss: BinaryCrossEntropy, L2: 0.01}); err != nil {
			t.Fatal(err)
		}
	}
	for neuron, weights := range network.layers[0].weights {
		if weights[1] != 0 {
			t.Errorf("neuron %d: zeroed input 1 moved to %v", neuron, weights[1])
		}
		if weights[0] == 0 || weights[2] == 0 {
			t.Errorf("neuron %d: exercised inputs did not train: %v", neuron, weights)
		}
	}

	for name, inputs := range map[string][]int{"out of range": {3}, "negative": {-1}, "repeated": {0, 0}} {
		if err := smallNetwork(t).ZeroInputs(inputs); err == nil {
			t.Errorf("%s: ZeroInputs() error = nil", name)
		}
	}
}
