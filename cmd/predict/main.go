package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/scoring"
)

// Exit codes. Problems with the scoring input always use 2 or 3.
const (
	exitScored            = 0
	exitFailure           = 1
	exitInvalidInput      = 2
	exitInsufficientInput = 3
)

type scoredOutput struct {
	Status          string         `json:"status"`
	ContractVersion string         `json:"contract_version"`
	Model           scoring.Model  `json:"model"`
	Score           float64        `json:"score"`
	Notes           []pixrisk.Note `json:"notes"`
}

type issueOutput struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type invalidOutput struct {
	Status string        `json:"status"`
	Model  scoring.Model `json:"model"`
	Issues []issueOutput `json:"issues"`
}

type insufficientOutput struct {
	Status          string            `json:"status"`
	ContractVersion string            `json:"contract_version"`
	Model           scoring.Model     `json:"model"`
	Blockers        []pixrisk.Blocker `json:"blockers"`
}

const usage = `usage: predict -model FILE [scoring-input.json | -]

Scores one pix-payer-input-v2 scoring input with the pix-risk-model-v1
artifact in FILE, usually the one promote wrote. The input is read from the
named file, or from standard input when the argument is - or absent. The
result is one JSON object on standard output, identifying the model; the
score is not a probability and not a decision.

Exit codes: 0 scored, 1 failure, 2 invalid input, 3 insufficient input.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run scores one scoring input with the model given by -model, which is
// loaded and validated before the input is read. Results go to stdout as
// one JSON object; operational failures go to stderr.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "predict: "+format+"\n", a...)
		return exitFailure
	}

	var model string

	fs := flag.NewFlagSet("predict", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage+"\n")
		fs.PrintDefaults()
	}
	fs.StringVar(&model, "model", "", "pix-risk-model-v1 artifact `file`")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitScored
		}
		return fail("%v", err)
	}

	switch {
	case model == "":
		return fail("missing required flag -model; usage: predict -model FILE [scoring-input.json | -]")
	case fs.NArg() > 1:
		return fail("unexpected argument %q", fs.Arg(1))
	}

	scorer, err := scoring.Load(model)
	if err != nil {
		return fail("%v", err)
	}

	reader := stdin
	if name := fs.Arg(0); name != "" && name != "-" {
		file, err := os.Open(name)
		if err != nil {
			return fail("%v", err)
		}
		defer file.Close()

		reader = file
	}

	// One byte more than the limit lets the decoder report an oversized
	// document as invalid input.
	document, err := io.ReadAll(io.LimitReader(reader, pixinput.MaxDocumentSize+1))
	if err != nil {
		return fail("read scoring input: %v", err)
	}

	result, err := scorer.Score(document)
	if err != nil {
		return fail("%v", err)
	}

	switch result.Status {
	case scoring.StatusInvalidInput:
		output := invalidOutput{Status: result.Status, Model: result.Model, Issues: []issueOutput{}}
		for _, issue := range result.Issues {
			output.Issues = append(output.Issues, issueOutput(issue))
		}
		return write(stdout, stderr, exitInvalidInput, output)
	case scoring.StatusInsufficientInput:
		return write(stdout, stderr, exitInsufficientInput, insufficientOutput{
			Status:          result.Status,
			ContractVersion: result.ContractVersion,
			Model:           result.Model,
			Blockers:        result.Blockers,
		})
	}

	return write(stdout, stderr, exitScored, scoredOutput{
		Status:          result.Status,
		ContractVersion: result.ContractVersion,
		Model:           result.Model,
		Score:           *result.Score,
		Notes:           result.Notes,
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
