package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/digocelos/transaction-risk-go/modelinspection"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingrun"
)

// Exit codes. A dataset with invalid records always uses 2.
const (
	exitInspected      = 0
	exitFailure        = 1
	exitInvalidDataset = 2
)

type inspectedOutput struct {
	Status              string   `json:"status"`
	ReportSHA256        string   `json:"report_sha256"`
	ModelSHA256         string   `json:"model_sha256"`
	DatasetSHA256       string   `json:"dataset_sha256"`
	UnexercisedFeatures []string `json:"unexercised_features"`
	DirectionViolations int      `json:"direction_violations"`
}

type invalidOutput struct {
	Status  string `json:"status"`
	Dataset any    `json:"dataset"`
}

const usage = `usage: inspect -run DIR -dataset FILE -output FILE [-replace] [-max-records N]

Inspects the model of a training run published by train, together with
the dataset it was trained on, and writes a pix-model-inspection-v1
report: the parameters and how far they moved from their initialization,
the inputs that no train record exercised, the partial dependence of the
score on each standardized feature in real units on the validation
partition, and the validation metrics with each optional source masked.
The run is verified completely first, and the dataset must be the run's
training dataset, by SHA-256. Inspection never trains, refits, selects,
or changes the run or the dataset. An existing output is replaced only
with -replace. -max-records refuses datasets with more records; see
docs/operations/operating-limits.md before raising it.

The report is a measurement, not a verdict: read it before evaluating
held-out data, as docs/readiness.md describes.

Exit codes: 0 report written, 1 failure, 2 invalid dataset.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// options are the parsed command-line flags.
type options struct {
	runDir, dataset, output string
	replace                 bool
	maxRecords              int
}

// run inspects a run from the flags in args. The result goes to stdout as
// one JSON object; progress and failures go to stderr. A failure writes no
// report.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "inspect: "+format+"\n", a...)
		return exitFailure
	}

	opts, err := parse(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitInspected
	case err != nil:
		return fail("%v", err)
	}

	verified, err := trainingrun.Verify(opts.runDir)
	if err != nil {
		return fail("%v", err)
	}

	dataset, err := os.Open(opts.dataset)
	if err != nil {
		return fail("%v", err)
	}
	defer dataset.Close()

	report, err := modelinspection.Inspect(ctx, verified.Bundle.Model, verified.Config, dataset,
		modelinspection.Options{MaxRecords: opts.maxRecords})

	var invalid *training.InvalidDatasetError
	switch {
	case errors.As(err, &invalid):
		for _, d := range invalid.Diagnostics {
			location := fmt.Sprintf("line %d", d.Line)
			if d.Path != "" {
				location += ": " + d.Path
			}
			fmt.Fprintf(stderr, "inspect: %s: %s: %s\n", location, d.Code, d.Message)
		}
		fmt.Fprintf(stderr, "inspect: %v; nothing was written\n", invalid)
		return write(stdout, stderr, exitInvalidDataset, invalidOutput{Status: "invalid_dataset", Dataset: invalid.Report})
	case errors.Is(err, context.Canceled):
		return fail("canceled; nothing was written")
	case err != nil:
		return fail("%v", err)
	}

	data, err := modelinspection.Marshal(report)
	if err != nil {
		return fail("%v", err)
	}

	// A late cancellation still writes nothing.
	if err := ctx.Err(); err != nil {
		return fail("canceled; nothing was written")
	}

	violations := logReport(stderr, report)

	if err := writeReport(opts.output, data, opts.replace); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stderr, "inspect: wrote %s\n", opts.output)

	return write(stdout, stderr, exitInspected, inspectedOutput{
		Status:              "inspected",
		ReportSHA256:        sha256Hex(data),
		ModelSHA256:         report.Model.ArtifactSHA256,
		DatasetSHA256:       report.Dataset.DatasetSHA256,
		UnexercisedFeatures: report.UnexercisedFeatures,
		DirectionViolations: violations,
	})
}

// parse reads and checks the flags.
func parse(args []string, stderr io.Writer) (options, error) {
	var opts options

	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage+"\n")
		fs.PrintDefaults()
	}

	fs.StringVar(&opts.runDir, "run", "", "run `directory` published by train")
	fs.StringVar(&opts.dataset, "dataset", "", "the run's training dataset `file` (JSON Lines)")
	fs.StringVar(&opts.output, "output", "", "`file` to write the inspection report to")
	fs.BoolVar(&opts.replace, "replace", false, "replace an existing output file")
	fs.IntVar(&opts.maxRecords, "max-records", trainingdata.DefaultMaxRecords, "refuse datasets with more `records`")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	if fs.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	var missing []string
	for _, f := range []struct{ name, value string }{{"run", opts.runDir}, {"dataset", opts.dataset}, {"output", opts.output}} {
		if f.value == "" {
			missing = append(missing, "-"+f.name)
		}
	}
	if len(missing) > 0 {
		return options{}, fmt.Errorf("missing required flags: %s", strings.Join(missing, ", "))
	}

	if err := checkOutput(opts); err != nil {
		return options{}, err
	}

	return opts, nil
}

// checkOutput refuses an output that exists without -replace, that is the
// dataset or a file of the run, or whose directory does not exist.
func checkOutput(opts options) error {
	if info, err := os.Stat(filepath.Dir(opts.output)); err != nil || !info.IsDir() {
		return fmt.Errorf("the -output directory %s does not exist", filepath.Dir(opts.output))
	}

	output, err := os.Stat(opts.output)
	if err != nil {
		return nil
	}

	inputs := []struct{ name, path string }{{"dataset", opts.dataset}}
	for _, name := range []string{"model.json", "report.json", "config.json", "manifest.json"} {
		inputs = append(inputs, struct{ name, path string }{"run's " + name, filepath.Join(opts.runDir, name)})
	}
	for _, input := range inputs {
		if info, err := os.Stat(input.path); err == nil && os.SameFile(info, output) {
			return fmt.Errorf("-output must not be the %s", input.name)
		}
	}

	if !opts.replace {
		return fmt.Errorf("-output %s already exists; inspect with -replace to overwrite it", opts.output)
	}

	return nil
}

// writeReport writes data to a temporary file next to path, syncs it, and
// renames it into place, so path never holds a partial report.
func writeReport(path string, data []byte, replace bool) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".inspect-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)

	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("-output %s already exists", path)
		}
	}

	if err := os.Rename(temporary, path); err != nil {
		return err
	}

	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}

// logReport summarizes the report on stderr and returns the number of
// direction violations.
func logReport(stderr io.Writer, report modelinspection.Report) int {
	partitions := report.Partitions
	fmt.Fprintf(stderr, "inspect: %d train, %d validation, and %d test usable records; seed %d, sizes %v\n",
		partitions.Train.Fraud+partitions.Train.Legitimate,
		partitions.Validation.Fraud+partitions.Validation.Legitimate,
		partitions.Test.Fraud+partitions.Test.Legitimate,
		report.Parameters.Seed, report.Parameters.Sizes)

	if len(report.UnexercisedFeatures) > 0 {
		fmt.Fprintf(stderr, "inspect: no train record exercised %s; their weights are the random initialization\n",
			strings.Join(report.UnexercisedFeatures, ", "))
	}

	violations := 0
	for _, feature := range report.PartialDependence.Features {
		violations += len(feature.Violations)
		inside := 0
		for _, violation := range feature.Violations {
			if violation.InTrainRange {
				inside++
			}
		}
		if len(feature.Violations) > 0 {
			fmt.Fprintf(stderr, "inspect: %s moves against %s %d times, %d inside the train range [%v, %v] %s\n",
				feature.Name, feature.ExpectedDirection, len(feature.Violations), inside,
				feature.TrainRange.Min, feature.TrainRange.Max, feature.Unit)
		}
	}

	for _, probe := range report.Missingness.Probes {
		if probe.FraudsEvading > 0 || probe.LegitimateNewlyFlagged > 0 {
			fmt.Fprintf(stderr, "inspect: masking %s: %d frauds fall below the threshold, %d legitimate records rise above it\n",
				probe.Name, probe.FraudsEvading, probe.LegitimateNewlyFlagged)
		}
	}

	return violations
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// write prints one compact JSON object followed by a newline.
func write(stdout, stderr io.Writer, code int, value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintf(stderr, "inspect: encode result: %v\n", err)
		return exitFailure
	}

	if _, err := stdout.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(stderr, "inspect: write result: %v\n", err)
		return exitFailure
	}

	return code
}
