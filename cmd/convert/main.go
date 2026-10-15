package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/digocelos/transaction-risk-go/trainingcsv"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// Exit codes. A CSV file with invalid rows always uses 2.
const (
	exitConverted = 0
	exitFailure   = 1
	exitInvalid   = 2
)

type convertedOutput struct {
	Status string `json:"status"`
	trainingcsv.Summary
}

type invalidOutput struct {
	Status    string                `json:"status"`
	Problems  []trainingcsv.Problem `json:"problems"`
	Truncated bool                  `json:"truncated"`
}

const usage = `usage: convert -input FILE.csv -output FILE.jsonl [-separator ,|;]
               [-replace] [-max-records N]

Converts training data in the pix-training-csv-v1 layout, such as a filled
copy of templates/training-data/pix-training-example-v2.csv, into the
pix-training-example-v2 JSON Lines dataset that train and evaluate read.
Every row is validated as train would validate it; the dataset is written,
atomically, only when every row is valid. An existing output is replaced
only with -replace. Keep files with real data in data/, which git ignores.

Exit codes: 0 converted, 1 failure, 2 invalid rows.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run converts the CSV file of the flags. The result goes to stdout as one
// JSON object; problems and failures go to stderr.
func run(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "convert: "+format+"\n", a...)
		return exitFailure
	}

	var input, output, separator string
	var replace bool
	maxRecords := trainingdata.DefaultMaxRecords

	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage+"\n")
		fs.PrintDefaults()
	}
	fs.StringVar(&input, "input", "", "CSV `file` in the pix-training-csv-v1 layout")
	fs.StringVar(&output, "output", "", "JSON Lines `file` to write")
	fs.StringVar(&separator, "separator", ",", "cell `separator`: , or ;")
	fs.BoolVar(&replace, "replace", false, "replace an existing output file")
	fs.IntVar(&maxRecords, "max-records", maxRecords, "refuse files with more `rows`")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitConverted
		}
		return fail("%v", err)
	}

	var missing []string
	if input == "" {
		missing = append(missing, "-input")
	}
	if output == "" {
		missing = append(missing, "-output")
	}
	switch {
	case fs.NArg() > 0:
		return fail("unexpected argument %q", fs.Arg(0))
	case len(missing) > 0:
		return fail("missing required flags: %s", strings.Join(missing, ", "))
	case separator != "," && separator != ";":
		return fail("-separator must be , or ;, got %q", separator)
	}

	if err := checkOutput(input, output, replace); err != nil {
		return fail("%v", err)
	}

	file, err := os.Open(input)
	if err != nil {
		return fail("%v", err)
	}
	defer file.Close()

	temporary, err := os.CreateTemp(filepath.Dir(output), "."+filepath.Base(output)+".convert-*")
	if err != nil {
		return fail("%v", err)
	}
	defer os.Remove(temporary.Name())

	summary, err := trainingcsv.Convert(file, temporary, trainingcsv.Options{
		Separator:  rune(separator[0]),
		MaxRecords: maxRecords,
	})
	closeErr := temporary.Close()

	var invalid *trainingcsv.InvalidError
	switch {
	case errors.As(err, &invalid):
		for _, p := range invalid.Problems {
			location := fmt.Sprintf("row %d", p.Row)
			if p.Column != "" {
				location += ", column " + p.Column
			}
			fmt.Fprintf(stderr, "convert: %s: %s: %s\n", location, p.Code, p.Message)
		}
		if invalid.Truncated {
			fmt.Fprintf(stderr, "convert: stopped after %d problems\n", trainingcsv.MaxProblems)
		}
		fmt.Fprintf(stderr, "convert: %v; nothing was written\n", invalid)
		return write(stdout, stderr, exitInvalid, invalidOutput{
			Status: "invalid_csv", Problems: invalid.Problems, Truncated: invalid.Truncated,
		})
	case err != nil:
		return fail("%v", err)
	case closeErr != nil:
		return fail("%v", closeErr)
	}

	if err := place(temporary.Name(), output, replace); err != nil {
		return fail("%v", err)
	}

	records := summary.Records
	fmt.Fprintf(stderr, "convert: %d rows: %d usable (%d fraud, %d legitimate), %d insufficient, %d immature; %d empty rows skipped\n",
		summary.Rows, records.Usable, summary.UsableLabels.Fraud, summary.UsableLabels.Legitimate,
		records.Insufficient, records.Immature, summary.EmptyRows)
	fmt.Fprintf(stderr, "convert: wrote %s\n", output)

	return write(stdout, stderr, exitConverted, convertedOutput{Status: "converted", Summary: summary})
}

// checkOutput refuses an output that exists without -replace, that is the
// input, or whose directory does not exist.
func checkOutput(input, output string, replace bool) error {
	if info, err := os.Stat(filepath.Dir(output)); err != nil || !info.IsDir() {
		return fmt.Errorf("the -output directory %s does not exist", filepath.Dir(output))
	}

	existing, err := os.Stat(output)
	if err != nil {
		return nil
	}
	if source, err := os.Stat(input); err == nil && os.SameFile(source, existing) {
		return errors.New("-output must not be the input")
	}
	if !replace {
		return fmt.Errorf("-output %s already exists; convert with -replace to overwrite it", output)
	}
	return nil
}

// place syncs the converted file and renames it into place, so output
// never holds a partial dataset.
func place(temporary, output string, replace bool) error {
	file, err := os.Open(temporary)
	if err != nil {
		return err
	}
	err = file.Sync()
	file.Close()
	if err != nil {
		return err
	}

	if !replace {
		if _, err := os.Lstat(output); err == nil {
			return fmt.Errorf("-output %s already exists", output)
		}
	}
	if err := os.Rename(temporary, output); err != nil {
		return err
	}

	if dir, err := os.Open(filepath.Dir(output)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}

// write prints one compact JSON object followed by a newline.
func write(stdout, stderr io.Writer, code int, value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintf(stderr, "convert: encode result: %v\n", err)
		return exitFailure
	}
	if _, err := stdout.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(stderr, "convert: write result: %v\n", err)
		return exitFailure
	}
	return code
}
