# Training configuration `pix-training-config-v1`

**Status:** Normative for `pix-training-config-v1`.

## 1. Scope

This specification defines the configuration of a training run: a JSON document that fixes the feature schema, the network architecture, the algorithm, the loss, the seed, the learning rate, the number of epochs, the temporal boundaries, and the selection policies before any dataset is read or any artifact is written. `cmd/train` reads it with `-config` (ADR 0014).

The dataset and the output files are not part of the configuration: they are the command's other flags, so one configuration can train on several datasets.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as described in RFC 2119 and RFC 8174 when, and only when, they appear in all capitals.

## 2. Document

A configuration follows the document rules of [`pix-payer-input-v2` §4](pix-payer-input-v2.md#4-document): at most 65,536 bytes, no byte order mark, valid UTF-8, exactly one JSON object, no duplicate members, and no unknown members at any level. Validation reports every issue with a JSON Pointer `path`, a `code`, and a `message` that is not part of the contract, sorted by `path` and then by `code`. The document, version, structure, and consistency stages are those of [`pix-payer-input-v2` §11.1](pix-payer-input-v2.md#111-stages), with `config_version` in place of `contract_version`.

## 3. Members

Every member is required and none is nullable.

| Member | Type | Rules |
|---|---|---|
| `config_version` | string | `pix-training-config-v1`. Any other value is rejected with `unsupported_config_version`. |
| `feature_schema` | closed enumeration | `pix-risk-v1`. The network's input size is the feature count of this schema, 62. |
| `architecture` | object | See [3.1](#31-architecture). |
| `algorithm` | closed enumeration | `full-batch-gradient-descent`: every epoch is one gradient descent step over all usable train records, in dataset line order. |
| `loss` | closed enumeration | `mean-squared-error`, averaged over records and outputs. |
| `seed` | integer | 0 to 2^53−1, with the integer grammar of `pix-payer-input-v2` §5.1. It seeds the initial weights. |
| `learning_rate` | number | Greater than 0 and at most 100. Any JSON number syntax is accepted, such as `1`, `0.5`, or `1e-3`. |
| `max_epochs` | integer | 1 to 1,000,000. With `checkpoint` `final_epoch`, the run trains exactly this many epochs. |
| `boundaries` | object | `train_end` and `validation_end`, both timestamps as in `pix-payer-input-v2` §5.2. `train_end` MUST be earlier than `validation_end`, or `boundaries_out_of_order` is reported at `/boundaries/train_end`. They define the partitions of [`pix-training-example-v2` §11.1](pix-training-example-v2.md#111-partitions). |
| `selection` | object | See [3.2](#32-selection). |

### 3.1 Architecture

| Member | Type | Rules |
|---|---|---|
| `hidden_layers` | array of integers | 0 to 4 elements, each from 1 to 256 neurons, in order from the input. An empty array is a single sigmoid output over the features, a logistic regression. More than 4 elements is reported at the array; an invalid element at its index. |
| `activation` | closed enumeration | `sigmoid`, for every layer. |

The network always has exactly one output, the risk score. A larger network is not assumed to be better; the configuration only makes the architecture explicit.

### 3.2 Selection

| Member | Type | Rules |
|---|---|---|
| `checkpoint` | closed enumeration | `final_epoch`: the model is the network after the last epoch. No early stopping. |
| `threshold` | object | `policy`: closed enumeration, `fixed`. `value`: a number from 0 to 1, the decision threshold at which validation and test are measured. |

Each option has exactly one supported value in this version. Validation-based checkpoint selection, early stopping, and threshold selection are not accepted until a later configuration version implements them (#28), so no accepted option is ignored.

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
| `wrong_type`, `required`, `unknown_member`, `null_not_allowed`, `invalid_number_format`, `invalid_format`, `invalid_enum_value` | As in `pix-payer-input-v2`, for configuration members. An unsupported option, such as `best_validation`, is `invalid_enum_value`. |
| `out_of_range` | A number, an integer, the number of hidden layers, or a layer size outside its range. |
| `unsupported_config_version` | `config_version` is a string other than `pix-training-config-v1`. |
| `boundaries_out_of_order` | `train_end` is not earlier than `validation_end`. |

## 6. Examples

Every example in [`training-config-examples/`](training-config-examples/) is paired with a `.expected.json` file holding `{"status": "valid"}` or `{"status": "invalid", "issues": [...]}`.

| File | Case | Expected |
|---|---|---|
| `01-baseline.json` | One hidden layer of 4 neurons, 5,000 epochs, learning rate 1, threshold 0.5. | valid |
| `02-no-hidden-layer.json` | No hidden layer. | valid |
| `03-two-hidden-layers.json` | Hidden layers of 16 and 8 neurons. | valid |
| `04-invalid-unsupported-version.json` | `pix-training-config-v2`, with an unknown member. | only `unsupported_config_version` |
| `05-invalid-unknown-member.json` | `epochs` and `architecture.dropout`. | `unknown_member` twice |
| `06-invalid-unsupported-options.json` | `adam`, `best_validation`, and `max_f1`. | `invalid_enum_value` three times |
| `07-invalid-dimensions.json` | Layer sizes 0, 257, and 4.5. | `out_of_range` twice, `invalid_number_format` |
| `08-invalid-ranges.json` | Learning rate 0, 1,000,001 epochs, threshold 1.5. | `out_of_range` three times |
| `09-invalid-boundaries-order.json` | Equal boundaries. | `boundaries_out_of_order` |
| `10-invalid-schema-and-depth.json` | `transaction-features-v1` and five hidden layers. | `out_of_range`, `invalid_enum_value` |
