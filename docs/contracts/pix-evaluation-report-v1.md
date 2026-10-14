# Evaluation report `pix-evaluation-report-v1`

**Status:** Normative for `pix-evaluation-report-v1`.

## 1. Scope

An evaluation measures a saved [`pix-risk-model-v1`](pix-risk-model-v1.md) artifact on an explicit scope of a [`pix-training-example-v2`](pix-training-example-v2.md) dataset, and the evaluation report records the result. `cmd/evaluate` and the `modelevaluation` package produce it (ADR 0017).

Evaluation only reads. It never trains, refits the preparation state, selects a checkpoint, or tunes a threshold, and it never changes the artifact or the dataset. It scores the usable records in scope with the artifact's own preparation state and network, so an artifact evaluated on the partitions of its training dataset reproduces the metrics of its [training report](pix-training-report-v2.md) exactly.

The report describes software behavior on the data supplied. Its metrics are not a claim of detection quality, calibration, or regulatory compliance, and a complete evaluation is not a quality pass.

## 2. Inputs

| Input | Rules |
|---|---|
| Model artifact | The exact bytes of a `pix-risk-model-v1` artifact, decoded as strictly as [`pix-risk-model-v1` §2](pix-risk-model-v1.md#2-model-artifact) requires. Its SHA-256 identifies it in the report. |
| Dataset | A `pix-training-example-v2` dataset, read and classified with `pix-risk-v1` exactly as for training ([`pix-training-example-v2` §9](pix-training-example-v2.md#9-record-classification)). A dataset with any invalid record is not evaluated: the evaluation fails with the dataset report and every diagnostic, and no report is written. |
| Scope | See [section 3](#3-scope). |
| Threshold override | Optional. A number from 0 to 1 to measure at instead of the artifact's threshold, for this evaluation only. |
| Reference dataset | Optional. The dataset the model was trained on, to measure group overlap when another dataset is evaluated. Its SHA-256 MUST be the artifact's `dataset_sha256`; otherwise the evaluation fails. |

The artifact, the scope, and the threshold override are validated before any dataset is read. A reference dataset is read before the evaluated dataset.

## 3. Scope

The scope selects records by the `decision_time` of their scoring input, compared as instants. It is either a partition of the artifact's boundaries or a window:

| Scope | Records |
|---|---|
| `validation` | `train_end < decision_time ≤ validation_end` |
| `test` | `decision_time > validation_end` |
| Window | `after < decision_time`, and `decision_time ≤ through` when `through` is set |

`train_end` and `validation_end` are the artifact's boundaries. A window's `after` MUST NOT be earlier than `train_end`, and `through`, when set, MUST be later than `after`. The train partition is never evaluated, because metrics on training examples are not evaluation results ([`pix-training-example-v2` §11.2](pix-training-example-v2.md#112-roles)).

A scope that reaches past `validation_end` measures held-out data. Software cannot stop a person from evaluating held-out data repeatedly and changing a model after seeing the results; doing so turns that data into validation data, and a changed model needs new held-out data (ADR 0017).

## 4. Encoding

The report is one JSON object, indented with two spaces and followed by a newline. The same inputs produce the same bytes when evaluated by the same build: member order is fixed, map keys are sorted, timestamps are written in UTC, numbers use the shortest representation that round-trips, and no value comes from the system clock. Undefined values are `null`, never NaN, infinity, or zero.

## 5. Members

| Member | Type | Meaning |
|---|---|---|
| `evaluation_report_version` | string | `pix-evaluation-report-v1`. |
| `software` | object | `module`, `version`, and `revision` of the build that evaluated, as in [`pix-risk-model-v1` §2](pix-risk-model-v1.md#2-model-artifact). |
| `model` | object | See [section 5.1](#51-model). |
| `dataset` | object | The dataset report of the evaluated dataset ([`pix-training-example-v2` §13](pix-training-example-v2.md#13-dataset-report)), covering every record, in scope or not. |
| `same_dataset_as_training` | boolean | Whether the evaluated dataset's SHA-256 is the artifact's `dataset_sha256`. |
| `scope` | object | See [section 5.2](#52-scope). |
| `records` | object | See [section 5.3](#53-records). |
| `coverage` | object | The coverage of the usable records in scope: `usable_records`, `populations`, `limit_periods`, and `sources`, as one partition of [`pix-training-report-v2` §4](pix-training-report-v2.md#4-coverage). |
| `group_overlap` | object | See [section 5.4](#54-group-overlap). |
| `threshold` | object | `source`, `artifact` or `override`, and `value`, the threshold every metric is measured at. The artifact's own threshold is always `model.threshold`, and an override never changes it. |
| `metrics` | object | The metrics of [`pix-training-example-v2` §11.5](pix-training-example-v2.md#115-metrics) over the usable records in scope, at `threshold.value`. |
| `completeness` | object | See [section 5.5](#55-completeness). |

### 5.1 Model

| Member | Meaning |
|---|---|
| `artifact_version` | `pix-risk-model-v1`. |
| `artifact_sha256` | The SHA-256 of the artifact's exact bytes. |
| `software` | The build that trained the model, from the artifact. |
| `contracts` | `scoring_input` `pix-payer-input-v2` and `training_example` `pix-training-example-v2`. |
| `feature_schema` | `pix-risk-v1`. |
| `config_version`, `config_sha256` | The training configuration's version and identity. |
| `training_report_version`, `training_report_sha256` | The training report's version and the SHA-256 of its exact bytes. |
| `training_dataset_sha256` | The fingerprint of the training dataset. |
| `train_end`, `validation_end` | The artifact's boundaries, in UTC. |
| `selected_epoch` | The epochs that produced the parameters. |
| `threshold` | The `policy` and `value` the model was selected with. |

### 5.2 Scope

`partition` is `validation` or `test` for a partition scope and `null` for a window. `after` is the exclusive start and `through` the inclusive end, `null` when the scope has no end; both are in UTC. A partition scope records the artifact's boundaries.

### 5.3 Records

| Member | Meaning |
|---|---|
| `in_scope` | The records in scope: usable `fraud` and `legitimate` records, and the `insufficient` and `immature` records excluded from the metrics. |
| `out_of_scope` | The records that are not invalid and are outside the scope. |
| `in_validation_range` | The usable records in scope whose decision time is at or before `validation_end`, the period the model's checkpoint and threshold were chosen on. On the training dataset, those are the validation records themselves. |

Immature records are excluded because their labels may still change ([`pix-training-example-v2` §9.2](pix-training-example-v2.md#92-label-maturity)); a scope close to the dataset's last label as-of time can hold many of them.

### 5.4 Group overlap

Group overlap compares the group keys of the usable records in scope with the keys of the usable records in the artifact's train partition (`decision_time ≤ train_end`) of the training dataset. Group keys are never model inputs.

| Member | Meaning |
|---|---|
| `reference` | Where the train records come from: `reference_dataset`, when a reference dataset was supplied; otherwise `evaluated_dataset`, when the evaluated dataset is the training dataset; otherwise `unavailable`. |
| `shared_keys` | The number of distinct keys in scope that train records also have; `null` when `reference` is `unavailable`. |
| `records_sharing_train_keys` | The number of usable records in scope whose key train records also have; `null` when `reference` is `unavailable`. |
| `records_without_group_key` | The number of usable records in scope without a key, which cannot be compared. |

Like the training report, the evaluation discloses overlap and does not remove records because of it.

### 5.5 Completeness

`complete` is `true` when `incomplete` is empty. `incomplete` lists each reason, in the order below, with the `metrics` it leaves undefined, trivial, or partial, by their member names in `metrics`:

| Reason | When | Metrics |
|---|---|---|
| `no_usable_examples` | No usable record is in scope. The other reasons are then not listed. | Every metric. |
| `no_fraud_examples` | No usable `fraud` record is in scope, so detection cannot be measured. | `precision`, `recall`, `f1`, `pr_auc`, `pr_curve`, `fraud_amount_cents_detected`, `fraud_amount_cents_missed`, `known_loss_cents_detected`, `known_loss_cents_missed`, `unknown_loss_frauds_detected`, `unknown_loss_frauds_missed` |
| `no_legitimate_examples` | No usable `legitimate` record is in scope, so false alerts cannot be measured. | `precision`, `f1`, `false_positive_rate`, `false_positives_per_1000_legitimate`, `pr_auc`, `pr_curve`, `legitimate_amount_cents_flagged` |
| `unknown_loss_frauds` | A fraud in scope has an unknown loss, so the known loss sums are partial. | `known_loss_cents_detected`, `known_loss_cents_missed` |

An incomplete evaluation still reports every metric; the reasons tell which ones not to read as measurements.

## 6. Consistency

The encoder refuses a report that contradicts itself: an unknown threshold source, an `artifact` threshold that is not the model's, metrics at another threshold, a dataset with invalid records, a `same_dataset_as_training` that does not compare the fingerprints, a scope that starts before `train_end`, is empty, or does not match its partition's boundaries, an overlap reference without the counts it implies, metrics or coverage that do not count the usable records in scope, or a completeness that does not follow from the records and metrics.
