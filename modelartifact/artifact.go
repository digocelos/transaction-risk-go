// Package modelartifact encodes and decodes the trained model artifact
// pix-risk-model-v1, as specified in docs/contracts/pix-risk-model-v1.md:
// everything the toolkit needs to score pix-payer-input-v2 inputs without
// training, and the identities of the configuration, dataset, and report
// that produced it. Its SHA-256 values detect accidental or deliberate
// changes; they are not signatures and prove nothing about who made the
// artifact (ADR 0016).
package modelartifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime/debug"
	"time"
	"unicode/utf8"

	"github.com/digocelos/transaction-risk-go/internal/strictjson"
	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// Version is the artifact version.
const Version = "pix-risk-model-v1"

// Unknown marks software information the build does not carry.
const Unknown = "unknown"

// Software identifies the toolkit build that trained a model.
type Software struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

// SoftwareFromBuild reads the identity of the running binary from its Go
// build information: the main module, its version, and the VCS revision,
// suffixed with "+dirty" when the working tree had changes. Missing values
// are Unknown. It never runs git, so one binary always reports the same
// identity.
func SoftwareFromBuild() Software {
	info, _ := debug.ReadBuildInfo()
	return softwareFrom(info)
}

func softwareFrom(info *debug.BuildInfo) Software {
	software := Software{Module: Unknown, Version: Unknown, Revision: Unknown}
	if info == nil {
		return software
	}

	if info.Main.Path != "" {
		software.Module = info.Main.Path
	}
	if info.Main.Version != "" {
		software.Version = info.Main.Version
	}

	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if setting.Value != "" {
				software.Revision = setting.Value
			}
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}

	if modified && software.Revision != Unknown {
		software.Revision += "+dirty"
	}

	return software
}

// Threshold is the decision threshold the model was selected with.
type Threshold struct {
	Policy string  `json:"policy"`
	Value  float64 `json:"value"`
}

// Training identifies what produced the model.
type Training struct {
	ConfigSHA256  string              `json:"config_sha256"`
	DatasetSHA256 string              `json:"dataset_sha256"`
	DatasetBytes  int64               `json:"dataset_bytes"`
	Records       trainingdata.Counts `json:"records"`
	TrainEnd      time.Time           `json:"train_end"`
	ValidationEnd time.Time           `json:"validation_end"`
	ReportSHA256  string              `json:"report_sha256"`
}

// Artifact is a trained pix-risk-v1 model. The versions of the contracts,
// the feature schema and its descriptor, the configuration, and the report
// are fixed by the artifact version.
type Artifact struct {
	Software      Software
	State         pixrisk.State
	Network       *nn.Network
	Sizes         []int
	Activation    string
	Loss          string
	Algorithm     string
	SelectedEpoch int
	Threshold     Threshold
	Training      Training
}

// FromTraining builds the artifact of a trained network from its
// preparation state, its training report, and the SHA-256 of the report's
// exact bytes.
func FromTraining(network *nn.Network, state pixrisk.State, report trainingreport.Report, reportSHA256 string, software Software) (Artifact, error) {
	config := report.Configuration.Config

	configSHA256, err := config.SHA256()
	if err != nil {
		return Artifact{}, fmt.Errorf("model artifact: %w", err)
	}

	artifact := Artifact{
		Software:      software,
		State:         state,
		Network:       network,
		Sizes:         config.Sizes(),
		Activation:    config.Activation,
		Loss:          config.Loss,
		Algorithm:     config.Algorithm,
		SelectedEpoch: report.Selection.SelectedEpoch,
		Threshold:     Threshold{Policy: report.Selection.Threshold.Policy, Value: report.Selection.Threshold.Value},
		Training: Training{
			ConfigSHA256:  configSHA256,
			DatasetSHA256: report.Dataset.DatasetSHA256,
			DatasetBytes:  report.Dataset.DatasetBytes,
			Records:       report.Dataset.Records,
			TrainEnd:      report.Partitions.TrainEnd,
			ValidationEnd: report.Partitions.ValidationEnd,
			ReportSHA256:  reportSHA256,
		},
	}

	if err := artifact.validate(); err != nil {
		return Artifact{}, fmt.Errorf("model artifact: %w", err)
	}

	return artifact, nil
}

// Feature is a descriptor entry. Indicator is nil for a feature that is
// always available.
type feature struct {
	Name      string       `json:"name"`
	Kind      pixrisk.Kind `json:"kind"`
	Indicator *int         `json:"indicator"`
}

type stored struct {
	ArtifactVersion string          `json:"artifact_version"`
	Software        Software        `json:"software"`
	Contracts       storedContracts `json:"contracts"`
	Features        storedFeatures  `json:"features"`
	Network         storedNetwork   `json:"network"`
	Selection       storedSelection `json:"selection"`
	Training        storedTraining  `json:"training"`
}

type storedContracts struct {
	ScoringInput    string `json:"scoring_input"`
	TrainingExample string `json:"training_example"`
}

type storedFeatures struct {
	FeatureSchema     string        `json:"feature_schema"`
	Descriptor        []feature     `json:"descriptor"`
	Preparation       pixrisk.State `json:"preparation"`
	PreparationSHA256 string        `json:"preparation_sha256"`
}

type storedNetwork struct {
	Sizes            []int      `json:"sizes"`
	Activation       string     `json:"activation"`
	Loss             string     `json:"loss"`
	Algorithm        string     `json:"algorithm"`
	Layers           []nn.Layer `json:"layers"`
	ParametersSHA256 string     `json:"parameters_sha256"`
}

type storedSelection struct {
	SelectedEpoch int       `json:"selected_epoch"`
	Threshold     Threshold `json:"threshold"`
}

type storedTraining struct {
	ConfigVersion string              `json:"config_version"`
	ConfigSHA256  string              `json:"config_sha256"`
	DatasetSHA256 string              `json:"dataset_sha256"`
	DatasetBytes  int64               `json:"dataset_bytes"`
	Records       trainingdata.Counts `json:"records"`
	TrainEnd      time.Time           `json:"train_end"`
	ValidationEnd time.Time           `json:"validation_end"`
	ReportVersion string              `json:"report_version"`
	ReportSHA256  string              `json:"report_sha256"`
}

func descriptor() []feature {
	features := pixrisk.Descriptor()
	entries := make([]feature, len(features))
	for i, f := range features {
		entries[i] = feature{Name: f.Name, Kind: f.Kind}
		if f.Indicator != pixrisk.Always {
			indicator := f.Indicator
			entries[i].Indicator = &indicator
		}
	}
	return entries
}

// sha256JSON returns the SHA-256 of the compact JSON encoding of value.
// Numbers use their shortest round-trip form, so decoding and encoding
// again gives the same bytes.
func sha256JSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Marshal encodes an artifact deterministically, with timestamps in UTC,
// the self-hashes of its preparation state and parameters, and a final
// newline.
func Marshal(artifact Artifact) ([]byte, error) {
	if err := artifact.validate(); err != nil {
		return nil, fmt.Errorf("model artifact: %w", err)
	}

	layers := artifact.Network.Layers()

	preparationSHA256, err := sha256JSON(artifact.State)
	if err != nil {
		return nil, err
	}
	parametersSHA256, err := sha256JSON(layers)
	if err != nil {
		return nil, err
	}

	training := artifact.Training
	value := stored{
		ArtifactVersion: Version,
		Software:        artifact.Software,
		Contracts:       storedContracts{ScoringInput: pixinput.ContractV2, TrainingExample: trainingdata.TrainingExampleV2},
		Features: storedFeatures{
			FeatureSchema:     pixrisk.Schema,
			Descriptor:        descriptor(),
			Preparation:       artifact.State,
			PreparationSHA256: preparationSHA256,
		},
		Network: storedNetwork{
			Sizes:            artifact.Sizes,
			Activation:       artifact.Activation,
			Loss:             artifact.Loss,
			Algorithm:        artifact.Algorithm,
			Layers:           layers,
			ParametersSHA256: parametersSHA256,
		},
		Selection: storedSelection{SelectedEpoch: artifact.SelectedEpoch, Threshold: artifact.Threshold},
		Training: storedTraining{
			ConfigVersion: trainingconfig.Version,
			ConfigSHA256:  training.ConfigSHA256,
			DatasetSHA256: training.DatasetSHA256,
			DatasetBytes:  training.DatasetBytes,
			Records:       training.Records,
			TrainEnd:      training.TrainEnd.UTC(),
			ValidationEnd: training.ValidationEnd.UTC(),
			ReportVersion: trainingreport.Version,
			ReportSHA256:  training.ReportSHA256,
		},
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode model artifact: %w", err)
	}

	return append(data, '\n'), nil
}

// Load reads and decodes an artifact file.
func Load(path string) (Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("read model artifact: %w", err)
	}

	return Decode(data)
}

// Decode decodes an artifact strictly: valid UTF-8, no duplicate or
// unknown members, no trailing data, the supported versions, the exact
// pix-risk-v1 descriptor, a valid preparation state, parameters that match
// the architecture and are finite, and self-hashes that match the content.
func Decode(data []byte) (Artifact, error) {
	var value stored
	if err := decodeStrict(data, &value); err != nil {
		return Artifact{}, fmt.Errorf("decode model artifact: %w", err)
	}

	features, network, training := value.Features, value.Network, value.Training

	switch {
	case value.ArtifactVersion != Version:
		return Artifact{}, fmt.Errorf("unsupported model artifact version: %q", value.ArtifactVersion)
	case value.Contracts != storedContracts{ScoringInput: pixinput.ContractV2, TrainingExample: trainingdata.TrainingExampleV2}:
		return Artifact{}, fmt.Errorf("unsupported contracts: %+v", value.Contracts)
	case features.FeatureSchema != pixrisk.Schema:
		return Artifact{}, fmt.Errorf("unsupported feature schema: %q", features.FeatureSchema)
	case !reflect.DeepEqual(features.Descriptor, descriptor()):
		return Artifact{}, errors.New("the feature descriptor is not the pix-risk-v1 descriptor")
	case training.ConfigVersion != trainingconfig.Version:
		return Artifact{}, fmt.Errorf("unsupported configuration version: %q", training.ConfigVersion)
	case training.ReportVersion != trainingreport.Version:
		return Artifact{}, fmt.Errorf("unsupported report version: %q", training.ReportVersion)
	}

	if sum, err := sha256JSON(features.Preparation); err != nil || sum != features.PreparationSHA256 {
		return Artifact{}, errors.New("preparation_sha256 does not match the preparation state")
	}
	if sum, err := sha256JSON(network.Layers); err != nil || sum != network.ParametersSHA256 {
		return Artifact{}, errors.New("parameters_sha256 does not match the parameters")
	}

	if len(network.Sizes) == 0 {
		return Artifact{}, errors.New("the network has no sizes")
	}

	built, err := nn.FromLayers(network.Sizes[0], network.Layers)
	if err != nil {
		return Artifact{}, fmt.Errorf("invalid parameters: %w", err)
	}

	artifact := Artifact{
		Software:      value.Software,
		State:         features.Preparation,
		Network:       built,
		Sizes:         network.Sizes,
		Activation:    network.Activation,
		Loss:          network.Loss,
		Algorithm:     network.Algorithm,
		SelectedEpoch: value.Selection.SelectedEpoch,
		Threshold:     value.Selection.Threshold,
		Training: Training{
			ConfigSHA256:  training.ConfigSHA256,
			DatasetSHA256: training.DatasetSHA256,
			DatasetBytes:  training.DatasetBytes,
			Records:       training.Records,
			TrainEnd:      training.TrainEnd,
			ValidationEnd: training.ValidationEnd,
			ReportSHA256:  training.ReportSHA256,
		},
	}

	if err := artifact.validate(); err != nil {
		return Artifact{}, fmt.Errorf("model artifact: %w", err)
	}

	return artifact, nil
}

// decodeStrict rejects invalid UTF-8, duplicate members, trailing data,
// and unknown members before decoding.
func decodeStrict(data []byte, value *stored) error {
	if !utf8.Valid(data) {
		return errors.New("not valid UTF-8")
	}

	root, duplicates, err := strictjson.Parse(data)
	if err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	if len(duplicates) > 0 {
		return fmt.Errorf("duplicate member %s", duplicates[0])
	}
	if root.Kind != strictjson.KindObject {
		return errors.New("not a JSON object")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	return decoder.Decode(value)
}

// validate checks that an artifact can score pix-risk-v1 inputs and that
// its identities are well formed.
func (a Artifact) validate() error {
	if err := a.State.Validate(); err != nil {
		return err
	}

	switch {
	case a.Network == nil:
		return errors.New("the network is missing")
	case !validSizes(a.Sizes):
		return fmt.Errorf("unsupported network sizes %v", a.Sizes)
	case !sameSizes(a.Network, a.Sizes):
		return fmt.Errorf("the parameters do not match the sizes %v", a.Sizes)
	case a.Activation != trainingconfig.ActivationSigmoid:
		return fmt.Errorf("unsupported activation: %q", a.Activation)
	case a.Loss != trainingconfig.LossMeanSquaredError:
		return fmt.Errorf("unsupported loss: %q", a.Loss)
	case a.Algorithm != trainingconfig.AlgorithmFullBatchGradient:
		return fmt.Errorf("unsupported algorithm: %q", a.Algorithm)
	case a.SelectedEpoch < 1 || a.SelectedEpoch > trainingconfig.MaxEpochs:
		return fmt.Errorf("selected epoch %d is out of range", a.SelectedEpoch)
	case a.Threshold.Policy != trainingconfig.ThresholdFixed &&
		a.Threshold.Policy != trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate:
		return fmt.Errorf("unsupported threshold policy: %q", a.Threshold.Policy)
	case !(a.Threshold.Value >= 0 && a.Threshold.Value <= 1):
		return errors.New("the threshold must be a number from 0 to 1")
	case !validText(a.Software.Module) || !validText(a.Software.Version) || !validText(a.Software.Revision):
		return errors.New("software module, version, and revision must be printable text of 1 to 256 bytes")
	}

	return a.Training.validate()
}

func (t Training) validate() error {
	counts := t.Records

	switch {
	case !isSHA256(t.ConfigSHA256) || !isSHA256(t.DatasetSHA256) || !isSHA256(t.ReportSHA256):
		return errors.New("config_sha256, dataset_sha256, and report_sha256 must be 64 lowercase hexadecimal digits")
	case t.DatasetBytes < 1:
		return errors.New("dataset_bytes must be positive")
	case counts.Usable < 1 || counts.Invalid != 0 || counts.Insufficient < 0 || counts.Immature < 0 ||
		counts.Usable+counts.Insufficient+counts.Immature != counts.Total:
		return errors.New("record counts must add up, with usable records and no invalid record")
	case t.TrainEnd.IsZero() || !t.TrainEnd.Before(t.ValidationEnd):
		return errors.New("train_end must be set and earlier than validation_end")
	}

	return nil
}

// validSizes accepts [62, hidden..., 1] with the configuration's limits.
func validSizes(sizes []int) bool {
	if len(sizes) < 2 || len(sizes) > trainingconfig.MaxHiddenLayers+2 ||
		sizes[0] != pixrisk.FeatureCount || sizes[len(sizes)-1] != 1 {
		return false
	}

	for _, size := range sizes[1 : len(sizes)-1] {
		if size < 1 || size > trainingconfig.MaxLayerNeurons {
			return false
		}
	}

	return true
}

func sameSizes(network *nn.Network, sizes []int) bool {
	layers := network.Layers()
	if network.InputSize() != sizes[0] || len(layers) != len(sizes)-1 {
		return false
	}

	for i, layer := range layers {
		if len(layer.Biases) != sizes[i+1] {
			return false
		}
	}

	return true
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

func validText(text string) bool {
	if len(text) < 1 || len(text) > 256 {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < 0x20 || text[i] > 0x7e {
			return false
		}
	}
	return true
}
