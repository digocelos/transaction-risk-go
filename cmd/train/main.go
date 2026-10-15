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
	"strings"
	"syscall"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
	"github.com/digocelos/transaction-risk-go/trainingrun"
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
	ConfigSHA256  string              `json:"config_sha256"`
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

const usage = `usage: train -config FILE -dataset FILE -run-dir DIR
             [-max-records N] [-max-training-work N]

Trains the network a pix-training-config-v2 configuration describes on the
train partition of a pix-training-example-v2 dataset, prepared with
pix-risk-v1, selects it on the validation partition, and publishes a run
directory: the pix-risk-model-v1 artifact, the training report, the
canonical configuration, and a manifest. DIR must not exist. Training
never promotes a model; see promote. -config, -dataset, and -run-dir are
required. The limits refuse a dataset or a run beyond the supported
envelope; see docs/operations/operating-limits.md before raising them.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// options are the parsed command-line flags.
type options struct {
	config, dataset, runDir string
	limits                  training.Limits
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

	result, err := training.Run(ctx, file, config, opts.limits, progress)

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
	artifact, err := modelartifact.FromTraining(result.Network, result.State, report, reportSHA256,
		modelartifact.SoftwareFromBuild())
	if err != nil {
		return fail("%v", err)
	}

	modelData, err := modelartifact.Marshal(artifact)
	if err != nil {
		return fail("%v", err)
	}

	configData, err := config.Canonical()
	if err != nil {
		return fail("%v", err)
	}

	// A late cancellation still writes nothing.
	if err := ctx.Err(); err != nil {
		return fail("canceled; nothing was written")
	}

	bundle := trainingrun.Bundle{Model: modelData, Report: reportData, Config: configData}
	if err := trainingrun.Publish(opts.runDir, bundle); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stderr, "train: published run %s\n", opts.runDir)

	return write(stdout, stderr, exitTrained, trainedOutput{
		Status:        "trained",
		DatasetSHA256: report.Dataset.DatasetSHA256,
		Records:       report.Dataset.Records,
		ModelSHA256:   sha256Hex(modelData),
		ReportSHA256:  reportSHA256,
		ConfigSHA256:  artifact.Training.ConfigSHA256,
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

// parse reads and checks the flags. Every flag is required.
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
	fs.StringVar(&opts.runDir, "run-dir", "", "run `directory` to publish; it must not exist")
	fs.IntVar(&opts.limits.MaxRecords, "max-records", training.DefaultLimits.MaxRecords,
		"refuse datasets with more `records`")
	fs.Int64Var(&opts.limits.MaxTrainingWork, "max-training-work", training.DefaultLimits.MaxTrainingWork,
		"refuse runs with more training `work`: parameters × (train records × max_epochs + validation records × evaluations)")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	if fs.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var missing []string
	for _, name := range []string{"config", "dataset", "run-dir"} {
		if !set[name] {
			missing = append(missing, "-"+name)
		}
	}
	if len(missing) > 0 {
		return options{}, fmt.Errorf("missing required flags: %s", strings.Join(missing, ", "))
	}

	if _, err := os.Lstat(opts.runDir); err == nil {
		return options{}, fmt.Errorf("-run-dir %s already exists; runs are never overwritten", opts.runDir)
	}

	return opts, nil
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
