package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/digocelos/transaction-risk-go/trainingrun"
)

// Exit codes.
const (
	exitPromoted = 0
	exitFailure  = 1
)

type promotedOutput struct {
	Status      string `json:"status"`
	ModelSHA256 string `json:"model_sha256"`
}

const usage = `usage: promote -run DIR -output FILE [-replace]

Verifies a training run published by train, including every hash in its
manifest and the bindings between its model, report, and configuration,
then writes its pix-risk-model-v1 artifact to FILE atomically. An existing
FILE is replaced only with -replace. Hashes detect mismatches; they are not
signatures.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run promotes the model of a verified run. The result goes to stdout as
// one JSON object; failures go to stderr and nothing is written.
func run(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "promote: "+format+"\n", a...)
		return exitFailure
	}

	var runDir, output string
	var replace bool

	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage+"\n")
		fs.PrintDefaults()
	}
	fs.StringVar(&runDir, "run", "", "run `directory` published by train")
	fs.StringVar(&output, "output", "", "`file` to write the promoted model artifact to")
	fs.BoolVar(&replace, "replace", false, "replace an existing output file")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitPromoted
		}
		return fail("%v", err)
	}

	var missing []string
	if runDir == "" {
		missing = append(missing, "-run")
	}
	if output == "" {
		missing = append(missing, "-output")
	}
	switch {
	case fs.NArg() > 0:
		return fail("unexpected argument %q", fs.Arg(0))
	case len(missing) > 0:
		return fail("missing required flags: %s", strings.Join(missing, ", "))
	}

	sum, err := trainingrun.Promote(runDir, output, replace)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stderr, "promote: promoted the model of %s to %s\n", runDir, output)

	data, err := json.Marshal(promotedOutput{Status: "promoted", ModelSHA256: sum})
	if err != nil {
		return fail("encode result: %v", err)
	}
	if _, err := stdout.Write(append(data, '\n')); err != nil {
		return fail("write result: %v", err)
	}

	return exitPromoted
}
