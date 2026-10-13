package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/transaction"
)

// Exit codes. Problems with the scoring input always use 2 or 3.
const (
	exitScored            = 0
	exitFailure           = 1
	exitInvalidInput      = 2
	exitInsufficientInput = 3
)

type scoredOutput struct {
	Status          string  `json:"status"`
	ContractVersion string  `json:"contract_version"`
	FeatureSchema   string  `json:"feature_schema"`
	Score           float64 `json:"score"`
}

type issueOutput struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type invalidOutput struct {
	Status string        `json:"status"`
	Issues []issueOutput `json:"issues"`
}

type blockerOutput struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type insufficientOutput struct {
	Status   string          `json:"status"`
	Blockers []blockerOutput `json:"blockers"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run scores one scoring input, read from the file named by the only
// argument, or from stdin when there is no argument or it is "-".
// Results go to stdout as one JSON object; operational failures go to
// stderr. The model is loaded only when there is something to score.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "predict: "+format+"\n", a...)
		return exitFailure
	}

	if len(args) > 1 {
		return fail("usage: predict [scoring-input.json | -]")
	}

	reader := stdin
	if len(args) == 1 && args[0] != "-" {
		file, err := os.Open(args[0])
		if err != nil {
			return fail("%v", err)
		}
		defer file.Close()

		reader = file
	}

	input, err := pixinput.Read(reader)

	var invalid *pixinput.ValidationError
	switch {
	case errors.As(err, &invalid):
		output := invalidOutput{Status: "invalid_input", Issues: []issueOutput{}}
		for _, issue := range invalid.Issues {
			output.Issues = append(output.Issues, issueOutput(issue))
		}
		return write(stdout, stderr, exitInvalidInput, output)
	case err != nil:
		return fail("%v", err)
	}

	features, err := transaction.FromScoringInput(input)

	var insufficient *transaction.InsufficientInputError
	switch {
	case errors.As(err, &insufficient):
		output := insufficientOutput{Status: "insufficient_input", Blockers: []blockerOutput{}}
		for _, blocker := range insufficient.Blockers {
			output.Blockers = append(output.Blockers, blockerOutput(blocker))
		}
		return write(stdout, stderr, exitInsufficientInput, output)
	case err != nil:
		return fail("%v", err)
	}

	model, err := nn.Load("model.json", transaction.FeatureSchema)
	if err != nil {
		return fail("%v", err)
	}

	outputs, err := model.Predict(features[:])
	if err != nil {
		return fail("%v", err)
	}

	if len(outputs) != 1 {
		return fail("transaction risk model must have exactly one output")
	}

	return write(stdout, stderr, exitScored, scoredOutput{
		Status:          "scored",
		ContractVersion: input.ContractVersion,
		FeatureSchema:   transaction.FeatureSchema,
		Score:           outputs[0],
	})
}

// write prints one compact JSON object followed by a newline. The JSON
// encoder writes the shortest decimal that round-trips each float64.
func write(stdout, stderr io.Writer, code int, value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintf(stderr, "predict: encode result: %v\n", err)
		return exitFailure
	}

	if _, err := stdout.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(stderr, "predict: write result: %v\n", err)
		return exitFailure
	}

	return code
}
