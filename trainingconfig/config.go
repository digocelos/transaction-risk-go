// Package trainingconfig decodes and validates training configurations
// that follow pix-training-config-v1, as specified in
// docs/contracts/pix-training-config-v1.md. A configuration fixes every
// option of a training run before any dataset is read or any artifact is
// written.
package trainingconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/internal/strictjson"
	"github.com/digocelos/transaction-risk-go/pixrisk"
)

// Version is the only configuration version this package supports.
const Version = "pix-training-config-v1"

// MaxDocumentSize is the largest configuration document, in bytes.
const MaxDocumentSize = 65536

// Resource limits of pix-training-config-v1.
const (
	MaxHiddenLayers  = 4
	MaxLayerNeurons  = 256
	MaxEpochs        = 1000000
	MaxLearningRate  = 100.0
	thresholdMinimum = 0.0
	thresholdMaximum = 1.0
)

// Supported options. Each option has exactly one supported value in this
// version; any other value is rejected.
const (
	ActivationSigmoid          = "sigmoid"
	AlgorithmFullBatchGradient = "full-batch-gradient-descent"
	LossMeanSquaredError       = "mean-squared-error"
	CheckpointFinalEpoch       = "final_epoch"
	ThresholdFixed             = "fixed"
)

// Validation codes. The catalog is closed.
const (
	CodeDocumentTooLarge         = "document_too_large"
	CodeBOMNotAllowed            = strictjson.CodeBOMNotAllowed
	CodeInvalidUTF8              = strictjson.CodeInvalidUTF8
	CodeInvalidJSON              = strictjson.CodeInvalidJSON
	CodeTrailingData             = strictjson.CodeTrailingData
	CodeWrongType                = strictjson.CodeWrongType
	CodeDuplicateMember          = strictjson.CodeDuplicateMember
	CodeRequired                 = strictjson.CodeRequired
	CodeUnsupportedConfigVersion = "unsupported_config_version"
	CodeUnknownMember            = strictjson.CodeUnknownMember
	CodeNullNotAllowed           = strictjson.CodeNullNotAllowed
	CodeInvalidNumberFormat      = strictjson.CodeInvalidNumberFormat
	CodeOutOfRange               = strictjson.CodeOutOfRange
	CodeInvalidFormat            = strictjson.CodeInvalidFormat
	CodeInvalidEnumValue         = strictjson.CodeInvalidEnumValue
	CodeBoundariesOutOfOrder     = "boundaries_out_of_order"
)

// Config is a valid pix-training-config-v1 configuration.
type Config struct {
	FeatureSchema string
	HiddenLayers  []int
	Activation    string
	Algorithm     string
	Loss          string
	Seed          int64
	LearningRate  float64
	MaxEpochs     int
	TrainEnd      time.Time
	ValidationEnd time.Time
	Checkpoint    string
	Threshold     Threshold
}

// Threshold is the decision threshold policy.
type Threshold struct {
	Policy string
	Value  float64
}

// Sizes returns the layer sizes of the network: the feature count of the
// feature schema, the hidden layers, and one risk output.
func (c Config) Sizes() []int {
	sizes := []int{pixrisk.FeatureCount}
	sizes = append(sizes, c.HiddenLayers...)
	return append(sizes, 1)
}

// Boundaries returns the temporal partition boundaries.
func (c Config) Boundaries() evaluation.Boundaries {
	return evaluation.Boundaries{TrainEnd: c.TrainEnd, ValidationEnd: c.ValidationEnd}
}

// Issue is one reason why a document is not a valid configuration. Path is
// a JSON Pointer; Message is for humans and may change.
type Issue struct {
	Path    string
	Code    string
	Message string
}

// ValidationError lists every issue of a configuration, sorted by path and
// then by code.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	first := e.Issues[0]
	message := fmt.Sprintf("invalid training configuration: %s: %s: %s", first.Path, first.Code, first.Message)

	if len(e.Issues) > 1 {
		message += fmt.Sprintf(" (and %d more issues)", len(e.Issues)-1)
	}

	return message
}

func newValidationError(issues []strictjson.Issue) *ValidationError {
	err := &ValidationError{}
	for _, issue := range strictjson.SortIssues(issues) {
		err.Issues = append(err.Issues, Issue(issue))
	}
	return err
}

// Read reads and decodes one configuration document, stopping as soon as
// it exceeds MaxDocumentSize.
func Read(r io.Reader) (Config, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxDocumentSize+1))
	if err != nil {
		return Config{}, fmt.Errorf("read training configuration: %w", err)
	}

	return Decode(data)
}

// Decode validates a configuration document and returns its typed form. A
// document that breaks pix-training-config-v1 yields a *ValidationError.
func Decode(data []byte) (Config, error) {
	root, issues := parseDocument(data)
	if len(issues) > 0 {
		return Config{}, newValidationError(issues)
	}

	if issue := checkVersion(root); issue != nil {
		return Config{}, newValidationError([]strictjson.Issue{*issue})
	}

	d := &decoder{}
	config := d.config(root)

	if len(d.Issues()) > 0 {
		return Config{}, newValidationError(d.Issues())
	}

	return config, nil
}

func parseDocument(data []byte) (*strictjson.Value, []strictjson.Issue) {
	single := func(code, message string) []strictjson.Issue {
		return []strictjson.Issue{{Path: "", Code: code, Message: message}}
	}

	switch {
	case len(data) > MaxDocumentSize:
		return nil, single(CodeDocumentTooLarge, fmt.Sprintf("document exceeds %d bytes", MaxDocumentSize))
	case bytes.HasPrefix(data, []byte("\xEF\xBB\xBF")):
		return nil, single(CodeBOMNotAllowed, "document must not start with a byte order mark")
	case !utf8.Valid(data):
		return nil, single(CodeInvalidUTF8, "document is not valid UTF-8")
	}

	root, duplicates, err := strictjson.Parse(data)

	switch {
	case errors.Is(err, strictjson.ErrTrailingData):
		return nil, single(CodeTrailingData, "only JSON whitespace may follow the top-level value")
	case err != nil:
		return nil, single(CodeInvalidJSON, "document is not valid JSON")
	case root.Kind != strictjson.KindObject:
		return nil, single(CodeWrongType, "document must be a JSON object")
	}

	var issues []strictjson.Issue
	for _, path := range duplicates {
		issues = append(issues, strictjson.Issue{
			Path: path, Code: CodeDuplicateMember, Message: "member appears more than once in the same object",
		})
	}

	return root, issues
}

func checkVersion(root *strictjson.Value) *strictjson.Issue {
	const path = "/config_version"

	value, ok := root.Members["config_version"]

	switch {
	case !ok:
		return &strictjson.Issue{Path: path, Code: CodeRequired, Message: "config_version is required"}
	case value.Kind == strictjson.KindNull:
		return &strictjson.Issue{Path: path, Code: CodeNullNotAllowed, Message: "config_version must not be null"}
	case value.Kind != strictjson.KindString:
		return &strictjson.Issue{Path: path, Code: CodeWrongType, Message: "config_version must be a string"}
	case value.Text != Version:
		return &strictjson.Issue{Path: path, Code: CodeUnsupportedConfigVersion,
			Message: fmt.Sprintf("unsupported configuration version %q", value.Text)}
	}

	return nil
}

type decoder struct {
	strictjson.Validator
}

func (d *decoder) config(root *strictjson.Value) Config {
	d.Object("", root, "config_version", "feature_schema", "architecture", "algorithm", "loss",
		"seed", "learning_rate", "max_epochs", "boundaries", "selection")

	config := Config{
		FeatureSchema: d.Enum("", root, "feature_schema", pixrisk.Schema),
		Algorithm:     d.Enum("", root, "algorithm", AlgorithmFullBatchGradient),
		Loss:          d.Enum("", root, "loss", LossMeanSquaredError),
	}

	seed, _ := d.Integer("", root, "seed", 0, false)
	config.Seed = seed

	epochs, state := d.Integer("", root, "max_epochs", 1, false)
	if state == strictjson.Valid && epochs > MaxEpochs {
		d.Add("/max_epochs", CodeOutOfRange, "max_epochs must be at most %d", MaxEpochs)
	}
	config.MaxEpochs = int(epochs)

	config.LearningRate = d.number("", root, "learning_rate", func(value float64) bool {
		return value > 0 && value <= MaxLearningRate
	}, "a number greater than 0 and at most 100")

	d.architecture(root, &config)
	d.boundaries(root, &config)
	d.selection(root, &config)

	return config
}

// number reads a JSON number member that satisfies inRange, described by
// rangeText in messages.
func (d *decoder) number(path string, parent *strictjson.Value, name string, inRange func(float64) bool, rangeText string) float64 {
	value, memberPath, state := d.Member(path, parent, name, false)
	if state != strictjson.Valid {
		return 0
	}

	if value.Kind != strictjson.KindNumber {
		d.Add(memberPath, CodeWrongType, "%s must be a number", name)
		return 0
	}

	number, err := strconv.ParseFloat(value.Text, 64)
	if err != nil || math.IsInf(number, 0) || !inRange(number) {
		d.Add(memberPath, CodeOutOfRange, "%s must be %s", name, rangeText)
		return 0
	}

	return number
}

func (d *decoder) architecture(root *strictjson.Value, config *Config) {
	object, path := d.ObjectMember("", root, "architecture", false, "hidden_layers", "activation")
	if object == nil {
		return
	}

	config.Activation = d.Enum(path, object, "activation", ActivationSigmoid)

	layers, layersPath, state := d.Member(path, object, "hidden_layers", false)
	if state != strictjson.Valid {
		return
	}

	if layers.Kind != strictjson.KindArray {
		d.Add(layersPath, CodeWrongType, "hidden_layers must be an array")
		return
	}

	if len(layers.Items) > MaxHiddenLayers {
		d.Add(layersPath, CodeOutOfRange, "hidden_layers must have at most %d layers", MaxHiddenLayers)
		return
	}

	config.HiddenLayers = []int{}
	for i, item := range layers.Items {
		itemPath := strictjson.Pointer(layersPath, strconv.Itoa(i))

		switch {
		case item.Kind != strictjson.KindNumber:
			d.Add(itemPath, CodeWrongType, "a hidden layer size must be an integer")
			continue
		case !strictjson.IsUnsignedInteger(item.Text):
			d.Add(itemPath, CodeInvalidNumberFormat,
				"a hidden layer size must be an unsigned integer without fraction or exponent")
			continue
		}

		size, err := strconv.Atoi(item.Text)
		if err != nil || size < 1 || size > MaxLayerNeurons {
			d.Add(itemPath, CodeOutOfRange, "a hidden layer must have from 1 to %d neurons", MaxLayerNeurons)
			continue
		}

		config.HiddenLayers = append(config.HiddenLayers, size)
	}
}

func (d *decoder) boundaries(root *strictjson.Value, config *Config) {
	object, path := d.ObjectMember("", root, "boundaries", false, "train_end", "validation_end")
	if object == nil {
		return
	}

	trainEnd, trainOK := d.Timestamp(path, object, "train_end")
	validationEnd, validationOK := d.Timestamp(path, object, "validation_end")

	if trainOK && validationOK && !trainEnd.Before(validationEnd) {
		d.Add(path+"/train_end", CodeBoundariesOutOfOrder, "train_end must be earlier than validation_end")
	}

	config.TrainEnd, config.ValidationEnd = trainEnd, validationEnd
}

func (d *decoder) selection(root *strictjson.Value, config *Config) {
	object, path := d.ObjectMember("", root, "selection", false, "checkpoint", "threshold")
	if object == nil {
		return
	}

	config.Checkpoint = d.Enum(path, object, "checkpoint", CheckpointFinalEpoch)

	threshold, thresholdPath := d.ObjectMember(path, object, "threshold", false, "policy", "value")
	if threshold == nil {
		return
	}

	config.Threshold = Threshold{
		Policy: d.Enum(thresholdPath, threshold, "policy", ThresholdFixed),
		Value: d.number(thresholdPath, threshold, "value", func(value float64) bool {
			return value >= thresholdMinimum && value <= thresholdMaximum
		}, "a number from 0 to 1"),
	}
}

// Validate checks a configuration built in code against the rules Decode
// applies: supported options, dimensions, ranges, and boundary order.
func (c Config) Validate() error {
	switch {
	case c.FeatureSchema != pixrisk.Schema:
		return fmt.Errorf("feature schema %q is not supported", c.FeatureSchema)
	case c.Activation != ActivationSigmoid:
		return fmt.Errorf("activation %q is not supported", c.Activation)
	case c.Algorithm != AlgorithmFullBatchGradient:
		return fmt.Errorf("algorithm %q is not supported", c.Algorithm)
	case c.Loss != LossMeanSquaredError:
		return fmt.Errorf("loss %q is not supported", c.Loss)
	case c.Checkpoint != CheckpointFinalEpoch:
		return fmt.Errorf("checkpoint %q is not supported", c.Checkpoint)
	case c.Threshold.Policy != ThresholdFixed:
		return fmt.Errorf("threshold policy %q is not supported", c.Threshold.Policy)
	case len(c.HiddenLayers) > MaxHiddenLayers:
		return fmt.Errorf("at most %d hidden layers are supported", MaxHiddenLayers)
	case c.Seed < 0 || c.Seed > strictjson.MaxInteger:
		return fmt.Errorf("seed must be from 0 to %d", int64(strictjson.MaxInteger))
	case !(c.LearningRate > 0 && c.LearningRate <= MaxLearningRate):
		return fmt.Errorf("learning rate must be greater than 0 and at most %v", MaxLearningRate)
	case c.MaxEpochs < 1 || c.MaxEpochs > MaxEpochs:
		return fmt.Errorf("max epochs must be from 1 to %d", MaxEpochs)
	case !(c.Threshold.Value >= thresholdMinimum && c.Threshold.Value <= thresholdMaximum):
		return errors.New("threshold must be a number from 0 to 1")
	case c.TrainEnd.IsZero() || c.ValidationEnd.IsZero() || !c.TrainEnd.Before(c.ValidationEnd):
		return errors.New("train_end must be earlier than validation_end")
	}

	for _, size := range c.HiddenLayers {
		if size < 1 || size > MaxLayerNeurons {
			return fmt.Errorf("a hidden layer must have from 1 to %d neurons", MaxLayerNeurons)
		}
	}

	return nil
}
