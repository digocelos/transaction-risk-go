# transaction-risk-go
An open-source Go toolkit for learning and building financial transaction fraud detection models, starting with Pix.

## Requirements

- Go 1.27 or later
- No external dependencies (standard library only)

Run all commands from the repository root. `cmd/demo` and `cmd/predict` read
and write `model.json` relative to the current directory.

## Training the model (`cmd/demo`)

```bash
go run ./cmd/demo
```

The demo:

1. Builds 8 fictional labeled transactions (4 legitimate, 4 fraudulent) and
   converts each one into a feature vector with `transaction.Features`.
2. Creates a 4-4-1 network (4 inputs, 4 hidden neurons, 1 output) with
   seed `42`, so every run starts from the same weights.
3. Prints the scores of the untrained network.
4. Trains for 5000 epochs with learning rate `1.0`, printing the loss
   every 500 epochs.
5. Prints the scores after training, then evaluates 4 examples that were
   never used in training. A score `>= 0.5` is predicted as fraud (`1`).
6. Saves the trained network to `model.json`, overwriting the existing file.

Expected output (abridged):

```
Epoch    0 | Loss: 0.279745
Epoch  500 | Loss: 0.005225
...
Epoch 4500 | Loss: 0.000217

Evaluation on unseen examples:
Example 1 | Score: 0.0009 | Prediction: 0 | Target: 0 | Correct: true
Example 2 | Score: 0.1873 | Prediction: 0 | Target: 0 | Correct: true
Example 3 | Score: 0.9962 | Prediction: 1 | Target: 1 | Correct: true
Example 4 | Score: 0.9907 | Prediction: 1 | Target: 1 | Correct: true

Model saved to model.json
```

To experiment, edit `transactions`, `targets`, the hidden layer size, the
number of epochs or the learning rate in `cmd/demo/main.go`, then run the
demo again.

## Scoring a Pix payment (`cmd/predict`)

`predict` reads one scoring input: a JSON document that follows the
[`pix-payer-input-v1` input contract](docs/contracts/pix-payer-input-v1.md).
It describes a Pix payment that the payer's institution is about to send,
together with observations about the payer account, the payer device, the
recipient, and the recipient's Pix key. Your system collects those
observations; the toolkit never queries DICT or any other source.

```bash
go run ./cmd/predict docs/contracts/examples/01-complete.json
```

```
{"status":"scored","contract_version":"pix-payer-input-v1","feature_schema":"transaction-features-v1","score":0.18734535214416673}
```

Pass `-`, or no argument, to read the scoring input from standard input:

```bash
go run ./cmd/predict < docs/contracts/examples/08-no-pix-key.json
```

The score ranges from 0 (legitimate) to 1 (fraud). It informs your
decision but is not one: regulatory rules and operational decisions stay in
your own systems.

`predict` validates the input, converts it to the features of
`transaction-features-v1`, and scores them with `model.json`, which must
have been trained with the same feature schema. The repository already
includes a trained `model.json`, so you can run `predict` without training
first. Standard output always holds exactly one JSON object, followed by a
newline:

| Exit code | `status` | Meaning |
|-----------|----------|---------|
| 0 | `scored` | The payment was scored. |
| 2 | `invalid_input` | The document breaks the contract. `issues` lists each problem with a JSON Pointer `path`, a stable `code`, and a `message`. |
| 3 | `insufficient_input` | The document is valid, but the model cannot score it. `blockers` lists each `path` with a `reason`: `missing`, `stale`, `no_history`, or `not_applicable`. |
| 1 | none | Operational failure, such as an unreadable file or a missing model. Standard output is empty, and the message goes to standard error. |

The model never guesses missing information. For example, a payer account
with no settled Pix payments in the last 90 days has no typical amount, so
the input is insufficient rather than scored with an invented value.

[`docs/contracts/examples/`](docs/contracts/examples/) holds 17 scoring
inputs, each with its expected result: start from `01-complete.json` and
change one observation at a time. The model uses four features:

| Feature | Observation | Value sent to the network |
|---------|-------------|---------------------------|
| Amount ratio | `pix_payment.amount_cents` and `payer_account.settled_outgoing_pix_90d.median_amount_cents` | amount / median amount / 10 |
| Recent count | `payer_account.initiated_outgoing_pix_24h.count` | count / 20 |
| `NewRecipient` | `recipient_person.settled_pix_from_payer_account_365d.count` | `1` if the count is 0, otherwise `0` |
| `NewDevice` | `payer_device.settled_outgoing_pix_90d.count` | `1` if the count is 0, otherwise `0` |

If `model.json` is missing, run the demo first. If you change the feature
order or scaling in `transaction/features.go`, update `FeatureSchema` and
retrain. `predict` rejects a model trained with a different schema.

## Training from a dataset (`cmd/train`)

`train` trains a network from a dataset that follows the
[`pix-training-example-v2` training contract](docs/contracts/pix-training-example-v2.md):
one JSON Lines record per past Pix payment, embedding its
[`pix-payer-input-v2`](docs/contracts/pix-payer-input-v2.md) scoring input
and the outcome observed later. The records are prepared with the feature
schema [`pix-risk-v1`](docs/features/pix-risk-v1.md), and a
[`pix-training-config-v2`](docs/contracts/pix-training-config-v2.md)
configuration fixes everything else about the run.

```bash
go run ./cmd/train \
  -config docs/contracts/training-config-examples/01-baseline.json \
  -dataset training/testdata/pix-risk-v1.jsonl \
  -model-output trained-model.json \
  -report-output training-report.json
```

Every flag is required. `training/testdata/pix-risk-v1.jsonl` holds 8 small
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
9. Writes the training report
   ([`pix-training-report-v2`](docs/contracts/pix-training-report-v2.md)):
   the configuration and its identity, the dataset report with every
   exclusion, the partitions, the coverage of populations and availability
   states, the selection, and the metrics.
10. Writes the model in model format version 2: the network plus its
    provenance, which records the dataset fingerprint, the configuration,
    the selected epoch and threshold, and the SHA-256 of the report. See
    [ADR 0010](docs/adr/0010-trained-models-carry-compact-provenance.md).

Each file is written to a temporary file first and then renamed, so a
failure never leaves a partial artifact. An interrupt (SIGINT or SIGTERM)
cancels the run within one epoch, or within 1,024 records while reading,
and nothing is written. Progress, including the training loss, goes to
standard error. Standard output holds exactly one JSON object, followed by
a newline:

| Exit code | `status` | Meaning |
|-----------|----------|---------|
| 0 | `trained` | The model and the report were written. The object holds `dataset_sha256`, the `records` counts, `model_sha256`, and `report_sha256`. |
| 2 | `invalid_dataset` | The dataset has invalid records. `dataset` holds the dataset report, whose `exclusions` list each problem by `line`, `path`, and `code`. Standard error repeats each problem with a message. Nothing is written. |
| 3 | `selection_infeasible` | No threshold keeps the validation false positive rate within the configured limit. The object holds `threshold_policy`, `max_false_positive_rate`, and `lowest_achievable_false_positive_rate`. Nothing is written. |
| 1 | none | Operational failure, such as a missing flag, an invalid configuration, an unreadable dataset, a train partition without both labels, a validation partition that cannot support the selection, a diverging run, a cancellation, or a failed write. Standard output is empty, and the message goes to standard error. |

### Limitations

Successful training is not production validation. The command shows that
a dataset can be trained and evaluated reproducibly; it does not show that
the model detects fraud. The bundled dataset is synthetic and far too small
for its metrics to mean anything. Before relying on a model, evaluate it on
a large, representative dataset of mature labels, and review its
precision, recall, false positives, and missed losses against your own risk
policy.

- Models trained by `train` cannot score payments yet: the feature
  standardization they need is not stored until the model artifact format
  of #29, and `predict` reads `pix-risk-v1` inputs only from #31.
- Training uses the natural class distribution, with no resampling or
  reweighting. The checkpoint, early stopping, and threshold follow your
  configuration; the toolkit never chooses a risk policy for you, and a
  validation-selected threshold is only as reliable as the validation
  partition is representative.
- All usable records are kept in memory, because every epoch uses the whole
  train partition.
- The same dataset bytes and configuration produce the same model and
  report on the same platform. Across CPU architectures, such as arm64 and
  amd64, scores can differ in their last bits; the toolkit tests that they
  agree within 10^−9.

## Version 2 contracts and `pix-risk-v1`

[`pix-payer-input-v2`](docs/contracts/pix-payer-input-v2.md) and
[`pix-training-example-v2`](docs/contracts/pix-training-example-v2.md)
replace the version 1 contracts, and the feature schema
[`pix-risk-v1`](docs/features/pix-risk-v1.md) replaces
`transaction-features-v1`. It scores new accounts and payments without a
Pix key, payer device, or limit per period, marking unavailable information
with explicit indicators instead of refusing to score. The `pixrisk`
package prepares its 62 features and fits their standardization on the
train partition only. `cmd/train` uses version 2; `cmd/predict` still uses
version 1 until it migrates.
