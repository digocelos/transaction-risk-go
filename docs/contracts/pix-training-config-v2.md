# Training configuration `pix-training-config-v2`

**Status:** Normative for `pix-training-config-v2`. It replaces `pix-training-config-v1`, which the toolkit no longer accepts: version 2 adds validation-based checkpoints, early stopping, threshold selection, and an explicit test evaluation (ADR 0014, ADR 0015).

## 1. Scope

This specification defines the configuration of a training run: a JSON document that fixes the feature schema, the network architecture, the algorithm, the loss, the seed, the learning rate, the number of epochs, the temporal boundaries, and the selection policies before any dataset is read or any artifact is written. `cmd/train` reads it with `-config` (ADR 0014). The training report records it and its identity ([`pix-training-report-v2`](pix-training-report-v2.md)).

The dataset and the output files are not part of the configuration: they are the command's other flags, so one configuration can train on several datasets.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as described in RFC 2119 and RFC 8174 when, and only when, they appear in all capitals.

## 2. Document

A configuration follows the document rules of [`pix-payer-input-v2` §4](pix-payer-input-v2.md#4-document): at most 65,536 bytes, no byte order mark, valid UTF-8, exactly one JSON object, no duplicate members, and no unknown members at any level. Validation reports every issue with a JSON Pointer `path`, a `code`, and a `message` that is not part of the contract, sorted by `path` and then by `code`. The document, version, structure, and consistency stages are those of [`pix-payer-input-v2` §11.1](pix-payer-input-v2.md#111-stages), with `config_version` in place of `contract_version`.

## 3. Members

Every member is required and none is nullable.

| Member | Type | Rules |
|---|---|---|
| `config_version` | string | `pix-training-config-v2`. Any other value, including `pix-training-config-v1`, is rejected with `unsupported_config_version`. |
| `feature_schema` | closed enumeration | `pix-risk-v1`. The network's input size is the feature count of this schema, 62. |
| `architecture` | object | See [3.1](#31-architecture). |
| `algorithm` | closed enumeration | `full-batch-gradient-descent`: every epoch is one gradient descent step over all usable train records, in dataset line order. |
| `loss` | closed enumeration | `mean-squared-error`, averaged over records and outputs. |
| `seed` | integer | 0 to 2^53−1, with the integer grammar of `pix-payer-input-v2` §5.1. It seeds the initial weights. |
| `learning_rate` | number | Greater than 0 and at most 100. Any JSON number syntax is accepted, such as `1`, `0.5`, or `1e-3`. |
| `max_epochs` | integer | 1 to 1,000,000. The run trains this many epochs unless early stopping ends it sooner. |
| `boundaries` | object | `train_end` and `validation_end`, both timestamps as in `pix-payer-input-v2` §5.2. `train_end` MUST be earlier than `validation_end`, or `boundaries_out_of_order` is reported at `/boundaries/train_end`. They define the partitions of [`pix-training-example-v2` §11.1](pix-training-example-v2.md#111-partitions). |
| `selection` | object | See [3.2](#32-selection). |

### 3.1 Architecture

| Member | Type | Rules |
|---|---|---|
| `hidden_layers` | array of integers | 0 to 4 elements, each from 1 to 256 neurons, in order from the input. An empty array is a single sigmoid output over the features, a logistic regression. More than 4 elements is reported at the array; an invalid element at its index. |
| `activation` | closed enumeration | `sigmoid`, for every layer. |

The network always has exactly one output, the risk score. A larger network is not assumed to be better; the configuration only makes the architecture explicit.

### 3.2 Selection

`selection` has exactly the members `checkpoint`, `early_stopping`, `threshold`, and `test_evaluation`. Each of the first three is an object whose `policy` decides its other members; members of another policy are unknown members. When `policy` is invalid, the other members are not checked. Selection uses the validation partition only; the train partition is never evaluated, and the test partition never influences a choice (ADR 0015).

**`checkpoint`**

| Policy | Members | Meaning |
|---|---|---|
| `final_epoch` | none | The model is the network after the last epoch trained. |
| `best_validation` | `objective`, `evaluation_interval`, `min_improvement` | The validation objective is measured after every `evaluation_interval` epochs and after the last epoch trained, and the network with the best value is restored. |

- `objective`: `validation_loss`, the mean squared error over the usable validation records (lower is better), or `validation_pr_auc`, their average precision as in [`pix-training-example-v2` §11.5](pix-training-example-v2.md#115-metrics) (higher is better).
- `evaluation_interval`: an integer from 1 to 1,000,000 epochs.
- `min_improvement`: a number from 0 to 1. An evaluation improves on the best one only when it is better by more than this amount, so equal values keep the earliest epoch.

**`early_stopping`**

| Policy | Members | Meaning |
|---|---|---|
| `none` | none | Train every requested epoch. |
| `patience` | `patience` | Stop after `patience` consecutive validation evaluations without improvement, an integer from 1 to 1,000,000. It requires the `best_validation` checkpoint; otherwise `incompatible_options` is reported at `/selection/early_stopping/policy`. |

**`threshold`**

| Policy | Members | Meaning |
|---|---|---|
| `fixed` | `value` | The decision threshold is `value`, a number from 0 to 1. |
| `max_recall_at_max_false_positive_rate` | `max_false_positive_rate` | The threshold is chosen on the validation partition with the selected network: the candidates are the distinct validation scores, and among those whose false positive rate, FP / (FP + TN), is at most `max_false_positive_rate`, a number from 0 to 1, the one with the highest recall wins; among equal recalls, the highest threshold wins, because it raises the fewest alerts. Equal scores are one candidate and are never split. |

The false positive rate limit is the integrator's risk policy; the toolkit never invents one. When no candidate satisfies it, the run writes nothing and reports the lowest false positive rate any candidate reaches (`cmd/train` exit code 3).

**`test_evaluation`**

- `skip`: a model-selection run. The test partition is never scored; the report records `test_evaluated: false` and no test metrics.
- `after_selection`: after the preparation state, the network, the checkpoint, and the threshold are frozen, the test partition is scored once.

**Validation requirements.** These are checked after the dataset is partitioned and before any training. A run that does not meet them fails without writing anything:

- `best_validation` needs at least one usable validation record, and `validation_pr_auc` at least one usable validation fraud record.
- `max_recall_at_max_false_positive_rate` needs at least one usable fraud and one usable legitimate validation record.

With `final_epoch` and a `fixed` threshold, the validation partition can be empty or have one label; its undefined metrics are `null`.

## 4. Resource limits and determinism

- The limits above bound the network to at most 4 hidden layers of 256 neurons, about 214,000 parameters, and a run to at most 1,000,000 epochs. Limits on dataset size and memory are documented separately (#32).
- Records are processed in dataset line order and nothing is shuffled. The same dataset bytes and configuration produce the same model and report, bit for bit, on the same platform.
- Across CPU architectures, results can differ in their last bits: fused multiply-add and `math.Exp` differ between arm64 and amd64. Scores trained with [`01-baseline.json`](training-config-examples/01-baseline.json) on the training fixture differ between the two by at most about 6·10^−18. The toolkit tests that they agree within 10^−9 on every architecture CI runs; this is the documented cross-architecture tolerance.
- A run fails without producing a model when a parameter, a gradient, or an update becomes non-finite. The network keeps its previous parameters, and the command writes nothing.

## 5. Validation codes

The catalog is closed. Codes shared with `pix-payer-input-v2` keep their meaning there.

| Code | Condition |
|---|---|
| `document_too_large`, `bom_not_allowed`, `invalid_utf8`, `invalid_json`, `trailing_data`, `duplicate_member` | Document rules, as in `pix-payer-input-v2`. |
| `wrong_type`, `required`, `unknown_member`, `null_not_allowed`, `invalid_number_format`, `invalid_format`, `invalid_enum_value` | As in `pix-payer-input-v2`, for configuration members. An unsupported option, such as the objective `accuracy`, is `invalid_enum_value`. |
| `out_of_range` | A number, an integer, the number of hidden layers, or a layer size outside its range. |
| `unsupported_config_version` | `config_version` is a string other than `pix-training-config-v2`. |
| `boundaries_out_of_order` | `train_end` is not earlier than `validation_end`. |
| `incompatible_options` | Two options cannot be combined, such as `patience` early stopping with the `final_epoch` checkpoint. |

## 6. Canonical form and identity

The canonical form of a valid configuration is its compact JSON with the members in the order of [section 3](#3-members), boundaries in UTC with RFC 3339 nanosecond formatting, numbers in their shortest form, and only the members the chosen policies use. Two documents that differ only in formatting, member order, timestamp offsets, or number spelling have the same canonical form. The configuration's identity, `config_sha256`, is the SHA-256 of its canonical form, as 64 lowercase hexadecimal digits.

## 7. Examples

Every example in [`training-config-examples/`](training-config-examples/) is paired with a `.expected.json` file holding `{"status": "valid"}` or `{"status": "invalid", "issues": [...]}`.

| File | Case | Expected |
|---|---|---|
| `01-baseline.json` | One hidden layer of 4 neurons, 5,000 epochs, the final epoch, a fixed threshold of 0.5, and the test evaluation. | valid |
| `02-no-hidden-layer.json` | No hidden layer, and the test partition skipped. | valid |
| `03-two-hidden-layers.json` | Hidden layers of 16 and 8 neurons. | valid |
| `04-best-validation-loss.json` | The best validation loss every 10 epochs, patience 20, and the highest recall at a false positive rate of at most 0.5. | valid |
| `05-best-validation-pr-auc.json` | The best validation PR-AUC every 50 epochs, with the test partition skipped. | valid |
| `06-invalid-unsupported-version.json` | `pix-training-config-v1`. | only `unsupported_config_version` |
| `07-invalid-members-of-another-policy.json` | `objective` with `final_epoch`, and `max_false_positive_rate` with `fixed`. | `unknown_member` twice |
| `08-invalid-incompatible-options.json` | `patience` with `final_epoch`. | `incompatible_options` |
| `09-invalid-selection-ranges.json` | Interval 0, improvement 2, patience 0, and false positive rate 1.5. | `out_of_range` four times |
| `10-invalid-unsupported-options.json` | The objective `accuracy`, the threshold policy `max_f1`, and the test evaluation `always`. | `invalid_enum_value` three times |
| `11-invalid-dimensions-and-ranges.json` | Layer sizes 0, 257, and 4.5, learning rate 0, and 1,000,001 epochs. | `out_of_range` four times, `invalid_number_format` |
| `12-invalid-boundaries-order.json` | Equal boundaries. | `boundaries_out_of_order` |
| `13-invalid-missing-policy-members.json` | `best_validation` and the selected threshold without their members. | `required` four times |
