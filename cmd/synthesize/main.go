package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/digocelos/transaction-risk-go/internal/synthetic"
)

const usage = `usage: synthesize -records N -seed S [-fraud-rate R] > dataset.jsonl

Writes a synthetic pix-training-example-v2 dataset to standard output, for
measuring and trying the toolkit at realistic sizes. Records have skewed
labels, fraud that shifts several features, and the absence patterns of
real integrations. The same flags always write the same bytes.

The data is made up: it is not real payment data, and a model trained on it
detects nothing.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run writes the dataset the flags describe to stdout. Nothing is written
// when the flags are invalid.
func run(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "synthesize: "+format+"\n", a...)
		return 1
	}

	var records int
	var seed uint64
	fraudRate := synthetic.DefaultFraudRate

	fs := flag.NewFlagSet("synthesize", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage+"\n")
		fs.PrintDefaults()
	}
	fs.IntVar(&records, "records", 0, "number of `records` to write")
	fs.Uint64Var(&seed, "seed", 0, "random `seed`")
	fs.Float64Var(&fraudRate, "fraud-rate", fraudRate, "share of fraud `records`, greater than 0 and less than 1")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return fail("%v", err)
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var missing []string
	for _, name := range []string{"records", "seed"} {
		if !set[name] {
			missing = append(missing, "-"+name)
		}
	}

	switch {
	case fs.NArg() > 0:
		return fail("unexpected argument %q", fs.Arg(0))
	case len(missing) > 0:
		return fail("missing required flags: %s", strings.Join(missing, ", "))
	}

	options := synthetic.DefaultOptions(records, seed)
	options.FraudRate = fraudRate

	// Validate before writing, so invalid flags write nothing.
	if err := options.Validate(); err != nil {
		return fail("%v", err)
	}

	fmt.Fprintf(stderr, "synthesize: writing %d synthetic records (seed %d, fraud rate %v); the data is not real\n",
		records, seed, fraudRate)

	if err := synthetic.Generate(stdout, options); err != nil {
		return fail("%v", err)
	}
	return 0
}
