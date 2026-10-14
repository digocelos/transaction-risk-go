// Package trainingconfig decodes and validates training configurations
// that follow pix-training-config-v2, as specified in
// docs/contracts/pix-training-config-v2.md. A configuration fixes every
// option of a training run before any dataset is read or any artifact is
// written.
package trainingconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/internal/strictjson"
	"github.com/digocelos/transaction-risk-go/pixrisk"
)

// Version is the only configuration version this package supports.
const Version = "pix-training-config-v2"

// MaxDocumentSize is the largest configuration document, in bytes.
const MaxDocumentSize = 65536

// Resource limits of pix-training-config-v2.
const (
	MaxHiddenLayers  = 4
	MaxLayerNeurons  = 256
	MaxEpochs        = 1000000
	MaxLearningRate  = 100.0
	thresholdMinimum = 0.0
	thresholdMaximum = 1.0
	MaxImprovement   = 1.0
)

// Supported options. Any other value is rejected.
const (
	ActivationSigmoid          = "sigmoid"
	AlgorithmFullBatchGradient = "full-batch-gradient-descent"
	LossMeanSquaredError       = "mean-squared-error"

	CheckpointFinalEpoch     = "final_epoch"
	CheckpointBestValidation = "best_validation"

	ObjectiveValidationLoss  = "validation_loss"
	ObjectiveValidationPRAUC = "validation_pr_auc"

	EarlyStoppingNone     = "none"
	EarlyStoppingPatience = "patience"

	ThresholdFixed                           = "fixed"
	ThresholdMaxRecallAtMaxFalsePositiveRate = "max_recall_at_max_false_positive_rate"

	TestSkip           = "skip"
	TestAfterSelection = "after_selection"
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
	CodeIncompatibleOptions      = "incompatible_options"
)

// Config is a valid pix-training-config-v2 configuration.
type Config struct {
	FeatureSchema  string
	HiddenLayers   []int
	Activation     string
	Algorithm      string
	Loss           string
	Seed           int64
	LearningRate   float64
	MaxEpochs      int
	TrainEnd       time.Time
	ValidationEnd  time.Time
	Checkpoint     Checkpoint
	EarlyStopping  EarlyStopping
	Threshold      Threshold
	TestEvaluation string
}

// Checkpoint chooses which epoch's network is kept. Objective,
// EvaluationInterval, and MinImprovement apply to best_validation only.
type Checkpoint struct {
	Policy             string
	Objective          string
	EvaluationInterval int
	MinImprovement     float64
}

// EarlyStopping stops training after Patience validation evaluations
// without improvement. Patience applies to the patience policy only.
type EarlyStopping struct {
	Policy   string
	Patience int
}

// Threshold is the decision threshold policy. Value applies to fixed, and
// MaxFalsePositiveRate to max_recall_at_max_false_positive_rate.
type Threshold struct {
	Policy               string
	Value                float64
	MaxFalsePositiveRate float64
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
// document that breaks pix-training-config-v2 yields a *ValidationError.
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
	object, path := d.ObjectMember("", root, "selection", false,
		"checkpoint", "early_stopping", "threshold", "test_evaluation")
	if object == nil {
		return
	}

	config.Checkpoint = d.checkpoint(path, object)
	config.EarlyStopping = d.earlyStopping(path, object)
	config.Threshold = d.threshold(path, object)
	config.TestEvaluation = d.Enum(path, object, "test_evaluation", TestSkip, TestAfterSelection)

	if config.EarlyStopping.Policy == EarlyStoppingPatience && config.Checkpoint.Policy == CheckpointFinalEpoch {
		d.Add(path+"/early_stopping/policy", CodeIncompatibleOptions,
			"early stopping counts validation evaluations, so it needs the best_validation checkpoint")
	}
}

// policyObject reads an object whose members depend on its policy: it
// returns the policy, or "" when the object or its policy is invalid, in
// which case its other members are not checked.
func (d *decoder) policyObject(path string, parent *strictjson.Value, name string,
	members map[string][]string) (*strictjson.Value, string, string) {
	object, objectPath, state := d.Member(path, parent, name, false)
	if state != strictjson.Valid {
		return nil, objectPath, ""
	}

	if object.Kind != strictjson.KindObject {
		d.Add(objectPath, CodeWrongType, "%s must be an object", name)
		return nil, objectPath, ""
	}

	policies := make([]string, 0, len(members))
	for policy := range members {
		policies = append(policies, policy)
	}
	slices.Sort(policies)

	policy := d.Enum(objectPath, object, "policy", policies...)
	if policy != "" {
		d.Object(objectPath, object, append([]string{"policy"}, members[policy]...)...)
	}

	return object, objectPath, policy
}

func (d *decoder) checkpoint(path string, parent *strictjson.Value) Checkpoint {
	object, objectPath, policy := d.policyObject(path, parent, "checkpoint", map[string][]string{
		CheckpointFinalEpoch:     nil,
		CheckpointBestValidation: {"objective", "evaluation_interval", "min_improvement"},
	})

	checkpoint := Checkpoint{Policy: policy}
	if policy != CheckpointBestValidation {
		return checkpoint
	}

	checkpoint.Objective = d.Enum(objectPath, object, "objective", ObjectiveValidationLoss, ObjectiveValidationPRAUC)

	interval, state := d.Integer(objectPath, object, "evaluation_interval", 1, false)
	if state == strictjson.Valid && interval > MaxEpochs {
		d.Add(objectPath+"/evaluation_interval", CodeOutOfRange, "evaluation_interval must be at most %d", MaxEpochs)
	}
	checkpoint.EvaluationInterval = int(interval)

	checkpoint.MinImprovement = d.number(objectPath, object, "min_improvement", func(value float64) bool {
		return value >= 0 && value <= MaxImprovement
	}, "a number from 0 to 1")

	return checkpoint
}

func (d *decoder) earlyStopping(path string, parent *strictjson.Value) EarlyStopping {
	object, objectPath, policy := d.policyObject(path, parent, "early_stopping", map[string][]string{
		EarlyStoppingNone:     nil,
		EarlyStoppingPatience: {"patience"},
	})

	stopping := EarlyStopping{Policy: policy}
	if policy != EarlyStoppingPatience {
		return stopping
	}

	patience, state := d.Integer(objectPath, object, "patience", 1, false)
	if state == strictjson.Valid && patience > MaxEpochs {
		d.Add(objectPath+"/patience", CodeOutOfRange, "patience must be at most %d", MaxEpochs)
	}
	stopping.Patience = int(patience)

	return stopping
}

func (d *decoder) threshold(path string, parent *strictjson.Value) Threshold {
	object, objectPath, policy := d.policyObject(path, parent, "threshold", map[string][]string{
		ThresholdFixed:                           {"value"},
		ThresholdMaxRecallAtMaxFalsePositiveRate: {"max_false_positive_rate"},
	})

	threshold := Threshold{Policy: policy}
	probability := func(value float64) bool { return value >= thresholdMinimum && value <= thresholdMaximum }

	switch policy {
	case ThresholdFixed:
		threshold.Value = d.number(objectPath, object, "value", probability, "a number from 0 to 1")
	case ThresholdMaxRecallAtMaxFalsePositiveRate:
		threshold.MaxFalsePositiveRate = d.number(objectPath, object, "max_false_positive_rate",
			probability, "a number from 0 to 1")
	}

	return threshold
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
	case len(c.HiddenLayers) > MaxHiddenLayers:
		return fmt.Errorf("at most %d hidden layers are supported", MaxHiddenLayers)
	case c.Seed < 0 || c.Seed > strictjson.MaxInteger:
		return fmt.Errorf("seed must be from 0 to %d", int64(strictjson.MaxInteger))
	case !(c.LearningRate > 0 && c.LearningRate <= MaxLearningRate):
		return fmt.Errorf("learning rate must be greater than 0 and at most %v", MaxLearningRate)
	case c.MaxEpochs < 1 || c.MaxEpochs > MaxEpochs:
		return fmt.Errorf("max epochs must be from 1 to %d", MaxEpochs)
	case c.TrainEnd.IsZero() || c.ValidationEnd.IsZero() || !c.TrainEnd.Before(c.ValidationEnd):
		return errors.New("train_end must be earlier than validation_end")
	}

	for _, size := range c.HiddenLayers {
		if size < 1 || size > MaxLayerNeurons {
			return fmt.Errorf("a hidden layer must have from 1 to %d neurons", MaxLayerNeurons)
		}
	}

	return c.validateSelection()
}

func (c Config) validateSelection() error {
	probability := func(value float64) bool { return value >= thresholdMinimum && value <= thresholdMaximum }

	switch c.Checkpoint.Policy {
	case CheckpointFinalEpoch:
	case CheckpointBestValidation:
		switch {
		case c.Checkpoint.Objective != ObjectiveValidationLoss && c.Checkpoint.Objective != ObjectiveValidationPRAUC:
			return fmt.Errorf("checkpoint objective %q is not supported", c.Checkpoint.Objective)
		case c.Checkpoint.EvaluationInterval < 1 || c.Checkpoint.EvaluationInterval > MaxEpochs:
			return fmt.Errorf("evaluation interval must be from 1 to %d", MaxEpochs)
		case !(c.Checkpoint.MinImprovement >= 0 && c.Checkpoint.MinImprovement <= MaxImprovement):
			return errors.New("min improvement must be a number from 0 to 1")
		}
	default:
		return fmt.Errorf("checkpoint policy %q is not supported", c.Checkpoint.Policy)
	}

	switch c.EarlyStopping.Policy {
	case EarlyStoppingNone:
	case EarlyStoppingPatience:
		switch {
		case c.EarlyStopping.Patience < 1 || c.EarlyStopping.Patience > MaxEpochs:
			return fmt.Errorf("patience must be from 1 to %d", MaxEpochs)
		case c.Checkpoint.Policy != CheckpointBestValidation:
			return errors.New("early stopping needs the best_validation checkpoint")
		}
	default:
		return fmt.Errorf("early stopping policy %q is not supported", c.EarlyStopping.Policy)
	}

	switch c.Threshold.Policy {
	case ThresholdFixed:
		if !probability(c.Threshold.Value) {
			return errors.New("threshold must be a number from 0 to 1")
		}
	case ThresholdMaxRecallAtMaxFalsePositiveRate:
		if !probability(c.Threshold.MaxFalsePositiveRate) {
			return errors.New("the maximum false positive rate must be a number from 0 to 1")
		}
	default:
		return fmt.Errorf("threshold policy %q is not supported", c.Threshold.Policy)
	}

	if c.TestEvaluation != TestSkip && c.TestEvaluation != TestAfterSelection {
		return fmt.Errorf("test evaluation %q is not supported", c.TestEvaluation)
	}

	return nil
}

// canonical is the canonical form of a configuration. Members that the
// chosen policies do not use are omitted.
type canonical struct {
	ConfigVersion string             `json:"config_version"`
	FeatureSchema string             `json:"feature_schema"`
	Architecture  canonicalArch      `json:"architecture"`
	Algorithm     string             `json:"algorithm"`
	Loss          string             `json:"loss"`
	Seed          int64              `json:"seed"`
	LearningRate  float64            `json:"learning_rate"`
	MaxEpochs     int                `json:"max_epochs"`
	Boundaries    canonicalBounds    `json:"boundaries"`
	Selection     canonicalSelection `json:"selection"`
}

type canonicalArch struct {
	HiddenLayers []int  `json:"hidden_layers"`
	Activation   string `json:"activation"`
}

type canonicalBounds struct {
	TrainEnd      string `json:"train_end"`
	ValidationEnd string `json:"validation_end"`
}

type canonicalSelection struct {
	Checkpoint struct {
		Policy             string   `json:"policy"`
		Objective          string   `json:"objective,omitempty"`
		EvaluationInterval int      `json:"evaluation_interval,omitempty"`
		MinImprovement     *float64 `json:"min_improvement,omitempty"`
	} `json:"checkpoint"`
	EarlyStopping struct {
		Policy   string `json:"policy"`
		Patience int    `json:"patience,omitempty"`
	} `json:"early_stopping"`
	Threshold struct {
		Policy               string   `json:"policy"`
		Value                *float64 `json:"value,omitempty"`
		MaxFalsePositiveRate *float64 `json:"max_false_positive_rate,omitempty"`
	} `json:"threshold"`
	TestEvaluation string `json:"test_evaluation"`
}

// Canonical returns the canonical JSON form of a valid configuration: the
// members in specification order, compact, boundaries in UTC with
// nanosecond precision, numbers in their shortest form, and only the
// members the chosen policies use. Two documents that differ only in
// formatting, member order, timestamp offsets, or number spelling have the
// same canonical form.
func (c Config) Canonical() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	form := canonical{
		ConfigVersion: Version,
		FeatureSchema: c.FeatureSchema,
		Architecture:  canonicalArch{HiddenLayers: append([]int{}, c.HiddenLayers...), Activation: c.Activation},
		Algorithm:     c.Algorithm,
		Loss:          c.Loss,
		Seed:          c.Seed,
		LearningRate:  c.LearningRate,
		MaxEpochs:     c.MaxEpochs,
		Boundaries: canonicalBounds{
			TrainEnd:      c.TrainEnd.UTC().Format(time.RFC3339Nano),
			ValidationEnd: c.ValidationEnd.UTC().Format(time.RFC3339Nano),
		},
	}

	selection := &form.Selection
	selection.Checkpoint.Policy = c.Checkpoint.Policy
	if c.Checkpoint.Policy == CheckpointBestValidation {
		minImprovement := c.Checkpoint.MinImprovement
		selection.Checkpoint.Objective = c.Checkpoint.Objective
		selection.Checkpoint.EvaluationInterval = c.Checkpoint.EvaluationInterval
		selection.Checkpoint.MinImprovement = &minImprovement
	}

	selection.EarlyStopping.Policy = c.EarlyStopping.Policy
	selection.EarlyStopping.Patience = c.EarlyStopping.Patience

	selection.Threshold.Policy = c.Threshold.Policy
	switch c.Threshold.Policy {
	case ThresholdFixed:
		value := c.Threshold.Value
		selection.Threshold.Value = &value
	case ThresholdMaxRecallAtMaxFalsePositiveRate:
		rate := c.Threshold.MaxFalsePositiveRate
		selection.Threshold.MaxFalsePositiveRate = &rate
	}

	selection.TestEvaluation = c.TestEvaluation

	return json.Marshal(form)
}

// SHA256 returns the SHA-256, as 64 lowercase hexadecimal digits, of the
// canonical form of a valid configuration. It identifies the configuration
// in reports.
func (c Config) SHA256() (string, error) {
	data, err := c.Canonical()
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
