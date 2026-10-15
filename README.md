# transaction-risk-go

A Go toolkit for payer-side Pix fraud scoring: it validates the information
an integrator supplies about a Pix payment, prepares model features, trains
and selects a neural network on historical outcomes, packages it as a
verified artifact, evaluates it, and scores new payments with it. It never
fetches data, enforces regulation, or decides whether a payment goes
through: scores inform the integrator's own rules. Licensed under
Apache-2.0, with the Go standard library only.

## Software readiness is not model readiness

The toolkit is verified as software: strict contracts, deterministic
training and scoring, leakage controls, verified artifacts, resource limits,
and an end-to-end test from a clean checkout. That says nothing about
whether a model detects fraud. The datasets in this repository are
synthetic and only exercise the software. A model is ready only after you
train it on a large, representative dataset of mature labels, evaluate it
on held-out data, and accept its precision, recall, false positives, and
missed losses against your own risk policy; the toolkit gives you the
measurements, not the verdict.

## Requirements

- Go 1.27 or later
- A 64-bit platform: linux or darwin on amd64 or arm64. The toolkit does not
  build where `int` has 32 bits.
- No external dependencies (standard library only)

See [operating limits](docs/operations/operating-limits.md) for the
supported dataset sizes, memory, training time, and file sizes, measured on
reference hardware. Run all commands from the repository root. Every
command takes its model and data paths explicitly.

## Quick start

From a clean checkout, with a synthetic dataset and a temporary directory;
`internal/e2e` runs the same steps in CI:

```bash
work=$(mktemp -d)
go run ./cmd/synthesize -records 3000 -seed 1 > "$work/data.jsonl"
go run ./cmd/train -config docs/operations/benchmark-config.json \
  -dataset "$work/data.jsonl" -run-dir "$work/run"
go run ./cmd/promote -run "$work/run" -output "$work/model.json"
go run ./cmd/evaluate -model "$work/model.json" -dataset "$work/data.jsonl" \
  -partition test -output "$work/evaluation.json"
go run ./cmd/predict -model "$work/model.json" docs/contracts/examples-v2/01-complete.json
```

`evaluate` exits with 3 when the evaluation is incomplete, for example when
a test fraud has an unknown loss; the report is still written.

## Workflow

The toolkit follows one path, configure → train and select → package →
evaluate → load and score, and each step reads and writes versioned files:

| Step | Command | Reads | Writes |
|---|---|---|---|
| Configure | | | a [`pix-training-config-v2`](docs/contracts/pix-training-config-v2.md) file |
| Train and select | `train` | the configuration and a [`pix-training-example-v2`](docs/contracts/pix-training-example-v2.md) dataset | a [run](docs/contracts/pix-risk-model-v1.md#3-training-run) with the [`pix-risk-model-v1`](docs/contracts/pix-risk-model-v1.md) artifact and the [`pix-training-report-v2`](docs/contracts/pix-training-report-v2.md) report |
| Package | `promote` | a run | the verified artifact, where your scorer reads it |
| Evaluate | `evaluate` | an artifact and a dataset | a [`pix-evaluation-report-v1`](docs/contracts/pix-evaluation-report-v1.md) report |
| Load and score | `predict`, `scoring` | an artifact and a [`pix-payer-input-v2`](docs/contracts/pix-payer-input-v2.md) scoring input | a score |

### Data: synthetic datasets (`cmd/synthesize`)

`synthesize` writes a synthetic `pix-training-example-v2` dataset of any
size, to try the toolkit or measure it without real data:

```bash
go run ./cmd/synthesize -records 100000 -seed 1 > synthetic.jsonl
```

The records are valid and in decision time order, from 2025-01-01 to
2026-06-30, with labels known as of 2026-08-31. About 1% are fraud
(`-fraud-rate`), and fraud shifts the amount, the payer's history with the
recipient, the payer device, and DICT markers. The dataset has the absence
patterns of real integrations: payments without a Pix key, payer device, or
limit per period, missing DICT groups and windows, stale observations,
insufficient inputs, immature labels, and unknown losses. The same flags
always write the same bytes. The data is made up: a model trained on it
detects nothing, and its metrics mean nothing.
[`docs/operations/benchmark-config.json`](docs/operations/benchmark-config.json)
is a configuration whose boundaries fit it.

### 1–2. Configure, train, and select (`cmd/train`)

`train` trains a network from a dataset that follows the
[`pix-training-example-v2` training contract](docs/contracts/pix-training-example-v2.md):
one JSON Lines record per past Pix payment, embedding its
[`pix-payer-input-v2`](docs/contracts/pix-payer-input-v2.md) scoring input
and the outcome observed later. The records are prepared with the feature
schema [`pix-risk-v1`](docs/features/pix-risk-v1.md), and a
[`pix-training-config-v2`](docs/contracts/pix-training-config-v2.md)
configuration fixes everything else about the run.

```bash
mkdir -p runs
go run ./cmd/train \
  -config docs/contracts/training-config-examples/01-baseline.json \
  -dataset training/testdata/pix-risk-v1.jsonl \
  -run-dir runs/first
```

`-config`, `-dataset`, and `-run-dir` are required, and the parent of
`-run-dir` must exist. `-max-records` (default 1,000,000) and
`-max-training-work` (default 10^12) refuse a dataset or a run beyond the
[supported envelope](docs/operations/operating-limits.md) before anything
is written.
`training/testdata/pix-risk-v1.jsonl` holds 8 small
synthetic records for trying the command, and
[`docs/contracts/training-config-examples/`](docs/contracts/training-config-examples/)
holds configurations to start from, such as
`04-best-validation-loss.json`:

```json
{
  "config_version": "pix-training-config-v2",
  "feature_schema": "pix-risk-v1",
  "architecture": {"hidden_layers": [4], "activation": "sigmoid"},
  "algorithm": "full-batch-gradient-descent",
  "loss": "mean-squared-error",
  "seed": 42,
  "learning_rate": 1.0,
  "max_epochs": 3000,
  "boundaries": {"train_end": "2026-06-05T00:00:00Z", "validation_end": "2026-06-12T00:00:00Z"},
  "selection": {
    "checkpoint": {"policy": "best_validation", "objective": "validation_loss",
                   "evaluation_interval": 10, "min_improvement": 0.0001},
    "early_stopping": {"policy": "patience", "patience": 20},
    "threshold": {"policy": "max_recall_at_max_false_positive_rate",
                  "max_false_positive_rate": 0.5},
    "test_evaluation": "after_selection"
  }
}
```

`train`:

1. Validates the configuration before reading anything else. An invalid
   configuration is reported issue by issue, with a JSON Pointer and a
   code, and nothing is read or written.
2. Reads, validates, and classifies every record, and fingerprints the
   dataset bytes with SHA-256. If any record is invalid, it reports every
   invalid record and stops without training. Insufficient and immature
   records are excluded and counted.
3. Splits the other records by decision time only, using the configured
   boundaries, and fits the feature standardization on the usable train
   records only.
4. Checks that the validation partition supports the configured selection.
5. Trains the configured network on the usable train records only, with
   full-batch gradient descent, for at most `max_epochs` epochs. The train
   partition needs at least one usable `fraud` record and one usable
   `legitimate` record. A step that would make a parameter non-finite
   stops training, and nothing is written.
6. Keeps the configured checkpoint: the last epoch, or the epoch with the
   best validation loss or PR-AUC, optionally stopping early after a number
   of evaluations without improvement.
7. Uses the configured threshold, or chooses the one with the highest
   validation recall whose false positive rate stays within your limit. A
   score at or above it predicts fraud.
8. Measures the validation partition and, when configured, scores the test
   partition once, after everything is frozen
   ([ADR 0015](docs/adr/0015-selection-uses-validation-only-and-test-is-explicit.md)).
9. Publishes the run directory given by `-run-dir`
   ([`pix-risk-run-v1`](docs/contracts/pix-risk-model-v1.md#3-training-run)):
   - `model.json`, the model artifact
     ([`pix-risk-model-v1`](docs/contracts/pix-risk-model-v1.md)): the
     network, the feature descriptor and fitted preparation state, the
     selected epoch and threshold, and the identities of the configuration,
     dataset, report, and software build;
   - `report.json`, the training report
     ([`pix-training-report-v2`](docs/contracts/pix-training-report-v2.md)):
     the configuration and its identity, the dataset report with every
     exclusion, the partitions, the coverage of populations and
     availability states, the selection, and the metrics;
   - `config.json`, the canonical configuration;
   - `manifest.json`, which binds the three files by SHA-256.

The run is written to a staging directory, read back and verified, and
published with a single rename, so it exists completely or not at all; an
existing directory is never overwritten
([ADR 0016](docs/adr/0016-trained-models-are-published-as-verified-runs.md)).
An interrupt (SIGINT or SIGTERM) cancels the run within 1,024 records while
reading or scoring and within 1,024 samples while training, and nothing is
written. Progress, including the training loss, goes to
standard error. Standard output holds exactly one JSON object, followed by
a newline:

| Exit code | `status` | Meaning |
|-----------|----------|---------|
| 0 | `trained` | The run was published. The object holds `dataset_sha256`, the `records` counts, `model_sha256`, `report_sha256`, and `config_sha256`. |
| 2 | `invalid_dataset` | The dataset has invalid records. `dataset` holds the dataset report, whose `exclusions` list each problem by `line`, `path`, and `code`. Standard error repeats each problem with a message. Nothing is written. |
| 3 | `selection_infeasible` | No threshold keeps the validation false positive rate within the configured limit. The object holds `threshold_policy`, `max_false_positive_rate`, and `lowest_achievable_false_positive_rate`. Nothing is written. |
| 1 | none | Operational failure, such as a missing flag, an invalid configuration, an existing run directory, an unreadable dataset, a dataset or run beyond the limits, a train partition without both labels, a validation partition that cannot support the selection, a diverging run, a cancellation, or a failed write. Standard output is empty, and the message goes to standard error. |

### 3. Package and promote (`cmd/promote`)

A finished run is not an accepted model. After reviewing its report,
promote its artifact to the place your scorer reads it from:

```bash
mkdir -p models
go run ./cmd/promote -run runs/first -output models/pix-risk.json
```

`promote` verifies the run completely: every file against the manifest, the
artifact, and the bindings between the model, the report, and the
configuration. It then writes the artifact through a temporary file and a
rename, and prints `{"status":"promoted","model_sha256":"…"}`. An existing
output is replaced only with `-replace`. Any failure exits with code 1 and
writes nothing. The SHA-256 values detect changed files; they are not
signatures and do not show who produced a model.

### 4. Evaluate (`cmd/evaluate`)

`evaluate` measures a model artifact on one scope of a
`pix-training-example-v2` dataset and writes an evaluation report
([`pix-evaluation-report-v1`](docs/contracts/pix-evaluation-report-v1.md)):

```bash
mkdir -p evaluations
go run ./cmd/evaluate \
  -model models/pix-risk.json \
  -dataset training/testdata/pix-risk-v1.jsonl \
  -partition test \
  -output evaluations/test.json
```

`-model`, `-dataset`, and `-output` are required, and the directory of
`-output` must exist. `-max-records` (default 1,000,000) limits the records
read from each dataset. Choose the records with exactly one scope:

- `-partition validation` or `-partition test`, the partitions of the
  boundaries stored in the model;
- `-after TIME`, optionally with `-through TIME`, the decision times after
  `-after` and through `-through`, as RFC 3339 timestamps with an offset.
  `-after` must not be earlier than the model's `train_end`: the train
  partition is never evaluated.

`-threshold VALUE` measures at another threshold for this evaluation only;
the report labels it as an override, and the model keeps its own.
`-reference-dataset FILE` supplies the model's training dataset, verified by
its SHA-256, to measure group overlap when you evaluate another dataset.
An existing output is replaced only with `-replace`, and the output can
never be the model or a dataset.

`evaluate`:

1. Decodes the model strictly and checks the scope and the threshold before
   reading any dataset.
2. Reads, validates, and classifies every record as `train` does. If any
   record is invalid, it reports every problem and writes nothing.
3. Prepares the usable records in scope with the model's own preparation
   state and scores them with its network. Nothing is trained, refitted,
   selected, or tuned, and the model file is never changed.
4. Measures them at the threshold and writes the report through a temporary
   file and a rename: the identities of the model and everything that
   produced it, the dataset report, the scope, the records in and out of
   it, their coverage, the group overlap with the train records, the
   threshold and its source, the metrics, and their completeness.

Evaluated on the validation and test partitions of its own training
dataset, a model reproduces the metrics of its training report exactly.

An evaluation is incomplete when its scope has no usable records, no fraud,
no legitimate payments, or frauds with an unknown loss. The report then
lists each reason with the metrics it affects. In the bundled 8-record
dataset, the only usable test record is a fraud with an unknown loss, so
the command above writes an incomplete report with `no_legitimate_examples`
and `unknown_loss_frauds`. Standard output holds exactly one JSON object,
followed by a newline:

| Exit code | `status` | Meaning |
|-----------|----------|---------|
| 0 | `evaluated` | The report was written and is complete. The object holds `report_sha256`, `model_sha256`, `dataset_sha256`, and an empty `incomplete` list. |
| 3 | `incomplete` | The report was written, but `incomplete` lists the reasons some metrics do not measure anything. |
| 2 | `invalid_dataset` | The dataset has invalid records. `dataset` holds the dataset report, and standard error repeats each problem. Nothing is written. |
| 1 | none | Operational failure, such as a missing flag, an invalid model, scope, or threshold, a reference dataset that is not the training dataset, a dataset beyond the record limit, an existing output, a cancellation, or a failed write. Standard output is empty, and the message goes to standard error. |

A complete evaluation is not a quality pass. Software also cannot stop
anyone from evaluating the test partition, or any held-out data, again and
again: if you change a model after seeing its results there, that data has
become validation data, and the changed model needs new held-out data
([ADR 0017](docs/adr/0017-saved-models-are-evaluated-read-only-on-an-explicit-scope.md)).

### 5. Load and score (`cmd/predict`, `scoring`)

`predict` scores one scoring input: a JSON document that follows the
[`pix-payer-input-v2` input contract](docs/contracts/pix-payer-input-v2.md).
It describes a Pix payment that the payer's institution is about to send,
together with observations about the payer account, the payer device, the
recipient, and the recipient's Pix key. Your system collects those
observations; the toolkit never queries DICT or any other source.

The model is always explicit: pass the artifact you promoted with `-model`.

```bash
go run ./cmd/predict -model models/pix-risk.json docs/contracts/examples-v2/04-no-pix-key.json
```

```
{"status":"scored","contract_version":"pix-payer-input-v2","model":{"artifact_version":"pix-risk-model-v1","artifact_sha256":"909932dd…","feature_schema":"pix-risk-v1","threshold":{"policy":"fixed","value":0.5}},"score":0.012040875480930998,"notes":[{"path":"/observations/recipient_account/entry","status":"missing"},{"path":"/pix_payment/recipient_key_usage","status":"not_applicable"}]}
```

Your hashes match only for the same build, and your score can differ in its
last digits on another CPU architecture. With `-` or no input argument,
`predict` reads the document from standard input, so any language can run
it as a subprocess: write the document to its standard input, read one JSON
line from its standard output, and branch on the exit code.

```bash
go install ./cmd/predict   # installs predict into $(go env GOPATH)/bin
predict -model models/pix-risk.json < docs/contracts/examples-v2/01-complete.json
```

The score ranges from 0 to 1 and ranks payments by risk. It is not a
calibrated probability, and it informs your decision without being one:
regulatory rules and operational decisions stay in your own systems. The
`threshold` in `model` is the one the model was selected with, reported as
metadata for your own rules.

`predict` loads and validates the model before reading the input, then
validates the input, prepares its `pix-risk-v1` features exactly as
training does, and scores them. Standard output always holds exactly one
JSON object, followed by a newline, and every object identifies the model:

| Exit code | `status` | Meaning |
|-----------|----------|---------|
| 0 | `scored` | The payment was scored. `notes` lists each optional observation that was not available, with its `path` and `status`: `missing`, `stale`, `not_applicable`, `no_history`, `zero_limit`, or `unrecognized`. |
| 2 | `invalid_input` | The document breaks the contract. `issues` lists each problem with a JSON Pointer `path`, a stable `code`, and a `message`. A version 1 document, or a training record with an outcome or group key, is invalid. |
| 3 | `insufficient_input` | The document is valid, but a required fact is `missing` or `stale`. `blockers` lists each `path` with its `reason`. |
| 1 | none | Operational failure, such as a missing `-model` flag, a missing or invalid model, or an unreadable file. Standard output is empty, and the message goes to standard error. |

The model never guesses missing information. A required fact, such as the
payer account's recent payment count, makes the input insufficient when it
is missing or stale. An optional observation, such as a DICT statistic,
that is not available is scored with an explicit availability indicator and
reported in `notes`, never replaced by an invented value.
[`docs/contracts/examples-v2/`](docs/contracts/examples-v2/) holds 20
scoring inputs, each with its expected preparation result.

#### From Go (`scoring`)

A Go program loads the artifact once and scores any number of documents,
from any number of goroutines:

```go
scorer, err := scoring.Load("models/pix-risk.json")
if err != nil {
	return err
}

result, err := scorer.Score(document) // a pix-payer-input-v2 document
if err != nil {
	return err // the scorer failed; invalid inputs are results
}

switch result.Status {
case scoring.StatusScored:
	use(*result.Score, result.Notes, result.Model.ArtifactSHA256)
case scoring.StatusInsufficientInput:
	report(result.Blockers)
case scoring.StatusInvalidInput:
	reject(result.Issues)
}
```

The `Scorer` never changes after loading and never trains, refits, or looks
anything up. [`scoring/example_test.go`](scoring/example_test.go) is a
complete example that runs with `go test ./scoring/`, generating its
artifact from the synthetic training fixture.

## Limitations

Successful training is not production validation. The commands show that
a dataset can be trained, evaluated, and scored reproducibly; they do not
show that the model detects fraud. The bundled datasets are synthetic, and
their metrics mean nothing. Before relying on a model, evaluate it on
a large, representative dataset of mature labels, and review its
precision, recall, false positives, and missed losses against your own risk
policy.

- Training uses the natural class distribution, with no resampling or
  reweighting. The checkpoint, early stopping, and threshold follow your
  configuration; the toolkit never chooses a risk policy for you, and a
  validation-selected threshold is only as reliable as the validation
  partition is representative.
- Training is full batch: every usable record stays in memory, and every
  epoch passes the whole train partition through the network. Memory grows
  with the records and time with the records, the parameters, and the
  epochs, so the toolkit refuses datasets and runs beyond its
  [supported envelope](docs/operations/operating-limits.md); larger data
  needs mini-batch training.
- The same dataset bytes and configuration produce the same model and
  report on the same platform. Across CPU architectures, such as arm64 and
  amd64, scores can differ in their last bits; the toolkit tests that they
  agree within 10^−9.

## Versions

Every identifier changes for its own reasons (ADR 0003), and a file never
claims more than its identifier says:

| Identifier | What it identifies |
|---|---|
| [`pix-payer-input-v2`](docs/contracts/pix-payer-input-v2.md) | The scoring input an integrator supplies. |
| [`pix-training-example-v2`](docs/contracts/pix-training-example-v2.md) | A training record: a scoring input with its outcome. |
| [`pix-risk-v1`](docs/features/pix-risk-v1.md) | The feature schema: the 62 ordered features, how they are prepared, and the layout of the fitted preparation state. |
| [`pix-training-config-v2`](docs/contracts/pix-training-config-v2.md) | A training configuration; its canonical SHA-256 identifies it. |
| [`pix-training-report-v2`](docs/contracts/pix-training-report-v2.md) | The report of a training run. |
| [`pix-risk-model-v1`](docs/contracts/pix-risk-model-v1.md) | The layout of a model artifact. |
| [`pix-risk-run-v1`](docs/contracts/pix-risk-model-v1.md#3-training-run) | The layout of a published run and its manifest. |
| [`pix-evaluation-report-v1`](docs/contracts/pix-evaluation-report-v1.md) | The report of an independent evaluation. |

The artifact version is the format of the file, not the model: two
`pix-risk-model-v1` artifacts can hold models of very different quality,
trained on different data. How features are generated is the feature
schema's, recorded inside the artifact; what produced a model is identified
by the SHA-256 of its configuration, dataset, and report, and the artifact
itself by the SHA-256 of its bytes.

## Migrating from the educational version 1

The first version of this repository was an educational workflow: a demo
that trained a four-feature network on fictional data, a committed
`model.json`, and `predict` reading it. It is removed, with no
compatibility (ADR 0013, ADR 0020):

- `cmd/demo`, the committed `model.json`, the `transaction` package, and the
  `nn` model format version 1 are gone. `predict` requires `-model` and an
  artifact from `train` and `promote`.
- `pix-payer-input-v1`, `pix-training-example-v1`, and
  `transaction-features-v1` are no longer accepted. Version 1 documents are
  invalid with `unsupported_contract_version` or
  `unsupported_training_example_version`. Move inputs to
  `pix-payer-input-v2` with its
  [migration notes](docs/contracts/pix-payer-input-v2.md#18-migration-notes),
  and retrain.
- The version 1 documents remain in the repository history, at commit
  [`6babe7c`](https://github.com/digocelos/transaction-risk-go/tree/6babe7c124a26d1c2426ef4b55b2b6c338724b5d).

## Documentation

- [`docs/contracts/`](docs/contracts/): the normative contracts and their
  examples.
- [`docs/features/pix-risk-v1.md`](docs/features/pix-risk-v1.md): the
  feature schema.
- [`docs/adr/`](docs/adr/): the architecture decisions.
- [`docs/operations/`](docs/operations/): operating limits and
  measurements.
- [`GLOSSARY.md`](GLOSSARY.md): the domain vocabulary.
