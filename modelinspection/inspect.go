package modelinspection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/modelevaluation"
	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// Options configure an inspection. MaxRecords limits the records read from
// the dataset, as trainingdata.Scan does; trainingdata.DefaultMaxRecords
// is the supported envelope.
type Options struct {
	MaxRecords int
}

// Inspect decodes a model artifact from its exact bytes, checks that the
// configuration is the one the artifact names, reads the training dataset
// exactly as training did, rebuilds the network training started from, and
// measures the parameters, the inputs no train record exercised, the
// partial dependence on the validation partition, and the missingness
// probes. The dataset must be the model's
// training dataset, by SHA-256; a dataset with invalid records yields a
// *training.InvalidDatasetError. The artifact and the configuration are
// validated before the dataset is read, and ctx is checked while reading.
func Inspect(ctx context.Context, model []byte, config trainingconfig.Config, dataset io.Reader, options Options) (Report, error) {
	artifact, err := modelartifact.Decode(model)
	if err != nil {
		return Report{}, err
	}

	configSHA256, err := config.SHA256()
	switch {
	case err != nil:
		return Report{}, err
	case configSHA256 != artifact.Training.ConfigSHA256:
		return Report{}, fmt.Errorf("the configuration (SHA-256 %s) is not the model's configuration (SHA-256 %s)",
			configSHA256, artifact.Training.ConfigSHA256)
	case !reflect.DeepEqual(config.Sizes(), artifact.Sizes):
		return Report{}, errors.New("the configuration's architecture is not the model's")
	}

	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	boundaries := evaluation.Boundaries{TrainEnd: artifact.Training.TrainEnd, ValidationEnd: artifact.Training.ValidationEnd}
	prepared, err := training.PrepareDataset(ctx, dataset, boundaries, options.MaxRecords)
	if err != nil {
		return Report{}, err
	}

	switch {
	case prepared.Dataset.DatasetSHA256 != artifact.Training.DatasetSHA256:
		return Report{}, fmt.Errorf("the dataset (SHA-256 %s) is not the model's training dataset (SHA-256 %s)",
			prepared.Dataset.DatasetSHA256, artifact.Training.DatasetSHA256)
	case !reflect.DeepEqual(prepared.State, artifact.State):
		return Report{}, errors.New("the preparation state fitted on the dataset is not the model's")
	}

	layers := artifact.Network.Layers()
	initial, _, err := training.InitialNetwork(config, artifact.State)
	if err != nil {
		return Report{}, err
	}

	parameters := inspectParameters(artifact, config.Seed, layers, initial.Layers(), prepared.Train)

	var unexercised []string
	for _, input := range parameters.Inputs {
		if !input.ExercisedInTrain {
			unexercised = append(unexercised, input.Name)
		}
	}
	if unexercised == nil {
		unexercised = []string{}
	}

	validation := prepared.Validation
	counts := trainingdata.LabelCounts{}
	for _, example := range validation {
		if example.Label == trainingdata.Fraud {
			counts.Fraud++
		} else {
			counts.Legitimate++
		}
	}

	return Report{
		Software:            modelartifact.SoftwareFromBuild(),
		Model:               modelevaluation.Identify(artifact, sha256Hex(model)),
		Dataset:             prepared.Dataset,
		Partitions:          prepared.Partitions,
		Parameters:          parameters,
		UnexercisedFeatures: unexercised,
		PartialDependence:   partialDependence(artifact, validation, counts),
		Missingness:         missingness(artifact, validation, counts),
	}, nil
}

// forward computes the outputs of every layer for one input vector, with
// the arithmetic of nn.Network.Predict, so activations and scores match
// scoring bit for bit on one platform.
func forward(layers []nn.Layer, inputs []float64) [][]float64 {
	outputs := make([][]float64, len(layers))
	current := inputs

	for i, layer := range layers {
		values := make([]float64, len(layer.Biases))
		for neuron, weights := range layer.Weights {
			sum := layer.Biases[neuron]
			for input, weight := range weights {
				sum += float64(current[input] * weight)
			}
			values[neuron] = 1 / (1 + math.Exp(-sum))
		}
		outputs[i] = values
		current = values
	}

	return outputs
}

func score(layers []nn.Layer, inputs []float64) float64 {
	outputs := forward(layers, inputs)
	return outputs[len(outputs)-1][0]
}

func inspectParameters(artifact modelartifact.Artifact, seed int64, layers, initial []nn.Layer, train []training.PreparedExample) Parameters {
	descriptor := pixrisk.Descriptor()
	first := layers[0]

	inputs := make([]Input, pixrisk.FeatureCount)
	for j, feature := range descriptor {
		var column, change, maxChange float64
		for neuron := range first.Weights {
			weight := first.Weights[neuron][j]
			delta := weight - initial[0].Weights[neuron][j]
			column += weight * weight
			change += delta * delta
			maxChange = math.Max(maxChange, math.Abs(delta))
		}

		nonzero := 0
		for _, example := range train {
			if example.Values[j] != 0 {
				nonzero++
			}
		}

		inputs[j] = Input{
			Name:                feature.Name,
			Kind:                feature.Kind,
			ColumnNorm:          math.Sqrt(column),
			ChangeNorm:          math.Sqrt(change),
			MaxAbsChange:        maxChange,
			TrainNonzeroRecords: nonzero,
			ExercisedInTrain:    artifact.State.Features[j].ExercisedInTrain,
		}
	}

	// Activations of every hidden neuron on the train records.
	hidden := layers[:len(layers)-1]
	sums := make([][]float64, len(hidden))
	below := make([][]int, len(hidden))
	above := make([][]int, len(hidden))
	for i, layer := range hidden {
		sums[i] = make([]float64, len(layer.Biases))
		below[i] = make([]int, len(layer.Biases))
		above[i] = make([]int, len(layer.Biases))
	}
	for _, example := range train {
		outputs := forward(layers, example.Values)
		for i := range hidden {
			for neuron, value := range outputs[i] {
				sums[i][neuron] += value
				if value < 0.01 {
					below[i][neuron]++
				}
				if value > 0.99 {
					above[i][neuron]++
				}
			}
		}
	}

	records := float64(len(train))
	hiddenLayers := make([]HiddenLayer, len(hidden))
	for i, layer := range hidden {
		next := layers[i+1]
		neurons := make([]Neuron, len(layer.Biases))
		for neuron, weights := range layer.Weights {
			var out float64
			for _, row := range next.Weights {
				out += row[neuron] * row[neuron]
			}
			activation := Activation{}
			if records > 0 {
				activation = Activation{
					Mean:      sums[i][neuron] / records,
					Below0_01: float64(below[i][neuron]) / records,
					Above0_99: float64(above[i][neuron]) / records,
				}
			}
			neurons[neuron] = Neuron{Bias: layer.Biases[neuron], InputNorm: norm(weights), OutputNorm: math.Sqrt(out), Activation: activation}
		}
		hiddenLayers[i] = HiddenLayer{Neurons: neurons, MaxAbsCosine: maxAbsCosine(layer.Weights)}
	}

	last := layers[len(layers)-1]
	return Parameters{
		Sizes:        artifact.Sizes,
		Seed:         seed,
		Inputs:       inputs,
		HiddenLayers: hiddenLayers,
		Output:       OutputLayer{Weights: last.Weights[0], Bias: last.Biases[0]},
	}
}

func norm(values []float64) float64 {
	var sum float64
	for _, value := range values {
		sum += value * value
	}
	return math.Sqrt(sum)
}

// maxAbsCosine returns the pair of rows with the largest absolute cosine,
// the first such pair in row order, or nil with fewer than two rows.
func maxAbsCosine(rows [][]float64) *Cosine {
	if len(rows) < 2 {
		return nil
	}

	best := Cosine{Neurons: [2]int{0, 1}}
	for i := range rows {
		for j := i + 1; j < len(rows); j++ {
			var dot float64
			for k := range rows[i] {
				dot += rows[i][k] * rows[j][k]
			}
			cosine := 0.0
			if ni, nj := norm(rows[i]), norm(rows[j]); ni > 0 && nj > 0 {
				cosine = dot / (ni * nj)
			}
			if math.Abs(cosine) > math.Abs(best.Value) {
				best = Cosine{Value: cosine, Neurons: [2]int{i, j}}
			}
		}
	}
	return &best
}

// partialDependence measures the score of every validation record as each
// standardized feature takes the values of its grid, in real units, with
// its indicator forced to known.
func partialDependence(artifact modelartifact.Artifact, validation []training.PreparedExample, counts trainingdata.LabelCounts) PartialDependence {
	descriptor := pixrisk.Descriptor()
	layers := artifact.Network.Layers()
	threshold := artifact.Threshold.Value

	var features []FeatureDependence
	for j, feature := range descriptor {
		if feature.Kind != pixrisk.Standardized {
			continue
		}
		s := scaleOf(feature.Name)
		state := artifact.State.Features[j]

		// The train range, in raw units, as the preparation state records
		// it; scoring clips to it before standardizing.
		trainRange := Range{Min: s.raw(*state.Min), Max: s.raw(*state.Max)}

		grid := make([]GridPoint, len(s.grid))
		for g, raw := range s.grid {
			// The grid value as scoring would see it: clipped to the train
			// range, then standardized.
			standardized := (math.Min(math.Max(s.transform(raw), *state.Min), *state.Max) - state.Mean) / state.Scale
			var fraudSum, legitimateSum float64
			var fraudFlagged, legitimateFlagged int
			for _, example := range validation {
				values := append([]float64(nil), example.Values...)
				values[j] = standardized
				if feature.Indicator != pixrisk.Always {
					values[feature.Indicator] = 1
				}
				value := score(layers, values)
				if example.Label == trainingdata.Fraud {
					fraudSum += value
					if value >= threshold {
						fraudFlagged++
					}
				} else {
					legitimateSum += value
					if value >= threshold {
						legitimateFlagged++
					}
				}
			}

			point := GridPoint{Value: raw, InTrainRange: raw >= trainRange.Min && raw <= trainRange.Max}
			if counts.Fraud > 0 {
				point.Fraud = &LabelScores{MeanScore: fraudSum / float64(counts.Fraud), FlaggedShare: float64(fraudFlagged) / float64(counts.Fraud)}
			}
			if counts.Legitimate > 0 {
				point.Legitimate = &LabelScores{MeanScore: legitimateSum / float64(counts.Legitimate), FlaggedShare: float64(legitimateFlagged) / float64(counts.Legitimate)}
			}
			grid[g] = point
		}

		direction := directions[feature.Name]
		features = append(features, FeatureDependence{
			Name:              feature.Name,
			Unit:              s.unit,
			ExpectedDirection: direction,
			TrainRange:        trainRange,
			Grid:              grid,
			Violations:        violations(direction, grid),
		})
	}

	return PartialDependence{Partition: evaluation.Validation, Records: counts, Tolerance: Tolerance, Features: features}
}

// violations lists the moves of the mean score against the expected
// direction between consecutive grid points, by label.
func violations(direction string, grid []GridPoint) []Violation {
	list := []Violation{}
	if direction == NoDirection {
		return list
	}

	for _, label := range []trainingdata.Label{trainingdata.Fraud, trainingdata.Legitimate} {
		for g := 1; g < len(grid); g++ {
			from, to := grid[g-1], grid[g]
			scoresFrom, scoresTo := from.Fraud, to.Fraud
			if label == trainingdata.Legitimate {
				scoresFrom, scoresTo = from.Legitimate, to.Legitimate
			}
			if scoresFrom == nil || scoresTo == nil {
				continue
			}

			delta := scoresTo.MeanScore - scoresFrom.MeanScore
			against := (direction == RiskIncreasing && delta < -Tolerance) ||
				(direction == RiskDecreasing && delta > Tolerance)
			if against {
				list = append(list, Violation{
					Label:         label,
					From:          from.Value,
					To:            to.Value,
					InTrainRange:  from.InTrainRange && to.InTrainRange,
					MeanScoreFrom: scoresFrom.MeanScore,
					MeanScoreTo:   scoresTo.MeanScore,
				})
			}
		}
	}

	return list
}

// missingness measures the validation metrics at baseline and with each
// probe applied to every validation record.
func missingness(artifact modelartifact.Artifact, validation []training.PreparedExample, counts trainingdata.LabelCounts) Missingness {
	layers := artifact.Network.Layers()
	threshold := artifact.Threshold.Value
	descriptor := pixrisk.Descriptor()

	baselineScores := make([]float64, len(validation))
	for i, example := range validation {
		baselineScores[i] = score(layers, example.Values)
	}
	baseline := summarize(validation, baselineScores, threshold)

	var results []Probe
	for _, p := range probes() {
		scores := make([]float64, len(validation))
		evading, flagged := 0, 0
		var fraudChange, legitimateChange float64
		for i, example := range validation {
			scores[i] = score(layers, p.apply(example.Values))
			change := scores[i] - baselineScores[i]
			if example.Label == trainingdata.Fraud {
				fraudChange += change
				if baselineScores[i] >= threshold && scores[i] < threshold {
					evading++
				}
			} else {
				legitimateChange += change
				if baselineScores[i] < threshold && scores[i] >= threshold {
					flagged++
				}
			}
		}

		set := []string{}
		for _, i := range p.set {
			set = append(set, descriptor[i].Name)
		}
		masked := []string{}
		for _, i := range p.masked {
			masked = append(masked, descriptor[i].Name)
		}

		results = append(results, Probe{
			Name:                   p.name,
			Kind:                   p.kind,
			Set:                    set,
			Masked:                 masked,
			Result:                 summarize(validation, scores, threshold),
			FraudsEvading:          evading,
			LegitimateNewlyFlagged: flagged,
			MeanScoreChange:        means(counts, fraudChange, legitimateChange),
		})
	}

	return Missingness{Partition: evaluation.Validation, Records: counts, Threshold: threshold, Baseline: baseline, Probes: results}
}

// summarize measures scores at the threshold. Scores are from 0 to 1 by
// construction, so evaluation.Evaluate cannot fail on them.
func summarize(validation []training.PreparedExample, scores []float64, threshold float64) Summary {
	predictions := make([]evaluation.Prediction, len(validation))
	counts := trainingdata.LabelCounts{}
	var fraudSum, legitimateSum float64
	for i, example := range validation {
		predictions[i] = evaluation.Prediction{
			Score:           scores[i],
			Label:           example.Label,
			AmountCents:     example.AmountCents,
			LossAmountCents: example.LossAmountCents,
		}
		if example.Label == trainingdata.Fraud {
			counts.Fraud++
			fraudSum += scores[i]
		} else {
			counts.Legitimate++
			legitimateSum += scores[i]
		}
	}

	metrics, err := evaluation.Evaluate(predictions, threshold)
	if err != nil {
		panic(fmt.Sprintf("inspection: %v", err))
	}

	return Summary{
		Recall:            metrics.Recall,
		FalsePositives:    metrics.FalsePositives,
		FalsePositiveRate: metrics.FalsePositiveRate,
		PRAUC:             metrics.PRAUC,
		MeanScore:         means(counts, fraudSum, legitimateSum),
	}
}

// means divides label sums by their counts, nil for a label with no
// record.
func means(counts trainingdata.LabelCounts, fraudSum, legitimateSum float64) LabelMeans {
	var result LabelMeans
	if counts.Fraud > 0 {
		mean := fraudSum / float64(counts.Fraud)
		result.Fraud = &mean
	}
	if counts.Legitimate > 0 {
		mean := legitimateSum / float64(counts.Legitimate)
		result.Legitimate = &mean
	}
	return result
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
