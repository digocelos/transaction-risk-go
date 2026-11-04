# Training configuration `pix-training-config-v3`

**Status:** Normative for `pix-training-config-v3`. It supersedes [`pix-training-config-v2`](pix-training-config-v2.md), which the toolkit still accepts: version 3 adds the initialization, the loss, and the regularization options, each with a value that reproduces version 2 exactly (ADR 0022).

## 1. Scope

This specification defines the configuration of a training run: a JSON document that fixes the feature schema, the network architecture, its initialization, the algorithm, the loss, the regularization, the seed, the learning rate, the number of epochs, the temporal boundaries, and the selection policies before any dataset is read or any artifact is written. `cmd/train` reads it with `-config` (ADR 0014). The training report records it and its identity ([`pix-training-report-v2`](pix-training-report-v2.md)), and the model artifact records its version ([`pix-risk-model-v1`](pix-risk-model-v1.md)).

The dataset and the output files are not part of the configuration: they are the command's other flags, so one configuration can train on several datasets.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as described in RFC 2119 and RFC 8174 when, and only when, they appear in all capitals.

## 2. Document

A configuration follows the document rules of [`pix-payer-input-v2` §4](pix-payer-input-v2.md#4-document): at most 65,536 bytes, no byte order mark, valid UTF-8, exactly one JSON object, no duplicate members, and no unknown members at any level. Validation reports every issue with a JSON Pointer `path`, a `code`, and a `message` that is not part of the contract, sorted by `path` and then by `code`. The document, version, structure, and consistency stages are those of [`pix-payer-input-v2` §11.1](pix-payer-input-v2.md#111-stages), with `config_version` in place of `contract_version`.

A document whose `config_version` is `pix-training-config-v2` is decoded by [its own contract](pix-training-config-v2.md): the members of this version are unknown members there, and `binary-cross-entropy` is not one of its losses.

## 3. Members

Every member is required and none is nullable. The members of version 2 keep their meaning; this section repeats them so the document stands alone.

| Member | Type | Rules |
|---|---|---|
| `config_version` | string | `pix-training-config-v3`. `pix-training-config-v2` is accepted by its own contract; any other value is rejected with `unsupported_config_version`. |
| `feature_schema` | closed enumeration | `pix-risk-v1`. The network's input size is the feature count of this schema, 62. |
| `architecture` | object | See [3.1](#31-architecture). |
| `initialization` | object | See [3.2](#32-initialization). |
| `algorithm` | closed enumeration | `full-batch-gradient-descent`: every epoch is one gradient descent step over all usable train records, in dataset line order. |
| `loss` | closed enumeration | See [3.3](#33-loss). |
| `regularization` | object | See [3.4](#34-regularization). |
| `seed` | integer | 0 to 2^53−1, with the integer grammar of `pix-payer-input-v2` §5.1. It seeds the initial weights and, separately, the availability dropout masks. |
| `learning_rate` | number | Greater than 0 and at most 100. Any JSON number syntax is accepted, such as `1`, `0.5`, or `1e-3`. |
| `max_epochs` | integer | 1 to 1,000,000. The run trains this many epochs unless early stopping ends it sooner. |
| `boundaries` | object | `train_end` and `validation_end`, both timestamps as in `pix-payer-input-v2` §5.2. `train_end` MUST be earlier than `validation_end`, or `boundaries_out_of_order` is reported at `/boundaries/train_end`. They define the partitions of [`pix-training-example-v2` §11.1](pix-training-example-v2.md#111-partitions). |
| `selection` | object | See [3.5](#35-selection). |

### 3.1 Architecture

| Member | Type | Rules |
|---|---|---|
| `hidden_layers` | array of integers | 0 to 4 elements, each from 1 to 256 neurons, in order from the input. An empty array is a single sigmoid output over the features, a logistic regression. More than 4 elements is reported at the array; an invalid element at its index. |
| `activation` | closed enumeration | `sigmoid`, for every layer. |

The network always has exactly one output, the risk score.

### 3.2 Initialization

| Member | Type | Rules |
|---|---|---|
| `scheme` | closed enumeration | `uniform-0.5`: every weight is drawn uniformly from [−0.5, 0.5), as version 2 does. `scaled-uniform`: the weights of each layer are drawn uniformly from [−√(3/n), √(3/n)), where n is the number of inputs of the layer, so that each pre-activation starts with a variance of about 1 for inputs of unit variance; with 62 standardized inputs, `uniform-0.5` starts with a standard deviation near 2.3 and saturates some neurons from the first epoch. Biases always start at 0. The draws come from a generator seeded with `seed`, in layer, neuron, and input order. |
| `zero_unexercised_inputs` | boolean | When `true`, after the weights are drawn, the first-layer weights of every feature whose value is 0 in every usable train record are set to 0. Full-batch gradient descent never changes the weight of an input that is 0 in every record, and weight decay keeps 0 at 0, so those weights stay 0 for the whole run: a scoring input that activates such a feature gets no response from it, instead of the random initialization. `false` leaves the random weights, as version 2 does. |

An unexercised feature is a fact about the dataset, not the configuration: `cmd/train` names them on standard error, and the inspection report lists them ([`pix-model-inspection-v1` §4.2](pix-model-inspection-v1.md#42-unexercised-features)).

### 3.3 Loss

| Value | Meaning |
|---|---|
| `mean-squared-error` | The mean of (output − target)² over records and outputs, as version 2. Its gradient with respect to the output's pre-activation is 2·(output − target)·output·(1 − output), which vanishes when the output saturates near 0 or 1. |
| `binary-cross-entropy` | The mean of −(target·ln(output) + (1 − target)·ln(1 − output)): the negative log-likelihood of the label under the score. Its gradient with respect to the output's pre-activation is output − target, which does not vanish when the output saturates, so a record the network is confidently wrong about keeps training it. For the reported value, outputs are clamped to [10^−15, 1 − 10^−15], so a saturated output gives a large finite loss instead of infinity. |

The loss is the training objective and the `validation_loss` objective of [3.5](#35-selection): with `binary-cross-entropy`, `selected_objective_value` in the training report is the mean cross-entropy over the usable validation records.

### 3.4 Regularization

| Member | Type | Rules |
|---|---|---|
| `l2` | number | 0 to 1. The weight decay coefficient λ: every weight, not bias, also receives the gradient of (λ/2)·weight², so one update is weight − learning_rate·(gradient + λ·weight). The reported losses exclude the decay term. 0 is no decay, as version 2. Decay shrinks weights that carry little signal and divides the credit between collinear inputs, such as the DICT statistics of the recipient person and of the key, instead of letting one of them take it all. |
| `availability_dropout` | number | 0 to 1. In every epoch, for each usable train record in dataset line order and each optional source of `pix-risk-v1` in the order of [`pix-training-report-v2` §4](pix-training-report-v2.md#4-coverage), one draw from a generator seeded with `seed` decides, with this probability, whether the source's features are set to 0 in that record for that epoch, exactly as the preparation represents an unavailable source ([`pix-risk-v1` §11](../features/pix-risk-v1.md#11-fitted-standardization)): the values and the availability indicators of the source. The generator is separate from the one that draws the initial weights, so the masks of a run depend only on `seed`, the probability, and the order of records and sources. Validation and test records are never masked, and the training loss `cmd/train` prints is measured on the masked records. 0 is no dropout, as version 2. |

Availability dropout trains the network on records whose optional sources are missing at a rate the dataset may never show, so the availability indicators receive gradient and the response to an unavailable source is learned rather than left at the initialization. It is the only option that changes what the network sees; it does not change the dataset, the preparation state, or any report of the data.

### 3.5 Selection

`selection` is as in [`pix-training-config-v2` §3.2](pix-training-config-v2.md#32-selection), with one difference: the `validation_loss` objective of the `best_validation` checkpoint is the configured `loss` over the usable validation records, lower is better, measured on the records as the dataset supplies them.

## 4. Resource limits and determinism

- The limits of [`pix-training-config-v2` §4](pix-training-config-v2.md#4-resource-limits-and-determinism) apply. Availability dropout adds one copy of the train partition's feature vectors to the memory of a run and no training work.
- Records are processed in dataset line order and nothing is shuffled. The same dataset bytes and configuration produce the same model and report, bit for bit, on the same platform; the masks are part of that determinism.
- Across CPU architectures, results can differ in their last bits, within the tolerance of version 2.
- A run fails without producing a model when a parameter, a gradient, or an update becomes non-finite. The clamp of the cross-entropy value never hides a non-finite output: a non-finite output makes the gradients non-finite, and the step fails.

## 5. Validation codes

The catalog is that of [`pix-training-config-v2` §5](pix-training-config-v2.md#5-validation-codes). `out_of_range` also covers `l2` and `availability_dropout` outside 0 to 1, `invalid_enum_value` an unsupported `scheme` or `loss`, and `wrong_type` a `zero_unexercised_inputs` that is not a boolean.

## 6. Canonical form and identity

The canonical form of a valid configuration is its compact JSON with the members in the order of [section 3](#3-members), `initialization` and `regularization` included, boundaries in UTC with RFC 3339 nanosecond formatting, numbers in their shortest form, and only the members the chosen policies use. The configuration's identity, `config_sha256`, is the SHA-256 of its canonical form. A version 3 document with the version 2 values has a different canonical form and identity from the version 2 document, because its version differs, and trains the same model.

## 7. Examples

Every example in [`training-config-examples/`](training-config-examples/) is paired with a `.expected.json` file holding `{"status": "valid"}` or `{"status": "invalid", "issues": [...]}`. The version 2 examples remain valid under their own contract.

| File | Case | Expected |
|---|---|---|
| `14-v3-defaults.json` | The baseline of version 2 as a version 3 document: `uniform-0.5`, no zeroed inputs, the mean squared error, no decay, no dropout. It trains the same model as `01-baseline.json`. | valid |
| `15-v3-regularized.json` | Hidden layers of 16 and 8 neurons, `scaled-uniform` with zeroed unexercised inputs, the binary cross-entropy, `l2` 10^−4, dropout 0.1, learning rate 0.5, the best validation loss every 10 epochs with patience 30, and the highest recall at a false positive rate of at most 0.05. | valid |
| `16-invalid-v3-options-in-v2.json` | A version 2 document with `initialization`, `regularization`, and the cross-entropy. | `unknown_member` twice, `invalid_enum_value` |
| `17-invalid-v3-ranges.json` | The scheme `xavier`, a string for `zero_unexercised_inputs`, the loss `hinge`, `l2` 2, and dropout −0.1. | `invalid_enum_value` twice, `wrong_type`, `out_of_range` twice |
| `18-invalid-v3-missing-members.json` | A version 3 document without `initialization` and `regularization`. | `required` twice |
