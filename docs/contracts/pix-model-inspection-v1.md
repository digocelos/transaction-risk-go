# Inspection report `pix-model-inspection-v1`

**Status:** Normative for `pix-model-inspection-v1`.

## 1. Scope

An inspection measures a trained [`pix-risk-model-v1`](pix-risk-model-v1.md) artifact itself, together with the run that published it ([`pix-risk-run-v1`](pix-risk-model-v1.md#3-training-run)) and the dataset it was trained on, and the inspection report records the result. `cmd/inspect` and the `modelinspection` package produce it.

The training report measures outcomes: metrics on partitions. The inspection report measures the model: how far each parameter moved from its random initialization, which inputs no train record ever exercised, how the score responds to each standardized feature across a grid in real units, and how the validation metrics change when an optional source is unavailable. These are the measurements that explain why a model behaves as it does on inputs its training data did not cover, and they belong in a report that anyone can read before a model is evaluated on held-out data ([readiness](../readiness.md#5-deferred-validation-with-real-data)).

Inspection only reads. It never trains, refits the preparation state, selects a checkpoint, or tunes a threshold, and it never changes the run or the dataset. Scores are computed with the artifact's own preparation state and network, with the arithmetic of scoring, so the baseline of the missingness probes reproduces the validation metrics of the [training report](pix-training-report-v2.md) exactly.

The report describes software behavior on the data supplied. Nothing in it is a verdict: a feature that moves against its expected direction, an unexercised input, or a probe that lets frauds evade is a fact about one model on one dataset, for the maintainer to weigh.

## 2. Inputs

| Input | Rules |
|---|---|
| Run | A `pix-risk-run-v1` directory, verified completely as [`pix-risk-model-v1` §3](pix-risk-model-v1.md#3-training-run) requires: every file against the manifest, the artifact, the configuration, and their bindings. The artifact and the configuration come from the verified files; the configuration supplies the `seed`, the architecture, the initialization, and whether unexercised inputs were zeroed, which rebuild the network training started from exactly as `training.InitialNetwork` does. |
| Dataset | The run's training dataset, read, classified, partitioned, and prepared with `pix-risk-v1` exactly as training did ([`pix-training-example-v2` §9](pix-training-example-v2.md#9-record-classification)). Its SHA-256 MUST be the artifact's `dataset_sha256`, and the preparation state fitted on its train partition MUST equal the artifact's; otherwise the inspection fails. A dataset with any invalid record is not inspected: the inspection fails with the dataset report and every diagnostic, and no report is written. |

The run is verified before the dataset is read. There is no scope and no threshold override: inspection uses the artifact's partitions and threshold.

## 3. Encoding

The report is one JSON object, indented with two spaces and followed by a newline. The same run and dataset produce the same bytes when inspected by the same build: member order is fixed, timestamps are written in UTC, numbers use the shortest representation that round-trips, and no value comes from the system clock. Undefined values are `null`, never NaN, infinity, or zero.

## 4. Members

| Member | Type | Meaning |
|---|---|---|
| `inspection_report_version` | string | `pix-model-inspection-v1`. |
| `software` | object | `module`, `version`, and `revision` of the build that inspected, as in [`pix-risk-model-v1` §2](pix-risk-model-v1.md#2-model-artifact). |
| `model` | object | The identity of the inspected artifact and of what produced it, with the members of [`pix-evaluation-report-v1` §5.1](pix-evaluation-report-v1.md#51-model). |
| `dataset` | object | The dataset report of the training dataset ([`pix-training-example-v2` §13](pix-training-example-v2.md#13-dataset-report)). |
| `partitions` | object | The partitions of the dataset at the artifact's boundaries, as in [`pix-training-report-v2` §3](pix-training-report-v2.md#3-members); they equal the training report's. |
| `parameters` | object | See [section 4.1](#41-parameters). |
| `unexercised_features` | array of strings | The names of the features whose value is 0 in every usable train record, in descriptor order; empty when every feature was exercised. See [section 4.2](#42-unexercised-features). |
| `partial_dependence` | object | See [section 4.3](#43-partial-dependence). |
| `missingness` | object | See [section 4.4](#44-missingness). |

### 4.1 Parameters

| Member | Meaning |
|---|---|
| `sizes` | The architecture, `[62, hidden layers…, 1]`, as the artifact records. |
| `seed` | The configuration's `seed`, from which the initial network is rebuilt as training did, with the configuration's initialization scheme and, when it zeroes unexercised inputs, their columns at 0. |
| `inputs` | One object per feature, in descriptor order: `name`, `kind`, `column_norm`, the L2 norm of the feature's column of first-layer weights; `change_norm`, the L2 norm of that column's change since initialization; `max_abs_change`, the largest absolute change of one weight in it; `train_nonzero_records`, the number of usable train records in which the feature is not 0; and `exercised_in_train`, whether that number is positive. |
| `hidden_layers` | One object per hidden layer, in order: `neurons`, one object per neuron with `bias`, `input_norm`, the L2 norm of its weights, `output_norm`, the L2 norm of the weights that read it in the next layer, and `activation`, its output on the usable train records as `mean`, `below_0_01`, and `above_0_99`, the shares of records on which the output is below 0.01 and above 0.99; and `max_abs_cosine`, the pair of neurons of the layer whose weight rows have the largest absolute cosine, as `value` and `neurons`, or `null` for a layer with one neuron. |
| `output` | The output neuron's `weights`, one per neuron of the last hidden layer, and `bias`. |

Full-batch gradient descent changes a first-layer weight only through records in which its input is not 0, so a column with `exercised_in_train` `false` has `change_norm` exactly 0 and holds its initialization: the random draw, or 0 when the configuration zeroes unexercised inputs ([`pix-training-config-v3` §3.2](pix-training-config-v3.md#32-initialization)), in which case its `column_norm` is 0 too. Neurons that are saturated on most records, or pairs with a cosine near 1 or −1, show capacity the model does not use.

### 4.2 Unexercised features

A feature no usable train record exercised reaches the network, when a scoring input activates it, through weights that training never touched: the network's response to it is the random initialization, in a direction that depends only on the seed. Indicators of states the dataset never had, such as `limit_zero` when no train record has a zero limit, or `key_type_other` when every key type was recognized, are the usual case. The training report's `not_exercised_in_train` lists availability states by source; this list is by feature, and a feature can be unexercised even when its source is always known.

### 4.3 Partial dependence

For each standardized feature of [`pix-risk-v1` §9](../features/pix-risk-v1.md#9-features), in descriptor order, the score of every usable validation record is recomputed with the feature set to each value of a fixed grid, in real units, and its availability indicator forced to known. Every other feature keeps the record's value.

| Member | Meaning |
|---|---|
| `partition` | `validation`. |
| `records` | The usable validation records, as `fraud` and `legitimate`. |
| `tolerance` | `0.001`, the smallest change of a mean score that counts as a move against a direction. |
| `features` | One object per standardized feature: `name`; `unit`; `expected_direction`; `train_range`; `grid`; and `violations`. |

The real units and their fixed transformations are those of [`pix-risk-v1` §9](../features/pix-risk-v1.md#9-features): `centavos` and `count` and `days` through `log1p`, `ratio` through `log1p` for `limit_utilization` and through `ln` for `amount_to_median_90d`. The grid values are:

| Unit | Features | Grid |
|---|---|---|
| `centavos` | `amount` | 100, 500, 2,000, 5,000, 10,000, 20,000, 50,000, 100,000, 200,000, 500,000, 1,000,000, 5,000,000 |
| `count` | every count: payer histories, device history, and DICT statistics | 0, 1, 2, 3, 5, 10, 20, 50, 100, 300, 1,000, 10,000 |
| `ratio` | `amount_to_median_90d` | 0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5, 10, 20, 50, 100 |
| `ratio` | `limit_utilization` | 0, 0.1, 0.25, 0.5, 0.75, 0.9, 1, 1.25, 1.5, 2, 5, 10 |
| `days` | `recipient_account_age`, `key_link_age`, `key_ownership_age` | 0, 1, 3, 7, 14, 30, 60, 90, 180, 365, 730, 1,825 |

`expected_direction` is the direction [`pix-risk-v1` §20](../features/pix-risk-v1.md#20-expected-directions) declares for the feature: `risk_increasing`, `risk_decreasing`, or `none`. `train_range` is the `min` and `max` of the feature's raw values among the usable train records in which it is available, in the same units. Each `grid` entry has `value`, `in_train_range`, whether the value is within `train_range`, and, for `fraud` and for `legitimate`, `mean_score` and `flagged_share`, the share of records at or above the artifact's threshold, or `null` for a label without records.

`violations` lists, for `fraud` and then for `legitimate`, each pair of consecutive grid points between which the mean score moves against the expected direction by more than `tolerance`: `label`, `from`, `to`, `in_train_range`, whether both points are within the train range, `mean_score_from`, and `mean_score_to`. A feature with direction `none` has no violations. A violation outside the train range is extrapolation; one inside it is a relation the model learned from the data.

### 4.4 Missingness

The validation records are scored at baseline, as the dataset supplies them, and then with one probe applied to every record. A probe sets the features of one optional source, or of several, to 0, exactly as `pix-risk-v1` represents an unavailable source ([§11](../features/pix-risk-v1.md#11-fitted-standardization)), and, for a population, sets its discriminator to 1 as well.

| Member | Meaning |
|---|---|
| `partition` | `validation`. |
| `records` | The usable validation records, as `fraud` and `legitimate`. |
| `threshold` | The artifact's threshold, used for every count. |
| `baseline` | The summary at baseline, which reproduces the training report's validation `recall`, `false_positives`, `false_positive_rate`, and `pr_auc`. |
| `probes` | One object per probe, in the order below. |

A summary has `recall`, `false_positives`, `false_positive_rate`, `pr_auc`, each `null` when it measures nothing, and `mean_score`, the mean score of the `fraud` and of the `legitimate` records, `null` for a label without records.

Each probe has `name`, `kind`, `set`, the indicators set to 1, `masked`, the features set to 0, both by name, `result`, its summary, `frauds_evading`, the frauds at or above the threshold at baseline and below it with the probe, `legitimate_newly_flagged`, the legitimate records below the threshold at baseline and at or above it with the probe, and `mean_score_change`, the mean change of the score by label.

| Order | `name` | `kind` | What is masked |
|---|---|---|---|
| 1–14 | each optional source, in the order of [`pix-training-report-v2` §4](pix-training-report-v2.md#4-coverage) | `source` | The features the source feeds: `period_limit` feeds 14–16, `payer_device_history` 19–20, `payer_device_registration` 21–22, `recipient_pre_registration` 17–18, `recipient_account_entry` 23–24, `recipient_pix_key_entry` 25–33, `person_spi` 34–37, `person_fraud_markers` 38–43, `person_infraction_reports` 44–45, `person_entries` 46–47, `key_spi` 48–51, `key_fraud_markers` 52–57, `key_infraction_reports` 58–59, and `key_entries` 60–61. |
| 15 | `dict_statistics` | `sources` | The eight DICT groups of the person and of the key, 34–61: a DICT lookup that failed entirely. |
| 16 | `no_pix_key` | `population` | Sets `no_pix_key` and masks the key entry and the four key groups, 25–33 and 48–61: a payment by typed account data. |
| 17 | `no_payer_device` | `population` | Sets `no_payer_device` and masks the device observations, 19–22. |

A probe measures what the model would do if the source were unavailable for every validation record, not how often it is unavailable; the coverage of the training report says that. Frauds that evade when a source is masked, and legitimate records that are flagged, show where the model's answer to an unavailable source is not neutral.

## 5. Consistency

The encoder refuses a report that contradicts itself: a dataset with invalid records or one that is not the model's training dataset, partitions at other boundaries, sizes that are not `[62, …, 1]` or hidden layers and an output that do not follow them, inputs that are not the `pix-risk-v1` descriptor in order or whose `exercised_in_train` does not follow their train records, an `unexercised_features` list that does not list those inputs in order, a partial dependence that does not cover every standardized feature in order, another partition or tolerance, probes at another threshold or in another order, record counts that disagree between the partial dependence, the probes, and the partitions, or a cosine reported for a one-neuron layer or missing for a larger one.

## 6. Example

The report of the synthetic quick-start model, abbreviated to one input, one neuron, one feature, and one probe:

```json
{
  "inspection_report_version": "pix-model-inspection-v1",
  "software": {"module": "github.com/digocelos/transaction-risk-go", "version": "(devel)", "revision": "unknown"},
  "model": {"artifact_version": "pix-risk-model-v1", "artifact_sha256": "…", "…": "…"},
  "dataset": {"dataset_report_version": "pix-training-dataset-report-v1", "…": "…"},
  "partitions": {"train_end": "2026-06-05T00:00:00Z", "validation_end": "2026-06-12T00:00:00Z", "…": "…"},
  "parameters": {
    "sizes": [62, 8, 1],
    "seed": 42,
    "inputs": [
      {"name": "amount", "kind": "standardized", "column_norm": 1.21, "change_norm": 0.46, "max_abs_change": 0.24, "train_nonzero_records": 2410, "exercised_in_train": true}
    ],
    "hidden_layers": [
      {"neurons": [{"bias": -0.05, "input_norm": 2.48, "output_norm": 0.97, "activation": {"mean": 0.63, "below_0_01": 0, "above_0_99": 0.02}}],
       "max_abs_cosine": {"value": -0.39, "neurons": [0, 5]}}
    ],
    "output": {"weights": [-2.58, 0.79, 2.97, -1.4, 0.7, -1.25, -2.45, 1.19], "bias": 0.8}
  },
  "unexercised_features": ["limit_zero", "key_type_other"],
  "partial_dependence": {
    "partition": "validation",
    "records": {"fraud": 3, "legitimate": 310},
    "tolerance": 0.001,
    "features": [
      {"name": "person_fraud_markers_m12", "unit": "count", "expected_direction": "risk_increasing",
       "train_range": {"min": 0, "max": 12},
       "grid": [{"value": 0, "in_train_range": true, "fraud": {"mean_score": 0.85, "flagged_share": 1}, "legitimate": {"mean_score": 0.01, "flagged_share": 0.01}}],
       "violations": [{"label": "fraud", "from": 5, "to": 10, "in_train_range": true, "mean_score_from": 0.937, "mean_score_to": 0.932}]}
    ]
  },
  "missingness": {
    "partition": "validation",
    "records": {"fraud": 3, "legitimate": 310},
    "threshold": 0.5,
    "baseline": {"recall": 1, "false_positives": 2, "false_positive_rate": 0.0065, "pr_auc": 0.9, "mean_score": {"fraud": 0.85, "legitimate": 0.01}},
    "probes": [
      {"name": "recipient_pix_key_entry", "kind": "source", "set": [], "masked": ["key_link_age", "key_ownership_age", "key_type_cpf", "key_type_cnpj", "key_type_phone", "key_type_email", "key_type_evp", "key_type_other", "key_entry_known"],
       "result": {"recall": 0.67, "false_positives": 1, "false_positive_rate": 0.0032, "pr_auc": 0.8, "mean_score": {"fraud": 0.65, "legitimate": 0.009}},
       "frauds_evading": 1, "legitimate_newly_flagged": 0, "mean_score_change": {"fraud": -0.2, "legitimate": -0.001}}
    ]
  }
}
```

The numbers are illustrative; a real report holds every input, neuron, feature, grid point, and probe. `cmd/inspect` prints the identities of the report, the model, and the dataset, the unexercised features, and the number of direction violations on standard output.

## 7. Out of scope

Gradients and attributions per record, monotonicity constraints in the network, and any threshold on what the report measures: what counts as too many violations or too much sensitivity is the maintainer's decision, with the risk owners.
