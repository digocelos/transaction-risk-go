// Package modelinspection inspects a trained pix-risk-model-v1 artifact
// together with its training run and training dataset, and writes the
// inspection report pix-model-inspection-v1, as specified in
// docs/contracts/pix-model-inspection-v1.md: the parameters and how far
// they moved from their initialization, the inputs that no train record
// exercised, the partial dependence of the score on each standardized
// feature in real units, and the sensitivity of the validation metrics to
// each optional source being unavailable. Inspection only reads: it never
// trains, refits, selects, or changes the artifact, the run, or the
// dataset, and nothing it reports is a verdict on the model.
package modelinspection

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/modelevaluation"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// Version is the version of the inspection report layout.
const Version = "pix-model-inspection-v1"

// Tolerance is the smallest change of a mean score, between two
// consecutive grid points, that counts as a move against the expected
// direction of a feature.
const Tolerance = 0.001

// Report is the inspection report of one model, with its run and its
// training dataset. Software is the build that inspected, and
// Model.Software the build that trained.
type Report struct {
	InspectionReportVersion string                     `json:"inspection_report_version"`
	Software                modelartifact.Software     `json:"software"`
	Model                   modelevaluation.Model      `json:"model"`
	Dataset                 trainingdata.Report        `json:"dataset"`
	Partitions              evaluation.PartitionReport `json:"partitions"`
	Parameters              Parameters                 `json:"parameters"`
	UnexercisedFeatures     []string                   `json:"unexercised_features"`
	PartialDependence       PartialDependence          `json:"partial_dependence"`
	Missingness             Missingness                `json:"missingness"`
}

// Parameters describes the network parameters: the architecture, the seed
// that initialized them, one entry per input feature, the hidden layers,
// and the output layer.
type Parameters struct {
	Sizes        []int         `json:"sizes"`
	Seed         int64         `json:"seed"`
	Inputs       []Input       `json:"inputs"`
	HiddenLayers []HiddenLayer `json:"hidden_layers"`
	Output       OutputLayer   `json:"output"`
}

// Input describes the first-layer column of one feature: the L2 norm of
// its weights, the L2 norm and the largest absolute value of their change
// since initialization, the number of usable train records in which the
// feature is not 0, and whether any was. A column that no train record
// exercised never received a gradient and equals its initialization.
type Input struct {
	Name                string       `json:"name"`
	Kind                pixrisk.Kind `json:"kind"`
	ColumnNorm          float64      `json:"column_norm"`
	ChangeNorm          float64      `json:"change_norm"`
	MaxAbsChange        float64      `json:"max_abs_change"`
	TrainNonzeroRecords int          `json:"train_nonzero_records"`
	ExercisedInTrain    bool         `json:"exercised_in_train"`
}

// HiddenLayer describes one hidden layer: its neurons and the pair of
// neurons whose weight rows have the largest absolute cosine, nil for a
// layer with one neuron.
type HiddenLayer struct {
	Neurons      []Neuron `json:"neurons"`
	MaxAbsCosine *Cosine  `json:"max_abs_cosine"`
}

// Neuron describes one hidden neuron: its bias, the L2 norm of its input
// weights, the L2 norm of the weights that read it in the next layer, and
// its activation on the usable train records.
type Neuron struct {
	Bias       float64    `json:"bias"`
	InputNorm  float64    `json:"input_norm"`
	OutputNorm float64    `json:"output_norm"`
	Activation Activation `json:"activation"`
}

// Activation summarizes a neuron's output on the usable train records: its
// mean and the shares of records on which it is below 0.01 or above 0.99.
type Activation struct {
	Mean      float64 `json:"mean"`
	Below0_01 float64 `json:"below_0_01"`
	Above0_99 float64 `json:"above_0_99"`
}

// Cosine is the cosine between the weight rows of two neurons of one
// layer.
type Cosine struct {
	Value   float64 `json:"value"`
	Neurons [2]int  `json:"neurons"`
}

// OutputLayer holds the parameters of the output neuron.
type OutputLayer struct {
	Weights []float64 `json:"weights"`
	Bias    float64   `json:"bias"`
}

// PartialDependence holds, for each standardized feature, the mean score
// and the share of records at or above the threshold when the feature
// takes each value of a fixed grid in real units on every usable record of
// the partition, with its availability indicator forced to known.
type PartialDependence struct {
	Partition evaluation.Partition     `json:"partition"`
	Records   trainingdata.LabelCounts `json:"records"`
	Tolerance float64                  `json:"tolerance"`
	Features  []FeatureDependence      `json:"features"`
}

// FeatureDependence is the partial dependence of the score on one
// standardized feature. TrainRange is the range of the feature's raw
// values among the usable train records in which it is available.
type FeatureDependence struct {
	Name              string      `json:"name"`
	Unit              string      `json:"unit"`
	ExpectedDirection string      `json:"expected_direction"`
	TrainRange        Range       `json:"train_range"`
	Grid              []GridPoint `json:"grid"`
	Violations        []Violation `json:"violations"`
}

// Range is a closed interval in real units.
type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// GridPoint is one value of the grid, in real units, with the scores by
// label; a label with no record is nil.
type GridPoint struct {
	Value        float64      `json:"value"`
	InTrainRange bool         `json:"in_train_range"`
	Fraud        *LabelScores `json:"fraud"`
	Legitimate   *LabelScores `json:"legitimate"`
}

// LabelScores summarizes the scores of the records with one label.
type LabelScores struct {
	MeanScore    float64 `json:"mean_score"`
	FlaggedShare float64 `json:"flagged_share"`
}

// Violation is a move of the mean score against the expected direction of
// a feature between two consecutive grid points, by more than the
// tolerance. InTrainRange is true when both points are in the train range.
type Violation struct {
	Label         trainingdata.Label `json:"label"`
	From          float64            `json:"from"`
	To            float64            `json:"to"`
	InTrainRange  bool               `json:"in_train_range"`
	MeanScoreFrom float64            `json:"mean_score_from"`
	MeanScoreTo   float64            `json:"mean_score_to"`
}

// Missingness holds the validation metrics with every optional source
// available as the dataset supplies it, and with one source or population
// masked at a time.
type Missingness struct {
	Partition evaluation.Partition     `json:"partition"`
	Records   trainingdata.LabelCounts `json:"records"`
	Threshold float64                  `json:"threshold"`
	Baseline  Summary                  `json:"baseline"`
	Probes    []Probe                  `json:"probes"`
}

// Summary holds the metrics a probe compares: recall, false positives, the
// false positive rate, PR-AUC, and the mean score by label. A metric that
// measures nothing is nil.
type Summary struct {
	Recall            *float64   `json:"recall"`
	FalsePositives    int        `json:"false_positives"`
	FalsePositiveRate *float64   `json:"false_positive_rate"`
	PRAUC             *float64   `json:"pr_auc"`
	MeanScore         LabelMeans `json:"mean_score"`
}

// LabelMeans holds a mean by label, nil for a label with no record.
type LabelMeans struct {
	Fraud      *float64 `json:"fraud"`
	Legitimate *float64 `json:"legitimate"`
}

// Kinds of probes.
const (
	ProbeSource     = "source"
	ProbeSources    = "sources"
	ProbePopulation = "population"
)

// Probe is the result of masking one optional source, a set of sources, or
// moving every record into a population: Set lists the indicators set to
// 1 and Masked the features set to 0, exactly as pix-risk-v1 represents an
// unavailable or not applicable source. FraudsEvading counts the frauds
// at or above the threshold at baseline and below it with the probe;
// LegitimateNewlyFlagged counts the legitimate records below the
// threshold at baseline and at or above it with the probe.
type Probe struct {
	Name                   string     `json:"name"`
	Kind                   string     `json:"kind"`
	Set                    []string   `json:"set"`
	Masked                 []string   `json:"masked"`
	Result                 Summary    `json:"result"`
	FraudsEvading          int        `json:"frauds_evading"`
	LegitimateNewlyFlagged int        `json:"legitimate_newly_flagged"`
	MeanScoreChange        LabelMeans `json:"mean_score_change"`
}

// Marshal validates a report and encodes it deterministically: the same
// report always produces the same bytes, timestamps are written in UTC, and
// the output ends with a newline.
func Marshal(report Report) ([]byte, error) {
	if err := validate(report); err != nil {
		return nil, fmt.Errorf("invalid inspection report: %w", err)
	}

	report.InspectionReportVersion = Version
	report.Model.TrainEnd = report.Model.TrainEnd.UTC()
	report.Model.ValidationEnd = report.Model.ValidationEnd.UTC()
	report.Partitions.TrainEnd = report.Partitions.TrainEnd.UTC()
	report.Partitions.ValidationEnd = report.Partitions.ValidationEnd.UTC()

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode inspection report: %w", err)
	}

	return append(data, '\n'), nil
}

func validate(report Report) error {
	model, parameters := report.Model, report.Parameters
	descriptor := pixrisk.Descriptor()

	switch {
	case report.Dataset.Records.Invalid != 0:
		return errors.New("a dataset with invalid records cannot be inspected")
	case report.Dataset.DatasetSHA256 != model.TrainingDatasetSHA256:
		return errors.New("the dataset must be the model's training dataset")
	case !report.Partitions.TrainEnd.Equal(model.TrainEnd) || !report.Partitions.ValidationEnd.Equal(model.ValidationEnd):
		return errors.New("the partitions must use the model's boundaries")
	case len(parameters.Sizes) < 2 || parameters.Sizes[0] != pixrisk.FeatureCount || parameters.Sizes[len(parameters.Sizes)-1] != 1:
		return fmt.Errorf("unsupported sizes %v", parameters.Sizes)
	case len(parameters.Inputs) != pixrisk.FeatureCount:
		return fmt.Errorf("%d inputs, want %d", len(parameters.Inputs), pixrisk.FeatureCount)
	case len(parameters.HiddenLayers) != len(parameters.Sizes)-2:
		return errors.New("the hidden layers must follow the sizes")
	case len(parameters.Output.Weights) != parameters.Sizes[len(parameters.Sizes)-2]:
		return errors.New("the output layer must read the last hidden layer")
	case report.PartialDependence.Partition != evaluation.Validation || report.Missingness.Partition != evaluation.Validation:
		return errors.New("partial dependence and probes are measured on the validation partition")
	case report.PartialDependence.Tolerance != Tolerance:
		return errors.New("the tolerance must be the one of this version")
	case report.Missingness.Threshold != model.Threshold.Value:
		return errors.New("the probes must use the model's threshold")
	case report.PartialDependence.Records != report.Missingness.Records:
		return errors.New("partial dependence and probes must count the same records")
	case report.PartialDependence.Records.Fraud != report.Partitions.Validation.Fraud ||
		report.PartialDependence.Records.Legitimate != report.Partitions.Validation.Legitimate:
		return errors.New("partial dependence must count the usable validation records")
	case len(report.Missingness.Probes) != len(probes()):
		return fmt.Errorf("%d probes, want %d", len(report.Missingness.Probes), len(probes()))
	}

	var unexercised []string
	standardized := 0
	for i, feature := range descriptor {
		input := parameters.Inputs[i]
		switch {
		case input.Name != feature.Name || input.Kind != feature.Kind:
			return fmt.Errorf("input %d is %s %s, want %s %s", i, input.Name, input.Kind, feature.Name, feature.Kind)
		case input.ExercisedInTrain != (input.TrainNonzeroRecords > 0):
			return fmt.Errorf("%s: exercised_in_train must follow the train records", input.Name)
		}
		if !input.ExercisedInTrain {
			unexercised = append(unexercised, input.Name)
		}
		if feature.Kind == pixrisk.Standardized {
			if standardized >= len(report.PartialDependence.Features) ||
				report.PartialDependence.Features[standardized].Name != feature.Name {
				return fmt.Errorf("partial dependence must cover every standardized feature in order; %s is missing", feature.Name)
			}
			standardized++
		}
	}

	switch {
	case standardized != len(report.PartialDependence.Features):
		return errors.New("partial dependence covers a feature that is not standardized")
	case len(unexercised) != len(report.UnexercisedFeatures):
		return errors.New("unexercised_features must list the inputs that no train record exercised")
	}
	for i, name := range unexercised {
		if report.UnexercisedFeatures[i] != name {
			return errors.New("unexercised_features must list the inputs that no train record exercised, in order")
		}
	}

	for i, layer := range parameters.HiddenLayers {
		if len(layer.Neurons) != parameters.Sizes[i+1] {
			return fmt.Errorf("hidden layer %d has %d neurons, want %d", i, len(layer.Neurons), parameters.Sizes[i+1])
		}
		if (layer.MaxAbsCosine == nil) != (len(layer.Neurons) < 2) {
			return fmt.Errorf("hidden layer %d: the cosine is reported exactly when the layer has two neurons or more", i)
		}
	}

	for i, probe := range report.Missingness.Probes {
		if probe.Name != probes()[i].name || probe.Kind != probes()[i].kind {
			return fmt.Errorf("probe %d is %s, want %s", i, probe.Name, probes()[i].name)
		}
	}

	return nil
}
