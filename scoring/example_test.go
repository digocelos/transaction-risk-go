package scoring_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/scoring"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// This example loads a model artifact once and scores a scoring input
// without a Pix key. In a real integration, the artifact is the file
// cmd/promote wrote; here it is generated from the synthetic training
// fixture, so the example runs from a clean checkout.
func Example() {
	dir, err := os.MkdirTemp("", "scoring-example-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "pix-risk.json")
	if err := generateArtifact(path); err != nil {
		log.Fatal(err)
	}

	// Load once, then score any number of documents, from any number of
	// goroutines.
	scorer, err := scoring.Load(path)
	if err != nil {
		log.Fatal(err)
	}

	document, err := os.ReadFile("../docs/contracts/examples-v2/04-no-pix-key.json")
	if err != nil {
		log.Fatal(err)
	}

	result, err := scorer.Score(document)
	if err != nil {
		log.Fatal(err)
	}

	switch result.Status {
	case scoring.StatusScored:
		fmt.Println(result.Status, result.ContractVersion, result.Model.FeatureSchema)
		fmt.Println("score from 0 to 1:", *result.Score >= 0 && *result.Score <= 1)
		for _, note := range result.Notes {
			fmt.Println("note:", note.Path, note.Status)
		}
		// The threshold is metadata for your own decision rules.
		fmt.Println("model threshold:", result.Model.Threshold.Policy, result.Model.Threshold.Value)
	case scoring.StatusInsufficientInput:
		fmt.Println("insufficient:", result.Blockers)
	case scoring.StatusInvalidInput:
		fmt.Println("invalid:", result.Issues)
	}

	// Output:
	// scored pix-payer-input-v2 pix-risk-v1
	// score from 0 to 1: true
	// note: /observations/recipient_account/entry missing
	// note: /pix_payment/recipient_key_usage not_applicable
	// model threshold: fixed 0.5
}

// generateArtifact trains the baseline configuration on the synthetic
// fixture and writes the model artifact to path, as cmd/train and
// cmd/promote would.
func generateArtifact(path string) error {
	data, err := os.ReadFile("../docs/contracts/training-config-examples/01-baseline.json")
	if err != nil {
		return err
	}
	cfg, err := trainingconfig.Decode(data)
	if err != nil {
		return err
	}
	cfg.MaxEpochs = 300

	dataset, err := os.ReadFile("../training/testdata/pix-risk-v1.jsonl")
	if err != nil {
		return err
	}

	result, err := training.Run(context.Background(), bytes.NewReader(dataset), cfg, training.DefaultLimits, nil)
	if err != nil {
		return err
	}
	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(report)

	artifact, err := modelartifact.FromTraining(result.Network, result.State, result.Report,
		hex.EncodeToString(sum[:]), modelartifact.SoftwareFromBuild())
	if err != nil {
		return err
	}
	model, err := modelartifact.Marshal(artifact)
	if err != nil {
		return err
	}

	return os.WriteFile(path, model, 0o644)
}
