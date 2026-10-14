// Package scoring scores payer-side Pix payments with a trained
// pix-risk-model-v1 artifact. A Scorer loads and validates one explicit
// artifact once and then scores any number of pix-payer-input-v2 documents:
// it validates each document, prepares its pix-risk-v1 features with the
// artifact's frozen preparation state, and runs the network. It never
// trains, refits, or looks anything up, and it never reads the system
// clock (ADR 0018).
//
// A score ranks payments by risk. It is not a calibrated probability and
// not a decision: the threshold the model was selected with is reported as
// metadata, and approving or rejecting a payment stays with the integrator
// (ADR 0002).
package scoring

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/pixrisk"
)

// Statuses of a scoring result.
const (
	StatusScored            = "scored"
	StatusInvalidInput      = "invalid_input"
	StatusInsufficientInput = "insufficient_input"
)

// Model identifies the loaded artifact by the SHA-256 of its exact bytes.
// Threshold is the threshold the model was selected with: metadata for the
// integrator's decision, not a decision.
type Model struct {
	ArtifactVersion string                  `json:"artifact_version"`
	ArtifactSHA256  string                  `json:"artifact_sha256"`
	FeatureSchema   string                  `json:"feature_schema"`
	Threshold       modelartifact.Threshold `json:"threshold"`
}

// Result is the outcome of scoring one document. Every result identifies
// the model. A scored result has the contract version, the score, and the
// preparation notes of the optional observations that were not available.
// An invalid input has the issues that break the contract and no contract
// version. An insufficient input is valid, has the contract version, and
// has the blockers that keep pix-risk-v1 from preparing it. Score is nil
// unless the document was scored.
type Result struct {
	Status          string
	ContractVersion string
	Model           Model
	Score           *float64
	Notes           []pixrisk.Note
	Issues          []pixinput.Issue
	Blockers        []pixrisk.Blocker
}

// Scorer scores documents with one loaded artifact. It never changes after
// loading and does not expose the artifact's parameters or preparation
// state, so one Scorer is safe for concurrent use by any number of
// goroutines.
//
// Scoring one document is bounded CPU work without I/O, so it takes no
// context: cancel between calls.
type Scorer struct {
	artifact modelartifact.Artifact
	model    Model
}

// New loads an artifact from its exact bytes. It decodes and validates the
// artifact as strictly as modelartifact.Decode does, and keeps no reference
// to data.
func New(data []byte) (*Scorer, error) {
	artifact, err := modelartifact.Decode(data)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(data)

	return &Scorer{
		artifact: artifact,
		model: Model{
			ArtifactVersion: modelartifact.Version,
			ArtifactSHA256:  hex.EncodeToString(sum[:]),
			FeatureSchema:   pixrisk.Schema,
			Threshold:       artifact.Threshold,
		},
	}, nil
}

// Load reads an artifact file once and loads it with New.
func Load(path string) (*Scorer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read model artifact: %w", err)
	}

	return New(data)
}

// Model returns the identity of the loaded artifact.
func (s *Scorer) Model() Model {
	return s.model
}

// Score validates a pix-payer-input-v2 document, prepares its pix-risk-v1
// features, and scores them. An invalid or insufficient document is a
// result, not an error; an error means the scorer itself failed.
func (s *Scorer) Score(document []byte) (Result, error) {
	result := Result{Model: s.model}

	input, err := pixinput.Decode(document, pixinput.ContractV2)

	var invalid *pixinput.ValidationError
	switch {
	case errors.As(err, &invalid):
		result.Status = StatusInvalidInput
		result.Issues = invalid.Issues
		return result, nil
	case err != nil:
		return Result{}, err
	}

	result.ContractVersion = input.ContractVersion

	prepared, err := pixrisk.Prepare(input)

	var insufficient *pixrisk.InsufficientInputError
	switch {
	case errors.As(err, &insufficient):
		result.Status = StatusInsufficientInput
		result.Blockers = insufficient.Blockers
		return result, nil
	case err != nil:
		return Result{}, err
	}

	values, err := s.artifact.State.Apply(prepared)
	if err != nil {
		return Result{}, fmt.Errorf("apply the preparation state: %w", err)
	}

	outputs, err := s.artifact.Network.Predict(values)
	if err != nil {
		return Result{}, fmt.Errorf("score: %w", err)
	}

	score := outputs[0]
	result.Status = StatusScored
	result.Score = &score
	result.Notes = prepared.Notes

	return result, nil
}
