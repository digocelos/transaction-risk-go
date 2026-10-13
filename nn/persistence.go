package nn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"time"
	"unicode/utf8"

	"github.com/digocelos/transaction-risk-go/internal/strictjson"
)

// AlgorithmFullBatchGradientDescent identifies TrainBatch: full-batch
// gradient descent on mean squared error.
const AlgorithmFullBatchGradientDescent = "full-batch-gradient-descent"

// RecordCounts holds the dataset's records by category.
type RecordCounts struct {
	Total        int `json:"total"`
	Usable       int `json:"usable"`
	Invalid      int `json:"invalid"`
	Insufficient int `json:"insufficient"`
	Immature     int `json:"immature"`
}

// Provenance records how a model was trained (ADR 0010). It never holds
// values read from the clock or from global state.
type Provenance struct {
	Algorithm              string       `json:"algorithm"`
	Seed                   int64        `json:"seed"`
	Epochs                 int          `json:"epochs"`
	LearningRate           float64      `json:"learning_rate"`
	TrainingExampleVersion string       `json:"training_example_version"`
	DatasetSHA256          string       `json:"dataset_sha256"`
	DatasetBytes           int64        `json:"dataset_bytes"`
	Records                RecordCounts `json:"records"`
	TrainEnd               time.Time    `json:"train_end"`
	ValidationEnd          time.Time    `json:"validation_end"`
	Threshold              float64      `json:"threshold"`
	ReportSHA256           string       `json:"report_sha256"`
}

// validate checks provenance ranges and consistency.
func (p Provenance) validate() error {
	counts := p.Records

	switch {
	case p.Algorithm != AlgorithmFullBatchGradientDescent:
		return fmt.Errorf("unsupported training algorithm: %q", p.Algorithm)
	case p.Epochs < 1:
		return errors.New("epochs must be at least 1")
	case !(p.LearningRate > 0) || math.IsInf(p.LearningRate, 0):
		return errors.New("learning rate must be finite and positive")
	case !isVersionName(p.TrainingExampleVersion):
		return fmt.Errorf("invalid training example version: %q", p.TrainingExampleVersion)
	case !isSHA256(p.DatasetSHA256):
		return errors.New("dataset_sha256 must be 64 lowercase hexadecimal digits")
	case p.DatasetBytes < 1:
		return errors.New("dataset_bytes must be positive")
	case counts.Total < 0 || counts.Usable < 1 || counts.Invalid < 0 ||
		counts.Insufficient < 0 || counts.Immature < 0:
		return errors.New("record counts must not be negative, and at least one record must be usable")
	case counts.Usable+counts.Invalid+counts.Insufficient+counts.Immature != counts.Total:
		return errors.New("record counts must add up to the total")
	case p.TrainEnd.IsZero() || p.ValidationEnd.IsZero() || !p.TrainEnd.Before(p.ValidationEnd):
		return errors.New("train_end must be set and earlier than validation_end")
	case !(p.Threshold >= 0 && p.Threshold <= 1):
		return errors.New("threshold must be a number from 0 to 1")
	case !isSHA256(p.ReportSHA256):
		return errors.New("report_sha256 must be 64 lowercase hexadecimal digits")
	}

	return nil
}

func isSHA256(text string) bool {
	if len(text) != 64 {
		return false
	}

	for i := 0; i < len(text); i++ {
		c := text[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}

	return true
}

// isVersionName matches ^[a-z0-9][a-z0-9.-]{0,63}$.
func isVersionName(text string) bool {
	if len(text) < 1 || len(text) > 64 {
		return false
	}

	for i := 0; i < len(text); i++ {
		c := text[i]
		alphanumeric := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !alphanumeric && (i == 0 || (c != '.' && c != '-')) {
			return false
		}
	}

	return true
}

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
	Provenance    *Provenance   `json:"provenance,omitempty"`
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

// MarshalWithProvenance encodes the network and its provenance as model
// format version 2. The same network and provenance always produce the
// same bytes; timestamps are written in UTC.
func (n *Network) MarshalWithProvenance(featureSchema string, provenance Provenance) ([]byte, error) {
	if n.inputSize <= 0 || len(n.layers) == 0 {
		return nil, fmt.Errorf("cannot save an empty network")
	}

	if featureSchema == "" {
		return nil, fmt.Errorf("feature schema cannot be empty")
	}

	if err := provenance.validate(); err != nil {
		return nil, fmt.Errorf("invalid provenance: %w", err)
	}

	provenance.TrainEnd = provenance.TrainEnd.UTC()
	provenance.ValidationEnd = provenance.ValidationEnd.UTC()

	stored := storedModel{
		FormatVersion: 2,
		FeatureSchema: featureSchema,
		Activation:    "sigmoid",
		InputSize:     n.inputSize,
		Layers:        make([]storedLayer, len(n.layers)),
		Provenance:    &provenance,
	}

	for i, layer := range n.layers {
		stored.Layers[i] = storedLayer{
			Weights: layer.weights,
			Biases:  layer.biases,
		}
	}

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode model: %w", err)
	}

	return data, nil
}

// SaveWithProvenance writes the network and its provenance to a file in
// model format version 2.
func (n *Network) SaveWithProvenance(path, featureSchema string, provenance Provenance) error {
	data, err := n.MarshalWithProvenance(featureSchema, provenance)
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write model: %w", err)
	}

	return nil
}

// Load reads and validates a model against the expected feature schema.
// It accepts model format versions 1 and 2.
func Load(path, expectedFeatureSchema string) (*Network, error) {
	network, _, err := LoadWithProvenance(path, expectedFeatureSchema)
	return network, err
}

// LoadWithProvenance reads a model and, for format version 2, its
// provenance. Format version 1 is read exactly as before and has no
// provenance. Format version 2 is decoded strictly, and malformed or
// out-of-range provenance fails.
func LoadWithProvenance(path, expectedFeatureSchema string) (*Network, *Provenance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read model: %w", err)
	}

	var header struct {
		FormatVersion int `json:"format_version"`
	}

	if err := json.Unmarshal(data, &header); err != nil {
		return nil, nil, fmt.Errorf("decode model: %w", err)
	}

	var stored storedModel

	switch header.FormatVersion {
	case 1:
		if err := json.Unmarshal(data, &stored); err != nil {
			return nil, nil, fmt.Errorf("decode model: %w", err)
		}
		stored.Provenance = nil
	case 2:
		if err := decodeStrict(data, &stored); err != nil {
			return nil, nil, fmt.Errorf("decode model: %w", err)
		}
		if stored.Provenance == nil {
			return nil, nil, fmt.Errorf("model format version 2 requires provenance")
		}
		if err := stored.Provenance.validate(); err != nil {
			return nil, nil, fmt.Errorf("invalid provenance: %w", err)
		}
	default:
		return nil, nil, fmt.Errorf(
			"unsupported model format version: %d",
			header.FormatVersion,
		)
	}

	network, err := buildNetwork(stored, expectedFeatureSchema)
	if err != nil {
		return nil, nil, err
	}

	return network, stored.Provenance, nil
}

// decodeStrict rejects invalid UTF-8, duplicate members, trailing data,
// and unknown members before decoding.
func decodeStrict(data []byte, stored *storedModel) error {
	if !utf8.Valid(data) {
		return errors.New("model is not valid UTF-8")
	}

	_, duplicates, err := strictjson.Parse(data)
	if err != nil {
		return fmt.Errorf("model is not valid JSON: %w", err)
	}

	if len(duplicates) > 0 {
		return fmt.Errorf("duplicate member %s", duplicates[0])
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	return decoder.Decode(stored)
}

// buildNetwork validates stored parameters against the expected feature
// schema.
func buildNetwork(stored storedModel, expectedFeatureSchema string) (*Network, error) {
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
