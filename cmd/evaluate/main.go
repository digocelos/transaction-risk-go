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
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/internal/strictjson"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/modelevaluation"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// Exit codes. A dataset with invalid records always uses 2, and a report
// that is written but incomplete always uses 3.
const (
	exitEvaluated      = 0
	exitFailure        = 1
	exitInvalidDataset = 2
	exitIncomplete     = 3
)

type evaluatedOutput struct {
	Status        string   `json:"status"`
	ReportSHA256  string   `json:"report_sha256"`
	ModelSHA256   string   `json:"model_sha256"`
	DatasetSHA256 string   `json:"dataset_sha256"`
	Incomplete    []string `json:"incomplete"`
}

type invalidOutput struct {
	Status  string `json:"status"`
	Dataset any    `json:"dataset"`
}

const usage = `usage: evaluate -model FILE -dataset FILE -output FILE
                (-partition validation|test | -after TIME [-through TIME])
                [-threshold VALUE] [-reference-dataset FILE] [-replace]
                [-max-records N]

Evaluates a pix-risk-model-v1 artifact on one scope of a
pix-training-example-v2 dataset and writes a pix-evaluation-report-v1
report. The scope is a partition of the model's boundaries, or the decision
times after -after, through -through when set; it never starts before the
model's train_end. Evaluation never trains, refits, selects, or changes the
model. -threshold measures at another threshold for this evaluation only,
and the report labels it. -reference-dataset is the model's training
dataset, used to measure group overlap when the evaluated dataset is
another one. An existing output is replaced only with -replace.
-max-records refuses datasets with more records; see
docs/operations/operating-limits.md before raising it.

Software cannot stop anyone from evaluating held-out data repeatedly. A
model changed after seeing its test results needs new held-out data.

Exit codes: 0 complete evaluation, 1 failure, 2 invalid dataset, 3 report
written but incomplete.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// options are the parsed command-line flags.
type options struct {
	model, dataset, reference, output string
	scope                             modelevaluation.Scope
	threshold                         *float64
	replace                           bool
	maxRecords                        int
}

// run evaluates a model from the flags in args. The result goes to stdout
// as one JSON object; progress and failures go to stderr. A failure writes
// no report.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "evaluate: "+format+"\n", a...)
		return exitFailure
	}

	opts, err := parse(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitEvaluated
	case err != nil:
		return fail("%v", err)
	}

	model, err := modelartifact.ReadFile(opts.model)
	if err != nil {
		return fail("%v", err)
	}

	dataset, err := os.Open(opts.dataset)
	if err != nil {
		return fail("%v", err)
	}
	defer dataset.Close()

	evaluationOptions := modelevaluation.Options{Scope: opts.scope, Threshold: opts.threshold, MaxRecords: opts.maxRecords}
	if opts.reference != "" {
		reference, err := os.Open(opts.reference)
		if err != nil {
			return fail("%v", err)
		}
		defer reference.Close()
		evaluationOptions.Reference = reference
	}

	report, err := modelevaluation.Evaluate(ctx, model, dataset, evaluationOptions)

	var invalid *training.InvalidDatasetError
	switch {
	case errors.As(err, &invalid):
		for _, d := range invalid.Diagnostics {
			location := fmt.Sprintf("line %d", d.Line)
			if d.Path != "" {
				location += ": " + d.Path
			}
			fmt.Fprintf(stderr, "evaluate: %s: %s: %s\n", location, d.Code, d.Message)
		}
		fmt.Fprintf(stderr, "evaluate: %v; nothing was written\n", invalid)
		return write(stdout, stderr, exitInvalidDataset, invalidOutput{Status: "invalid_dataset", Dataset: invalid.Report})
	case errors.Is(err, context.Canceled):
		return fail("canceled; nothing was written")
	case err != nil:
		return fail("%v", err)
	}

	data, err := modelevaluation.Marshal(report)
	if err != nil {
		return fail("%v", err)
	}

	// A late cancellation still writes nothing.
	if err := ctx.Err(); err != nil {
		return fail("canceled; nothing was written")
	}

	logReport(stderr, report)

	if err := writeReport(opts.output, data, opts.replace); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stderr, "evaluate: wrote %s\n", opts.output)

	result := evaluatedOutput{
		Status:        "evaluated",
		ReportSHA256:  sha256Hex(data),
		ModelSHA256:   report.Model.ArtifactSHA256,
		DatasetSHA256: report.Dataset.DatasetSHA256,
		Incomplete:    []string{},
	}
	for _, incomplete := range report.Completeness.Incomplete {
		result.Incomplete = append(result.Incomplete, incomplete.Reason)
	}

	if !report.Completeness.Complete {
		result.Status = "incomplete"
		return write(stdout, stderr, exitIncomplete, result)
	}
	return write(stdout, stderr, exitEvaluated, result)
}

// parse reads and checks the flags.
func parse(args []string, stderr io.Writer) (options, error) {
	var opts options
	var partition, after, through string
	var threshold float64

	fs := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage+"\n")
		fs.PrintDefaults()
	}

	fs.StringVar(&opts.model, "model", "", "pix-risk-model-v1 artifact `file`")
	fs.StringVar(&opts.dataset, "dataset", "", "pix-training-example-v2 dataset `file` (JSON Lines)")
	fs.StringVar(&opts.output, "output", "", "`file` to write the evaluation report to")
	fs.StringVar(&partition, "partition", "", "partition of the model's boundaries to evaluate: validation or test")
	fs.StringVar(&after, "after", "", "evaluate decision times after this RFC 3339 `time`, exclusive")
	fs.StringVar(&through, "through", "", "evaluate decision times through this RFC 3339 `time`, inclusive")
	fs.Float64Var(&threshold, "threshold", 0, "threshold `value` to measure at instead of the model's")
	fs.StringVar(&opts.reference, "reference-dataset", "", "the model's training dataset `file`, for group overlap")
	fs.BoolVar(&opts.replace, "replace", false, "replace an existing output file")
	fs.IntVar(&opts.maxRecords, "max-records", trainingdata.DefaultMaxRecords, "refuse datasets with more `records`")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	if fs.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var missing []string
	for _, name := range []string{"model", "dataset", "output"} {
		if !set[name] {
			missing = append(missing, "-"+name)
		}
	}

	switch {
	case len(missing) > 0:
		return options{}, fmt.Errorf("missing required flags: %s", strings.Join(missing, ", "))
	case set["partition"] && set["after"]:
		return options{}, errors.New("set either -partition or -after, not both")
	case set["through"] && !set["after"]:
		return options{}, errors.New("-through needs -after")
	case !set["partition"] && !set["after"]:
		return options{}, errors.New("set -partition or -after to choose the records to evaluate")
	}

	if set["threshold"] {
		opts.threshold = &threshold
	}

	if set["partition"] {
		opts.scope = modelevaluation.PartitionScope(evaluation.Partition(partition))
	} else {
		start, err := timestamp("-after", after)
		if err != nil {
			return options{}, err
		}

		var end *time.Time
		if set["through"] {
			value, err := timestamp("-through", through)
			if err != nil {
				return options{}, err
			}
			end = &value
		}

		opts.scope = modelevaluation.WindowScope(start, end)
	}

	if err := checkOutput(opts); err != nil {
		return options{}, err
	}

	return opts, nil
}

func timestamp(name, text string) (time.Time, error) {
	value, ok := strictjson.ParseTimestamp(text)
	if !ok {
		return time.Time{}, fmt.Errorf("%s must be an RFC 3339 timestamp with an offset, got %q", name, text)
	}
	return value, nil
}

// checkOutput refuses an output that exists without -replace, that is one
// of the inputs, or whose directory does not exist.
func checkOutput(opts options) error {
	if info, err := os.Stat(filepath.Dir(opts.output)); err != nil || !info.IsDir() {
		return fmt.Errorf("the -output directory %s does not exist", filepath.Dir(opts.output))
	}

	output, err := os.Stat(opts.output)
	if err != nil {
		return nil
	}

	inputs := []struct{ name, path string }{
		{"model", opts.model},
		{"dataset", opts.dataset},
		{"reference dataset", opts.reference},
	}
	for _, input := range inputs {
		if input.path == "" {
			continue
		}
		if info, err := os.Stat(input.path); err == nil && os.SameFile(info, output) {
			return fmt.Errorf("-output must not be the %s", input.name)
		}
	}

	if !opts.replace {
		return fmt.Errorf("-output %s already exists; evaluate with -replace to overwrite it", opts.output)
	}

	return nil
}

// writeReport writes data to a temporary file next to path, syncs it, and
// renames it into place, so path never holds a partial report.
func writeReport(path string, data []byte, replace bool) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".evaluate-*")
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

func logReport(stderr io.Writer, report modelevaluation.Report) {
	scope := report.Scope
	end := "with no end"
	if scope.Through != nil {
		end = "through " + scope.Through.UTC().Format(time.RFC3339Nano)
	}
	name := "window"
	if scope.Partition != nil {
		name = string(*scope.Partition) + " partition"
	}
	fmt.Fprintf(stderr, "evaluate: scope: %s, decision times after %s %s\n",
		name, scope.After.UTC().Format(time.RFC3339Nano), end)

	in := report.Records.InScope
	fmt.Fprintf(stderr, "evaluate: in scope: %d fraud and %d legitimate usable records, %d insufficient, %d immature; %d records out of scope\n",
		in.Fraud, in.Legitimate, in.Insufficient, in.Immature, report.Records.OutOfScope)

	if report.Threshold.Source == modelevaluation.ThresholdOverride {
		fmt.Fprintf(stderr, "evaluate: threshold override %v (the model's threshold is %v and is unchanged)\n",
			report.Threshold.Value, report.Model.Threshold.Value)
	} else {
		fmt.Fprintf(stderr, "evaluate: threshold %v (%s, from the model)\n",
			report.Threshold.Value, report.Model.Threshold.Policy)
	}

	if scope.Through == nil || scope.Through.After(report.Model.ValidationEnd) {
		fmt.Fprintln(stderr, "evaluate: the scope includes decision times after the model's validation_end; "+
			"software cannot stop repeated looks at held-out data, so a model changed after these results needs new held-out data")
	}

	for _, incomplete := range report.Completeness.Incomplete {
		fmt.Fprintf(stderr, "evaluate: incomplete: %s (%s)\n", incomplete.Reason, strings.Join(incomplete.Metrics, ", "))
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// write prints one compact JSON object followed by a newline.
func write(stdout, stderr io.Writer, code int, value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintf(stderr, "evaluate: encode result: %v\n", err)
		return exitFailure
	}

	if _, err := stdout.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(stderr, "evaluate: write result: %v\n", err)
		return exitFailure
	}

	return code
}
