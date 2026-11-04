package training

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// v3 returns the baseline configuration as a version 3 document with the
// values that reproduce version 2.
func v3() trainingconfig.Config {
	cfg := config()
	cfg.Version = trainingconfig.VersionV3
	return cfg
}

func firstLayerColumn(network *nn.Network, input int) []float64 {
	layer := network.Layers()[0]
	column := make([]float64, len(layer.Weights))
	for neuron := range layer.Weights {
		column[neuron] = layer.Weights[neuron][input]
	}
	return column
}

func weightNorm(network *nn.Network) float64 {
	total := 0.0
	for _, layer := range network.Layers() {
		for _, weights := range layer.Weights {
			for _, weight := range weights {
				total += weight * weight
			}
		}
	}
	return math.Sqrt(total)
}

// A version 3 configuration with the version 2 defaults reproduces a
// version 2 run bit for bit: the same network, scores, selection, and
// metrics; only the configuration's version and identity differ.
func TestRunV3DefaultsReproduceV2(t *testing.T) {
	data := read(t, pixRiskFixture)

	v2Result := run(t, data, config())
	v3Result := run(t, data, v3())

	if !reflect.DeepEqual(v2Result.Network.Layers(), v3Result.Network.Layers()) ||
		!reflect.DeepEqual(scores(t, v2Result), scores(t, v3Result)) {
		t.Error("the version 3 defaults do not reproduce the version 2 model")
	}

	v2Report, v3Report := v2Result.Report, v3Result.Report
	v2Report.Configuration, v3Report.Configuration = trainingreport.Configuration{}, trainingreport.Configuration{}
	if !reflect.DeepEqual(v2Report, v3Report) || !reflect.DeepEqual(v2Result.UnexercisedFeatures, v3Result.UnexercisedFeatures) {
		t.Error("the version 3 defaults do not reproduce the version 2 report")
	}

	v2SHA, _ := config().SHA256()
	v3SHA, _ := v3().SHA256()
	if v2SHA == v3SHA {
		t.Error("the two versions have the same identity")
	}
}

// Unexercised features are the ones that are 0 in every usable train
// record; their first-layer columns are exactly 0 when the configuration
// zeroes them, and the random initialization otherwise.
func TestRunZeroesUnexercisedInputs(t *testing.T) {
	data := read(t, pixRiskFixture)
	prepared := prepareDataset(t, data)

	unexercised := UnexercisedFeatures(prepared.Train)
	if len(unexercised) == 0 {
		t.Fatal("the fixture exercises every feature")
	}
	descriptor := pixrisk.Descriptor()
	var names []string
	for _, index := range unexercised {
		names = append(names, descriptor[index].Name)
		for _, example := range prepared.Train {
			if example.Values[index] != 0 {
				t.Errorf("%s is exercised by line %d", descriptor[index].Name, example.Line)
			}
		}
	}

	plain := run(t, data, v3())
	if !reflect.DeepEqual(plain.UnexercisedFeatures, names) {
		t.Errorf("UnexercisedFeatures = %v, want %v", plain.UnexercisedFeatures, names)
	}

	zeroing := v3()
	zeroing.ZeroUnexercisedInputs = true
	zeroed := run(t, data, zeroing)

	initial, initialUnexercised, err := InitialNetwork(zeroing, prepared.Train)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initialUnexercised, unexercised) {
		t.Errorf("InitialNetwork() unexercised = %v, want %v", initialUnexercised, unexercised)
	}

	for _, index := range unexercised {
		for _, network := range []*nn.Network{initial, zeroed.Network} {
			for neuron, weight := range firstLayerColumn(network, index) {
				if weight != 0 {
					t.Errorf("%s: neuron %d has weight %v, want 0", descriptor[index].Name, neuron, weight)
				}
			}
		}
		for _, weight := range firstLayerColumn(plain.Network, index) {
			if weight == 0 {
				t.Errorf("%s: without zeroing, a weight is exactly 0", descriptor[index].Name)
			}
		}
	}

	// An exercised feature trains either way.
	exercised := 0
	for _, weight := range firstLayerColumn(zeroed.Network, exercised) {
		if weight == 0 {
			t.Error("an exercised feature has a zero weight")
		}
	}
}

// The scaled initialization starts from the bounds nn documents and
// changes the model; InitialNetwork is the network training starts from.
func TestInitialNetworkFollowsTheConfiguration(t *testing.T) {
	data := read(t, pixRiskFixture)
	prepared := prepareDataset(t, data)

	scaled := v3()
	scaled.Initialization = trainingconfig.InitializationScaledUniform

	network, _, err := InitialNetwork(scaled, prepared.Train)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := nn.NewNetworkWith(scaled.Seed, nn.ScaledUniform, scaled.Sizes()...)
	if !reflect.DeepEqual(network.Layers(), want.Layers()) {
		t.Error("InitialNetwork() is not the scaled network of the seed")
	}

	uniform, _, _ := InitialNetwork(v3(), prepared.Train)
	plain, _ := nn.NewNetwork(scaled.Seed, scaled.Sizes()...)
	if !reflect.DeepEqual(uniform.Layers(), plain.Layers()) {
		t.Error("InitialNetwork() with uniform-0.5 is not NewNetwork")
	}

	if reflect.DeepEqual(scores(t, run(t, data, scaled)), scores(t, run(t, data, v3()))) {
		t.Error("the initialization does not change the model")
	}
}

// The cross-entropy trains and selects on its own value: the selected
// objective is the mean cross-entropy over the validation records, and
// weight decay shrinks the weights.
func TestRunWithCrossEntropyAndWeightDecay(t *testing.T) {
	data := read(t, selectionFixture)

	entropy := selectionConfig()
	entropy.Version = trainingconfig.VersionV3
	entropy.Loss = trainingconfig.LossBinaryCrossEntropy
	entropy.Checkpoint = bestValidation(trainingconfig.ObjectiveValidationLoss, 10)
	entropy.MaxEpochs = 300

	result := runSelection(t, data, entropy)
	prepared := preparedSelection(t, data)

	total := 0.0
	for _, p := range predictions(t, result, prepared.Validation) {
		target := 0.0
		if p.Label == trainingdata.Fraud {
			target = 1
		}
		total += nn.LossValue(nn.BinaryCrossEntropy, p.Score, target)
	}
	if want := total / float64(len(prepared.Validation)); *result.Report.Selection.SelectedObjectiveValue != want {
		t.Errorf("selected objective = %v, want the validation cross-entropy %v", *result.Report.Selection.SelectedObjectiveValue, want)
	}

	squared := entropy
	squared.Loss = trainingconfig.LossMeanSquaredError
	if reflect.DeepEqual(scores(t, result), scores(t, runSelection(t, data, squared))) {
		t.Error("the loss does not change the model")
	}

	decayed := entropy
	decayed.L2 = 0.05
	decayedResult := runSelection(t, data, decayed)
	if weightNorm(decayedResult.Network) >= weightNorm(result.Network) {
		t.Errorf("weight decay did not shrink the weights: %v, want below %v", weightNorm(decayedResult.Network), weightNorm(result.Network))
	}
}

// Availability dropout masks whole sources with the configured
// probability from a generator seeded with the configuration's seed, so
// it is deterministic, leaves the records untouched at 0, masks every
// source at 1, and changes the model in between; validation records are
// never masked.
func TestAvailabilityDropoutMasksSources(t *testing.T) {
	data := read(t, pixRiskFixture)
	prepared := prepareDataset(t, data)

	none := v3()
	if newAvailabilityDropout(none, prepared.Train) != nil {
		t.Error("a probability of 0 builds a dropout")
	}

	all := v3()
	all.AvailabilityDropout = 1
	dropout := newAvailabilityDropout(all, prepared.Train)
	dropout.next()
	for i, example := range prepared.Train {
		for _, source := range pixrisk.Sources() {
			for _, j := range pixrisk.SourceFeatures(source) {
				if dropout.masked[i][j] != 0 {
					t.Errorf("line %d: %s feature %d is %v with every source masked", example.Line, source, j, dropout.masked[i][j])
				}
			}
		}
		for j, value := range example.Values {
			optional := false
			for _, source := range pixrisk.Sources() {
				for _, k := range pixrisk.SourceFeatures(source) {
					optional = optional || k == j
				}
			}
			if !optional && dropout.masked[i][j] != value {
				t.Errorf("line %d: always-available feature %d changed", example.Line, j)
			}
		}
		if !reflect.DeepEqual(example.Values, prepared.Train[i].Values) {
			t.Error("the dropout changed the prepared records")
		}
	}

	half := v3()
	half.AvailabilityDropout = 0.5
	first, second := newAvailabilityDropout(half, prepared.Train), newAvailabilityDropout(half, prepared.Train)
	first.next()
	second.next()
	if !reflect.DeepEqual(first.masked, second.masked) {
		t.Error("the masks are not deterministic")
	}
	first.next()
	if reflect.DeepEqual(first.masked, second.masked) {
		t.Error("the masks do not change between epochs")
	}

	halfResult := run(t, data, half)
	if !reflect.DeepEqual(scores(t, halfResult), scores(t, run(t, data, half))) {
		t.Error("a run with dropout is not deterministic")
	}
	if reflect.DeepEqual(scores(t, halfResult), scores(t, run(t, data, none))) {
		t.Error("dropout does not change the model")
	}

	// With every source masked in every epoch, the model is the one
	// trained on records without any optional source.
	allResult := run(t, data, all)
	masked, _, _ := InitialNetwork(all, prepared.Train)
	samples := make([]nn.Sample, len(prepared.Train))
	for i, example := range prepared.Train {
		values := append([]float64(nil), example.Values...)
		for _, source := range pixrisk.Sources() {
			for _, j := range pixrisk.SourceFeatures(source) {
				values[j] = 0
			}
		}
		samples[i] = nn.Sample{Inputs: values, Targets: []float64{target(example.Label)}}
	}
	for range all.MaxEpochs {
		if _, err := masked.Train(context.Background(), samples, all.LearningRate, nn.Options{}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(masked.Layers(), allResult.Network.Layers()) {
		t.Error("a dropout of 1 is not training on records without optional sources")
	}
}
