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

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// Exit codes. A dataset with invalid records always uses 2, and a
// threshold policy that no candidate satisfies always uses 3.
const (
	exitTrained             = 0
	exitFailure             = 1
	exitInvalidDataset      = 2
	exitSelectionInfeasible = 3
)

type trainedOutput struct {
	Status        string              `json:"status"`
	DatasetSHA256 string              `json:"dataset_sha256"`
	Records       trainingdata.Counts `json:"records"`
	ModelSHA256   string              `json:"model_sha256"`
	ReportSHA256  string              `json:"report_sha256"`
}

type infeasibleOutput struct {
	Status                            string  `json:"status"`
	ThresholdPolicy                   string  `json:"threshold_policy"`
	MaxFalsePositiveRate              float64 `json:"max_false_positive_rate"`
	LowestAchievableFalsePositiveRate float64 `json:"lowest_achievable_false_positive_rate"`
}

type invalidOutput struct {
	Status  string              `json:"status"`
	Dataset trainingdata.Report `json:"dataset"`
}

const usage = `usage: train -config FILE -dataset FILE -model-output FILE -report-output FILE

Trains the network a pix-training-config-v2 configuration describes on the
train partition of a pix-training-example-v2 dataset, prepared with
pix-risk-v1, evaluates the validation and test partitions, and writes the
model and its training report. Every flag is required.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// options are the parsed command-line flags.
type options struct {
	config, dataset, modelOutput, reportOutput string
}

// run trains a model from the flags in args. The result goes to stdout as
// one JSON object; progress and operational failures go to stderr. When ctx
// is canceled, run stops and writes nothing.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "train: "+format+"\n", a...)
		return exitFailure
	}

	opts, err := parse(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitTrained
	case err != nil:
		return fail("%v", err)
	}

	config, err := readConfig(opts.config)

	var invalidConfig *trainingconfig.ValidationError
	switch {
	case errors.As(err, &invalidConfig):
		for _, issue := range invalidConfig.Issues {
			fmt.Fprintf(stderr, "train: %s: %s: %s: %s\n", opts.config, issue.Path, issue.Code, issue.Message)
		}
		return fail("%s is not a valid %s configuration", opts.config, trainingconfig.Version)
	case err != nil:
		return fail("%v", err)
	}

	file, err := os.Open(opts.dataset)
	if err != nil {
		return fail("%v", err)
	}
	defer file.Close()

	interval := max(1, config.MaxEpochs/10)
	progress := func(epoch int, loss float64) {
		if epoch%interval == 0 || epoch == config.MaxEpochs-1 {
			fmt.Fprintf(stderr, "train: epoch %d of %d: training loss %.6f\n", epoch, config.MaxEpochs, loss)
		}
	}

	result, err := training.Run(ctx, file, config, progress)

	var invalid *training.InvalidDatasetError
	var untrainable *training.UntrainableError
	var requirement *training.ValidationRequirementError
	var infeasible *evaluation.InfeasibleThresholdError
	switch {
	case errors.As(err, &invalid):
		for _, d := range invalid.Diagnostics {
			location := fmt.Sprintf("line %d", d.Line)
			if d.Path != "" {
				location += ": " + d.Path
			}
			fmt.Fprintf(stderr, "train: %s: %s: %s\n", location, d.Code, d.Message)
		}
		fmt.Fprintf(stderr, "train: %v\n", invalid)
		return write(stdout, stderr, exitInvalidDataset, invalidOutput{Status: "invalid_dataset", Dataset: invalid.Report})
	case errors.As(err, &untrainable):
		return fail("%v; move boundaries.train_end in the configuration or add records", untrainable)
	case errors.As(err, &requirement):
		return fail("%v; move the boundaries or change the selection policy", requirement)
	case errors.As(err, &infeasible):
		fmt.Fprintf(stderr, "train: %v; nothing was written\n", infeasible)
		return write(stdout, stderr, exitSelectionInfeasible, infeasibleOutput{
			Status:                            "selection_infeasible",
			ThresholdPolicy:                   config.Threshold.Policy,
			MaxFalsePositiveRate:              infeasible.MaxFalsePositiveRate,
			LowestAchievableFalsePositiveRate: infeasible.LowestAchievableFalsePositiveRate,
		})
	case errors.Is(err, context.Canceled):
		return fail("canceled; nothing was written")
	case err != nil:
		return fail("%v", err)
	}

	report := result.Report
	logPartitions(stderr, report.Dataset.Records, report.Partitions)
	fmt.Fprintf(stderr, "train: selected epoch %d of %d trained (%s, stopped by %s); threshold %v (%s)\n",
		report.Selection.SelectedEpoch, report.Selection.ActualEpochs, report.Selection.CheckpointPolicy,
		report.Selection.StopReason, report.Selection.Threshold.Value, report.Selection.Threshold.Policy)

	reportData, err := trainingreport.Marshal(report)
	if err != nil {
		return fail("%v", err)
	}

	reportSHA256 := sha256Hex(reportData)
	modelData, err := result.Network.MarshalWithProvenance(config.FeatureSchema,
		trainingreport.Provenance(report, reportSHA256))
	if err != nil {
		return fail("%v", err)
	}

	// A late cancellation still writes nothing.
	if err := ctx.Err(); err != nil {
		return fail("canceled; nothing was written")
	}

	// The report is written first: the model binds it by hash.
	if err := writeAtomically(
		artifact{opts.reportOutput, reportData},
		artifact{opts.modelOutput, modelData},
	); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stderr, "train: wrote %s and %s\n", opts.reportOutput, opts.modelOutput)

	return write(stdout, stderr, exitTrained, trainedOutput{
		Status:        "trained",
		DatasetSHA256: report.Dataset.DatasetSHA256,
		Records:       report.Dataset.Records,
		ModelSHA256:   sha256Hex(modelData),
		ReportSHA256:  reportSHA256,
	})
}

func readConfig(path string) (trainingconfig.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return trainingconfig.Config{}, err
	}
	defer file.Close()

	return trainingconfig.Read(file)
}

// parse reads and checks the flags. Every flag is required, and the two
// outputs must differ from each other and from both inputs.
func parse(args []string, stderr io.Writer) (options, error) {
	var opts options

	fs := flag.NewFlagSet("train", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage+"\n")
		fs.PrintDefaults()
	}

	fs.StringVar(&opts.config, "config", "", "pix-training-config-v2 configuration `file`")
	fs.StringVar(&opts.dataset, "dataset", "", "pix-training-example-v2 dataset `file` (JSON Lines)")
	fs.StringVar(&opts.modelOutput, "model-output", "", "`file` to write the trained model to")
	fs.StringVar(&opts.reportOutput, "report-output", "", "`file` to write the training report to")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	if fs.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var missing []string
	fs.VisitAll(func(f *flag.Flag) {
		if !set[f.Name] {
			missing = append(missing, "-"+f.Name)
		}
	})
	if len(missing) > 0 {
		return options{}, fmt.Errorf("missing required flags: %s", strings.Join(missing, ", "))
	}

	switch {
	case samePath(opts.modelOutput, opts.reportOutput):
		return options{}, errors.New("-model-output and -report-output must be different files")
	case samePath(opts.modelOutput, opts.dataset), samePath(opts.reportOutput, opts.dataset):
		return options{}, errors.New("an output must not overwrite the dataset")
	case samePath(opts.modelOutput, opts.config), samePath(opts.reportOutput, opts.config):
		return options{}, errors.New("an output must not overwrite the configuration")
	}

	return opts, nil
}

// samePath reports whether two paths name the same file.
func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil && absA == absB {
		return true
	}

	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}

func logPartitions(stderr io.Writer, records trainingdata.Counts, partitions evaluation.PartitionReport) {
	fmt.Fprintf(stderr, "train: %d records: %d usable, %d insufficient, %d immature\n",
		records.Total, records.Usable, records.Insufficient, records.Immature)

	for _, p := range []struct {
		name   string
		counts evaluation.PartitionCounts
	}{
		{"train", partitions.Train},
		{"validation", partitions.Validation},
		{"test", partitions.Test},
	} {
		fmt.Fprintf(stderr, "train: %s partition: %d fraud, %d legitimate usable records\n",
			p.name, p.counts.Fraud, p.counts.Legitimate)
	}
}

// artifact is a file to write.
type artifact struct {
	path string
	data []byte
}

// writeAtomically writes each artifact in full to a temporary file in its
// directory and syncs it before renaming any of them into place, in order.
// A failure leaves no partial file: each path keeps its previous content
// or receives its complete new content.
func writeAtomically(artifacts ...artifact) error {
	temporary := make([]string, 0, len(artifacts))
	defer func() {
		for _, name := range temporary {
			if name != "" {
				os.Remove(name)
			}
		}
	}()

	for _, a := range artifacts {
		name, err := writeTemporary(a)
		if err != nil {
			return err
		}
		temporary = append(temporary, name)
	}

	for i, a := range artifacts {
		if err := os.Rename(temporary[i], a.path); err != nil {
			return fmt.Errorf("write %s: %w", a.path, err)
		}
		temporary[i] = ""
	}

	return nil
}

func writeTemporary(a artifact) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(a.path), "."+filepath.Base(a.path)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("write %s: %w", a.path, err)
	}

	_, err = file.Write(a.data)
	if err == nil {
		err = file.Chmod(0o644)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		os.Remove(file.Name())
		return "", fmt.Errorf("write %s: %w", a.path, err)
	}

	return file.Name(), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// write prints one compact JSON object followed by a newline.
func write(stdout, stderr io.Writer, code int, value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintf(stderr, "train: encode result: %v\n", err)
		return exitFailure
	}

	if _, err := stdout.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(stderr, "train: write result: %v\n", err)
		return exitFailure
	}

	return code
}
