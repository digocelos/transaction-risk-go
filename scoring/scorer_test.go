package scoring_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/scoring"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

const (
	examplesDir = "../docs/contracts/examples-v2"
	fixture     = "../training/testdata/pix-risk-v1.jsonl"
	baseline    = "../docs/contracts/training-config-examples/01-baseline.json"
)

func read(t testing.TB, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// trainArtifact trains a small model on the synthetic fixture and returns
// the bytes of its artifact and the training result.
func trainArtifact(t testing.TB) ([]byte, training.Result) {
	t.Helper()

	cfg, err := trainingconfig.Decode(read(t, baseline))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs = 300

	result, err := training.Run(context.Background(), bytes.NewReader(read(t, fixture)), cfg, training.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := modelartifact.FromTraining(result.Network, result.State, result.Report,
		digest(report), modelartifact.SoftwareFromBuild())
	if err != nil {
		t.Fatal(err)
	}
	model, err := modelartifact.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}

	return model, result
}

func newScorer(t testing.TB, model []byte) *scoring.Scorer {
	t.Helper()

	scorer, err := scoring.New(model)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return scorer
}

func score(t testing.TB, scorer *scoring.Scorer, document []byte) scoring.Result {
	t.Helper()

	result, err := scorer.Score(document)
	if err != nil {
		t.Fatalf("Score() error = %v", err)
	}
	return result
}

// examples lists the scoring inputs of the specification examples.
func examples(t testing.TB) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(examplesDir, "[0-9][0-9]-*.json"))
	if err != nil {
		t.Fatal(err)
	}

	var inputs []string
	for _, path := range paths {
		if !strings.HasSuffix(path, ".expected.json") {
			inputs = append(inputs, path)
		}
	}
	return inputs
}

// expectation is an expected result of the pix-payer-input-v2 examples,
// whose "prepared" status is "scored" here.
type expectation struct {
	Status   string            `json:"status"`
	Notes    []pixrisk.Note    `json:"notes"`
	Blockers []pixrisk.Blocker `json:"blockers"`
	Issues   []struct {
		Path string `json:"path"`
		Code string `json:"code"`
	} `json:"issues"`
}

// Every specification example is scored, invalid, or insufficient as
// specified, with the preparation notes, blockers, or issues of its
// expected result, and every result identifies the loaded artifact.
func TestScoreSpecificationExamples(t *testing.T) {
	model, _ := trainArtifact(t)
	scorer := newScorer(t, model)

	artifact, err := modelartifact.Decode(model)
	if err != nil {
		t.Fatal(err)
	}
	wantModel := scoring.Model{
		ArtifactVersion: "pix-risk-model-v1",
		ArtifactSHA256:  digest(model),
		FeatureSchema:   "pix-risk-v1",
		Threshold:       artifact.Threshold,
	}

	inputs := examples(t)
	if len(inputs) != 20 {
		t.Fatalf("found %d examples, want 20", len(inputs))
	}

	for _, input := range inputs {
		name := filepath.Base(input)
		t.Run(name, func(t *testing.T) {
			var want expectation
			if err := json.Unmarshal(read(t, strings.TrimSuffix(input, ".json")+".expected.json"), &want); err != nil {
				t.Fatal(err)
			}

			got := score(t, scorer, read(t, input))

			if got.Model != wantModel {
				t.Errorf("Model = %+v, want %+v", got.Model, wantModel)
			}

			switch want.Status {
			case "prepared":
				if got.Status != scoring.StatusScored || got.ContractVersion != pixinput.ContractV2 {
					t.Fatalf("result = %+v, want scored", got)
				}
				if got.Score == nil || math.IsNaN(*got.Score) || *got.Score < 0 || *got.Score > 1 {
					t.Errorf("Score = %v, want a number from 0 to 1", got.Score)
				}
				if !reflect.DeepEqual(got.Notes, want.Notes) {
					t.Errorf("Notes = %+v, want %+v", got.Notes, want.Notes)
				}
				if got.Blockers != nil || got.Issues != nil {
					t.Errorf("a scored result has blockers %v or issues %v", got.Blockers, got.Issues)
				}
			case "insufficient_input":
				if got.Status != scoring.StatusInsufficientInput || got.ContractVersion != pixinput.ContractV2 || got.Score != nil {
					t.Fatalf("result = %+v, want insufficient_input without a score", got)
				}
				if !reflect.DeepEqual(got.Blockers, want.Blockers) {
					t.Errorf("Blockers = %+v, want %+v", got.Blockers, want.Blockers)
				}
			case "invalid_input":
				if got.Status != scoring.StatusInvalidInput || got.ContractVersion != "" || got.Score != nil {
					t.Fatalf("result = %+v, want invalid_input without a score", got)
				}
				if len(got.Issues) != len(want.Issues) {
					t.Fatalf("Issues = %+v, want %+v", got.Issues, want.Issues)
				}
				for i, issue := range got.Issues {
					if issue.Path != want.Issues[i].Path || issue.Code != want.Issues[i].Code || issue.Message == "" {
						t.Errorf("issue %d = %+v, want %+v with a message", i, issue, want.Issues[i])
					}
				}
			default:
				t.Fatalf("unknown expected status %q", want.Status)
			}
		})
	}
}

// Scoring the embedded scoring input of a training record gives, bit for
// bit, the score the training path gives the same record: training and
// inference prepare features identically.
func TestScoreMatchesTheTrainingPath(t *testing.T) {
	model, result := trainArtifact(t)
	scorer := newScorer(t, model)

	cfg, err := trainingconfig.Decode(read(t, baseline))
	if err != nil {
		t.Fatal(err)
	}
	dataset := read(t, fixture)
	prepared, err := training.PrepareDataset(context.Background(), bytes.NewReader(dataset), cfg.Boundaries(), training.DefaultLimits.MaxRecords)
	if err != nil {
		t.Fatal(err)
	}

	lines := bytes.Split(dataset, []byte("\n"))
	examples := slices.Concat(prepared.Train, prepared.Validation, prepared.Test)
	if len(examples) != 6 {
		t.Fatalf("found %d usable records, want 6", len(examples))
	}

	for _, example := range examples {
		var record struct {
			ScoringInput json.RawMessage `json:"scoring_input"`
		}
		if err := json.Unmarshal(lines[example.Line-1], &record); err != nil {
			t.Fatal(err)
		}

		outputs, err := result.Network.Predict(example.Values)
		if err != nil {
			t.Fatal(err)
		}

		got := score(t, scorer, record.ScoringInput)
		if got.Score == nil || *got.Score != outputs[0] {
			t.Errorf("line %d: score = %v, want the training path's %v", example.Line, got.Score, outputs[0])
		}
	}
}

// Only pix-payer-input-v2 documents are scored. A version 1 document and a
// document carrying training metadata, such as an outcome or a group key,
// are invalid.
func TestScoreRejectsOtherDocuments(t *testing.T) {
	model, _ := trainArtifact(t)
	scorer := newScorer(t, model)
	complete := read(t, filepath.Join(examplesDir, "01-complete.json"))

	withMember := func(name, value string) []byte {
		t.Helper()
		trimmed := bytes.TrimRight(complete, " \n")
		trimmed = bytes.TrimRight(trimmed[:len(trimmed)-1], " \n")
		return append(slices.Clone(trimmed), []byte(",\n  \""+name+"\": "+value+"\n}\n")...)
	}

	cases := []struct {
		name     string
		document []byte
		path     string
		code     string
	}{
		{"version 1", read(t, "../docs/contracts/examples/01-complete.json"), "/contract_version", "unsupported_contract_version"},
		{"outcome", withMember("outcome", `{"label": "fraud"}`), "/outcome", "unknown_member"},
		{"group key", withMember("group_key", `"g-7c1e9a"`), "/group_key", "unknown_member"},
		{"training example", bytes.SplitN(read(t, fixture), []byte("\n"), 2)[0], "/contract_version", "required"},
		{"empty", nil, "", "invalid_json"},
		{"oversized", append([]byte("{"), bytes.Repeat([]byte(" "), pixinput.MaxDocumentSize)...), "", "document_too_large"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := score(t, scorer, c.document)

			if got.Status != scoring.StatusInvalidInput || got.Score != nil {
				t.Fatalf("result = %+v, want invalid_input", got)
			}
			found := false
			for _, issue := range got.Issues {
				found = found || (issue.Path == c.path && issue.Code == c.code)
			}
			if !found {
				t.Errorf("Issues = %+v, want %s at %q", got.Issues, c.code, c.path)
			}
		})
	}
}

// A malformed, tampered, or foreign artifact is refused when it is loaded.
func TestNewRejectsInvalidArtifacts(t *testing.T) {
	model, _ := trainArtifact(t)
	version := bytes.Replace(model, []byte(`"pix-risk-model-v1"`), []byte(`"pix-risk-model-v2"`), 1)
	if bytes.Equal(version, model) {
		t.Fatal("the artifact does not record its version")
	}
	weights := bytes.Replace(model, []byte(`"weights": [`+"\n"+`          [`+"\n"+`            `), []byte(`"weights": [`+"\n"+`          [`+"\n"+`            1`), 1)
	if bytes.Equal(weights, model) {
		t.Fatal("the artifact layout changed")
	}

	cases := map[string][]byte{
		"empty":                   nil,
		"truncated":               model[:len(model)/2],
		"changed weight":          weights,
		"another version":         version,
		"educational model.json":  read(t, "../model.json"),
		"training dataset record": bytes.SplitN(read(t, fixture), []byte("\n"), 2)[0],
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if scorer, err := scoring.New(data); err == nil || scorer != nil {
				t.Fatalf("New() = %v, %v, want an error", scorer, err)
			}
		})
	}

	if _, err := scoring.Load(filepath.Join(t.TempDir(), "absent.json")); err == nil ||
		!strings.Contains(err.Error(), "read model artifact") {
		t.Errorf("Load(absent) error = %v", err)
	}
}

// The artifact is read once: a loaded scorer keeps scoring after its file
// changes or disappears, and it keeps no reference to the bytes it was
// built from.
func TestScorerLoadsOnce(t *testing.T) {
	model, _ := trainArtifact(t)
	document := read(t, filepath.Join(examplesDir, "01-complete.json"))

	path := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(path, model, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := scoring.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := score(t, loaded, document)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := score(t, loaded, document); !reflect.DeepEqual(got, want) {
		t.Errorf("after removing the file: %+v, want %+v", got, want)
	}

	data := bytes.Clone(model)
	scorer := newScorer(t, data)
	clear(data)
	if got := score(t, scorer, document); !reflect.DeepEqual(got, want) {
		t.Errorf("after clearing the bytes: %+v, want %+v", got, want)
	}
}

// Results belong to the caller: changing one, or the Model value, changes
// nothing the scorer returns later.
func TestScorerResultsAreCopies(t *testing.T) {
	model, _ := trainArtifact(t)
	scorer := newScorer(t, model)

	scored := read(t, filepath.Join(examplesDir, "02-partial-dict.json"))
	insufficient := read(t, filepath.Join(examplesDir, "13-required-stale.json"))
	invalid := read(t, filepath.Join(examplesDir, "16-invalid-offset-unknown-local.json"))

	want := []scoring.Result{score(t, scorer, scored), score(t, scorer, insufficient), score(t, scorer, invalid)}
	wantModel := scorer.Model()

	for _, documents := range [][]byte{scored, insufficient, invalid} {
		result := score(t, scorer, documents)
		if result.Score != nil {
			*result.Score = -1
		}
		for i := range result.Notes {
			result.Notes[i].Status = "changed"
		}
		for i := range result.Blockers {
			result.Blockers[i].Reason = "changed"
		}
		for i := range result.Issues {
			result.Issues[i].Code = "changed"
		}
		result.Model.Threshold.Value = -1
	}
	identity := scorer.Model()
	identity.ArtifactSHA256 = "changed"

	got := []scoring.Result{score(t, scorer, scored), score(t, scorer, insufficient), score(t, scorer, invalid)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("results changed: %+v, want %+v", got, want)
	}
	if scorer.Model() != wantModel {
		t.Errorf("Model() = %+v, want %+v", scorer.Model(), wantModel)
	}
}

// One scorer serves many concurrent calls with the same results as
// sequential calls. Run with -race to check for data races.
func TestScorerIsSafeForConcurrentUse(t *testing.T) {
	model, _ := trainArtifact(t)
	scorer := newScorer(t, model)

	inputs := examples(t)
	documents := make([][]byte, len(inputs))
	want := make([]scoring.Result, len(inputs))
	for i, input := range inputs {
		documents[i] = read(t, input)
		want[i] = score(t, scorer, documents[i])
	}

	const goroutines, rounds = 16, 25
	var wg sync.WaitGroup
	failures := make(chan string, goroutines)

	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := range rounds {
				i := (g + round) % len(documents)
				got, err := scorer.Score(documents[i])
				if err != nil || !reflect.DeepEqual(got, want[i]) {
					failures <- fmt.Sprintf("%s: %+v, %v", inputs[i], got, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(failures)

	for failure := range failures {
		t.Error(failure)
	}

	for i, document := range documents {
		if got := score(t, scorer, document); !reflect.DeepEqual(got, want[i]) {
			t.Errorf("%s after concurrent use: %+v, want %+v", inputs[i], got, want[i])
		}
	}
}

// Scoring depends on no training, network, or process package: it cannot
// train, look anything up, or call out.
func TestScoringDependsOnNoTrainingOrNetworkPackage(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	forbidden := map[string]bool{
		"github.com/digocelos/transaction-risk-go/training":    true,
		"github.com/digocelos/transaction-risk-go/trainingrun": true,
		"net":      true,
		"net/http": true,
		"os/exec":  true,
	}

	for _, dependency := range strings.Fields(string(out)) {
		if forbidden[dependency] {
			t.Errorf("scoring depends on %s", dependency)
		}
	}
}
