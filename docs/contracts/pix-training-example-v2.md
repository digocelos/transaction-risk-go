# Training contract `pix-training-example-v2`

**Status:** Normative for `pix-training-example-v2`. It replaces [`pix-training-example-v1`](pix-training-example-v1.md) (ADR 0013): the record, classification, evaluation protocol, fingerprint, and dataset report are unchanged, and records embed `pix-payer-input-v2` scoring inputs prepared for `pix-risk-v1`.

## 1. Scope

This specification defines the training dataset for payer-side Pix fraud models: the JSON Lines file a dataset owner supplies, how each record is validated and classified, how a dataset is fingerprinted and reported, and the temporal protocol for training and evaluating a model on it.

A training example pairs the exact scoring input that existed at decision time with outcome information observed later. The scoring input is defined by [`pix-payer-input-v2`](pix-payer-input-v2.md); this specification adds only training-only metadata around it (ADR 0009).

Out of scope:

- Implementing the decoder, the evaluation, or the training command.
- Selecting or describing a public dataset.
- The layout of trained model artifacts and of evaluation reports beyond the dataset report in [section 13](#13-dataset-report).
- Any claim that a model trained under this contract is fit for production.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as described in RFC 2119 and RFC 8174 when, and only when, they appear in all capitals.

## 2. Normative references

- [`pix-payer-input-v2`](pix-payer-input-v2.md), the scoring input contract, including its value types (§5), nullability rules (§6), validation (§11), and code catalog (§12).
- [`pix-risk-v1`](../features/pix-risk-v1.md), the feature schema whose preparation decides insufficiency.
- RFC 8259: The JavaScript Object Notation (JSON) Data Interchange Format.
- RFC 3339: Date and Time on the Internet: Timestamps.
- RFC 6901: JavaScript Object Notation (JSON) Pointer.
- FIPS 180-4: Secure Hash Standard, for SHA-256.
- Manual Operacional do DICT v8.5, 2026-09-01, for the contestation period of the Special Return Mechanism (MED).

## 3. Related decisions

| ADR | Decision |
|---|---|
| [0003](../adr/0003-separate-versions-for-contract-features-and-model-format.md) | Contract, feature schema, and model file format have separate versions. |
| [0004](../adr/0004-missing-observations-are-never-imputed.md) | Missing observations are never imputed. |
| [0005](../adr/0005-scoring-inputs-are-point-in-time.md) | Scoring inputs are point-in-time. |
| [0006](../adr/0006-strict-contract-versions.md) | Unknown fields are rejected and every change is a new version. |
| [0007](../adr/0007-no-personal-identifiers-in-scoring-inputs.md) | No personal identifiers in scoring inputs. |
| [0009](../adr/0009-training-metadata-stays-outside-the-scoring-input.md) | Training-only metadata stays outside the scoring input. |
| [0011](../adr/0011-optional-observations-carry-availability-indicators.md) | Optional observations carry explicit availability indicators. |
| [0013](../adr/0013-v2-contracts-replace-v1-without-coexistence.md) | Version 2 contracts replace version 1. |

Terms such as training example, outcome, label as-of time, label maturity, usable example, group key, and partition are defined in [`GLOSSARY.md`](../../GLOSSARY.md).

## 4. Dataset format

A training dataset is a JSON Lines file: a sequence of bytes split into lines by the line feed byte `0x0A` (LF).

1. **Lines.** Line 1 starts at the first byte. Each LF ends the current line and, if any bytes follow, starts the next one. When the file ends with LF, no empty line follows it. When the file does not end with LF, the bytes after the last LF form the last line. An empty file has no lines and is a valid dataset with zero records.
2. **Line numbers.** Lines are numbered from 1, in file order. Every line counts, including invalid and blank lines.
3. **One record per line.** Every line is one training example record ([section 5](#5-record)). The LF is not part of the record.
4. **Encoding.** Every line is valid UTF-8. Line 1 does not start with a byte order mark (`EF BB BF`).
5. **Line endings.** A line contains no carriage return byte (`0x0D`). Files with CRLF line endings are therefore invalid; convert them to LF.
6. **Blank lines.** A line that is empty, or that contains only spaces (`0x20`) and tabs (`0x09`), is invalid. Blank lines are reported, not skipped.
7. **Record size.** A line is at most 131,072 bytes, excluding its LF. A reader MUST stop buffering a longer line once it exceeds the limit, report it, and continue with the next line, so memory stays bounded.
8. **JSON.** Every line holds exactly one JSON value, and that value is an object. Only spaces and tabs may surround it on the line. No object in a record has two members with the same name, compared after unescaping, at any level, including inside the embedded scoring input. A string with an unpaired UTF-16 surrogate escape is invalid JSON.

The dataset as a whole has no size limit; readers process it one line at a time.

## 5. Record

A record is one JSON object with exactly these members. Every member MUST be present; an unknown member is invalid (ADR 0006).

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `training_example_version` | string | No | MUST be a supported version. The only supported version is `pix-training-example-v2`. |
| `scoring_input` | object | No | One `pix-payer-input-v2` scoring input ([section 6](#6-embedded-scoring-input)). |
| `outcome` | object | No | Training-only outcome information ([section 7](#7-outcome)). |
| `group_key` | string | Yes | Optional dataset-local opaque key for partition and leakage analysis ([section 8](#8-group-key)). `null` when the dataset owner supplies no key. |

`group_key` is optional in meaning but always present in form: a record without a key states `"group_key": null`. As in the scoring input contract, this keeps a misspelled member from being read silently as an absent one.

Value types are those of [`pix-payer-input-v2` §5](pix-payer-input-v2.md#5-value-types): integer (§5.1), timestamp (§5.2), and closed enumeration (§5.3), with the same lexical rules and the same codes.

Training-only metadata, meaning `outcome` and `group_key`, MUST NOT be passed to scoring, used as a model feature, or used to choose a feature value.

An example record, shown on several lines for readability; in a dataset it occupies exactly one line:

```json
{
  "training_example_version": "pix-training-example-v2",
  "scoring_input": { "contract_version": "pix-payer-input-v2", "decision_time": "2026-06-02T14:03:07.120-03:00", "...": "..." },
  "outcome": {
    "label": "fraud",
    "label_as_of": "2026-10-01T03:00:00Z",
    "loss_amount_cents": 120000
  },
  "group_key": "g-41d0b2"
}
```

## 6. Embedded scoring input

`scoring_input` holds the exact scoring input that existed at the payment's decision time.

- It is validated by `pix-payer-input-v2` exactly, using the exact bytes of the member's value as they appear in the record. Its 65,536-byte limit applies to those bytes.
- It MUST be the input as it existed at decision time. It MUST NOT be rebuilt, enriched, or corrected later with information that became available after `decision_time`, such as backfilled observations or later DICT statistics.
- Its issues are reported with the scoring input contract's codes, and with paths prefixed by `/scoring_input`. For example, `/pix_payment/amount_cents` becomes `/scoring_input/pix_payment/amount_cents`, and a whole-document issue at `""` becomes `/scoring_input`.
- Its preparation follows the feature schema being trained, [`pix-risk-v1`](../features/pix-risk-v1.md). Blocker paths are prefixed by `/scoring_input` in the same way.

## 7. Outcome

Path: `/outcome`. Exactly these members, all required:

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `label` | closed enumeration | No | `fraud` or `legitimate`. |
| `label_as_of` | timestamp | No | The instant up to which the dataset owner asserts that the outcome is known. MUST NOT be earlier than the scoring input's `decision_time`. |
| `loss_amount_cents` | integer | Yes | 0 to 2^53−1. The loss in centavos of BRL; `null` when the loss of a fraud is unknown. |

**Label semantics.**

- `fraud`: by `label_as_of`, the dataset owner has determined that the Pix payment was initiated as a result of fraud against the payer, for example through account takeover or social engineering. Each dataset owner documents the evidence behind its determination, such as an accepted infraction report or a confirmed internal investigation.
- `legitimate`: by `label_as_of`, no fraud determination exists for the payment.

A `legitimate` label is the absence of a fraud determination, so it becomes reliable only once enough time has passed for fraud to be reported ([section 9.2](#92-label-maturity)).

**`label_as_of` semantics.** `label_as_of` plays the role of `as_of` for the outcome: the instant at which the outcome was snapshotted, typically the time of the dataset export. It is not the time at which fraud was first reported. Datasets SHOULD use one `label_as_of` for every record of an export.

**Loss semantics.** `loss_amount_cents` is the part of the payment amount that the payer did not recover through any mechanism, such as MED refunds, as known at `label_as_of`.

- For `legitimate`, it MUST be `0`: no fraud means no fraud loss, so the value is known, not unknown.
- For `fraud`, it is an integer from 0 to the payment's `amount_cents`, or `null` when the loss is unknown. Unknown loss is never treated as zero.

## 8. Group key

`group_key` is an opaque string that groups records the dataset owner considers related, such as payments from the same payer account. It exists only to partition data and to measure leakage across partitions ([section 11.6](#116-group-overlap)).

- Syntax: `^[A-Za-z0-9_-]{1,64}$`, preserved exactly as received. A value that does not match is rejected with `invalid_format`.
- It is dataset-local: keys are compared only within one dataset and carry no meaning across datasets.
- It MUST NOT be a raw identifier: no CPF or CNPJ, Pix key, account number, document number, device identifier, or scoring correlation ID.
- It MUST NOT be reversible by anyone other than the dataset owner. A plain unkeyed hash of an identifier is reversible: for example, all CPF hashes can be enumerated. Owners SHOULD use random surrogate keys or a keyed hash whose secret never leaves the owner.
- It MUST NOT be used as a model feature, passed to scoring, or used to choose a threshold.

A group key is still pseudonymous data. When the dataset owner can link keys back to people, the key can be personal data under the LGPD, and the owner remains responsible for its governance. The toolkit treats the key as opaque and never tries to interpret it.

## 9. Record classification

Every record belongs to exactly one category. The categories are checked in this order, and the first that applies wins:

| Category | When | Used for training and evaluation |
|---|---|---|
| `invalid` | The record breaks this contract or the embedded scoring input contract ([section 10](#10-validation)). | No |
| `insufficient` | The record is valid, but preparation for the feature schema being trained returns `insufficient_input`: for `pix-risk-v1`, a required fact is missing or stale. | No |
| `immature` | The record is valid and sufficient, but its label is not mature ([9.2](#92-label-maturity)). | No |
| `usable` | None of the above. | Yes |

### 9.1 Invalid records fail the dataset

A dataset with at least one invalid record MUST NOT be used for training or evaluation. Training reports every invalid record and stops: an invalid record means the export itself is wrong, and dropping such records would bias the data silently. Insufficient and immature records are expected in valid datasets; they are excluded and counted, never dropped silently.

### 9.2 Label maturity

A label is mature when:

```text
label_as_of − decision_time ≥ 90 days
```

where 90 days is exactly 2,160 hours, compared as instants with no calendar or time zone. The maturity period is a constant of this contract version. It exceeds the 80-day period that Manual Operacional do DICT v8.5 gives for contesting a payment through MED, so most fraud reports for a payment exist before its label is used.

Maturity applies to both labels. It keeps fraud and legitimate examples equally old, so recent payments do not look legitimate merely because their fraud has not been reported yet.

## 10. Validation

### 10.1 Diagnostics

Validating a dataset produces diagnostics. Each diagnostic has:

- `line`: the line number ([section 4](#4-dataset-format));
- `path`: a JSON Pointer (RFC 6901) into the record, with `""` for the whole line;
- `code`: a code from the catalog in [10.3](#103-codes);
- `message`: an English message for humans. It is not part of the contract.

Diagnostics are sorted by `line`, then by the UTF-8 bytes of `path`, then by the UTF-8 bytes of `code`, with one diagnostic per line, path, and code.

### 10.2 Stages

Each line is validated independently, in four stages. Stages 1 and 2 stop the line's validation at their first issue; stages 3 and 4 collect every issue they can detect safely.

1. **Line.** Checked in this order: size, byte order mark (line 1 only), carriage return, UTF-8, blank line, JSON syntax and trailing data, top-level object. The first failure is the only diagnostic for the line. If those pass, every duplicate member is reported at its path, and the line stops when any duplicate exists.
2. **Version.** Locates `/training_example_version`. If it is absent, the code is `required`; if `null`, `null_not_allowed`; if not a string, `wrong_type`; if a string that is not a supported version, `unsupported_training_example_version`. On any of these, the line stops, without assuming the structure of `pix-training-example-v2`.
3. **Structure and values.** Applies [section 5](#5-record), [section 7](#7-outcome), and [section 8](#8-group-key): unknown members, required members, nulls, types, formats, ranges, and enumeration values. When `scoring_input` is an object, its exact bytes are validated by `pix-payer-input-v2`, and its issues are reported with the `/scoring_input` prefix ([section 6](#6-embedded-scoring-input)).
4. **Consistency.** Checks the rules below. A rule is checked only when every value it involves passed stage 3, and, for rules that read the scoring input, only when the whole embedded scoring input is valid.

| Rule | Code | Path |
|---|---|---|
| `label_as_of` ≥ `scoring_input.decision_time` | `label_before_decision_time` | `/outcome/label_as_of` |
| A `legitimate` label has `loss_amount_cents` equal to `0` | `loss_amount_mismatch` | `/outcome/loss_amount_cents` |
| A `fraud` label's known `loss_amount_cents` ≤ `scoring_input.pix_payment.amount_cents` | `loss_exceeds_amount` | `/outcome/loss_amount_cents` |

A record with any diagnostic is `invalid`.

### 10.3 Codes

The catalog is closed. Codes shared with [`pix-payer-input-v2` §12](pix-payer-input-v2.md#12-validation-codes) keep the same meaning there and here. They apply to the record and, with the `/scoring_input` prefix, to the embedded scoring input.

**Codes specific to this contract:**

| Code | Path | Condition | Stage |
|---|---|---|---|
| `record_too_large` | `""` | The line exceeds 131,072 bytes. | 1 |
| `carriage_return_not_allowed` | `""` | The line contains a carriage return byte. | 1 |
| `blank_line` | `""` | The line is empty or holds only spaces and tabs. | 1 |
| `unsupported_training_example_version` | `/training_example_version` | The version is a string that is not supported. | 2 |
| `label_before_decision_time` | `/outcome/label_as_of` | `label_as_of` is earlier than `decision_time`. | 4 |
| `loss_amount_mismatch` | `/outcome/loss_amount_cents` | A `legitimate` label has a loss other than `0`, including `null`. | 4 |
| `loss_exceeds_amount` | `/outcome/loss_amount_cents` | A `fraud` label's loss exceeds the payment amount. | 4 |

**Codes reused from `pix-payer-input-v2`:**

| Code | Use in records |
|---|---|
| `bom_not_allowed` | Line 1 starts with a byte order mark. |
| `invalid_utf8` | The line is not valid UTF-8. |
| `invalid_json` | The line is not one valid JSON value. |
| `trailing_data` | Something other than spaces and tabs follows the record's object on its line. |
| `wrong_type` | The line's value is not an object, or a member has the wrong JSON type. |
| `duplicate_member` | A member name appears twice in one object. |
| `required`, `unknown_member`, `null_not_allowed` | As in `pix-payer-input-v2`, for record and outcome members. |
| `invalid_number_format`, `out_of_range` | For `loss_amount_cents`. |
| `invalid_format` | For `label_as_of` and `group_key`. |
| `invalid_enum_value` | For `label`. |

Every code of `pix-payer-input-v2` can also appear with the `/scoring_input` prefix, from the embedded validation.

## 11. Temporal evaluation protocol

### 11.1 Partitions

A training run is configured with two boundaries, `train_end` and `validation_end`. Both are timestamps as in `pix-payer-input-v2` §5.2, compared as instants, and `train_end` MUST be earlier than `validation_end`. Each record that is not invalid belongs to exactly one partition, chosen by its scoring input's `decision_time`:

| Partition | Decision time |
|---|---|
| Train | `decision_time ≤ train_end` |
| Validation | `train_end < decision_time ≤ validation_end` |
| Test | `decision_time > validation_end` |

Upper bounds are inclusive, as in contract-defined lookback windows. Partitions MUST NOT be random, shuffled, or based on anything other than `decision_time` and the two boundaries. The run reads no system clock.

### 11.2 Roles

- **Train.** The model is fitted only on the usable records of the train partition. The train partition MUST contain at least one usable `fraud` record and at least one usable `legitimate` record.
- **Validation.** Used only to compare configurations and to choose the decision threshold. Its records never update model parameters.
- **Test.** Evaluated exactly once, after the model, its training configuration, and the threshold are fixed. Test results MUST NOT be used to change any of them; changing anything afterwards requires a new run reported as such.
- **No evaluation on training examples.** Metrics computed on the train partition MUST NOT be reported as evaluation results. A run MAY report training loss, labeled as training loss.

### 11.3 Threshold

A record is predicted `fraud` when `score ≥ threshold`. The threshold is a number from 0 to 1, fixed before test evaluation, either supplied in the run configuration or chosen from validation results only. The report records the threshold.

### 11.4 Class imbalance

- Training uses the natural class distribution of the usable train records. This protocol version applies no resampling, reweighting, or synthetic examples.
- Every partition reports its usable `fraud` and `legitimate` counts.
- Accuracy is not a protocol metric, because a model that predicts `legitimate` for everything scores high on accuracy when fraud is rare.

### 11.5 Metrics

Metrics are computed per evaluated partition over its usable records. Positives are `fraud` labels. `TP`, `FP`, `TN`, and `FN` are the confusion matrix counts at the threshold.

| Metric | Definition | Undefined when |
|---|---|---|
| `true_positives`, `false_positives`, `true_negatives`, `false_negatives` | Counts | Never |
| `precision` | `TP / (TP + FP)` | `TP + FP = 0` |
| `recall` | `TP / (TP + FN)` | `TP + FN = 0` |
| `f1` | `2·TP / (2·TP + FP + FN)` | `2·TP + FP + FN = 0` |
| `false_positive_rate` | `FP / (FP + TN)` | `FP + TN = 0` |
| `false_positives_per_1000_legitimate` | `1000 · FP / (FP + TN)` | `FP + TN = 0` |
| `pr_auc` | Average precision, defined below | No `fraud` records |
| `fraud_amount_cents_detected` | Sum of `amount_cents` over TP | Never |
| `fraud_amount_cents_missed` | Sum of `amount_cents` over FN | Never |
| `legitimate_amount_cents_flagged` | Sum of `amount_cents` over FP | Never |
| `known_loss_cents_detected` | Sum of known `loss_amount_cents` over TP | Never |
| `known_loss_cents_missed` | Sum of known `loss_amount_cents` over FN | Never |
| `unknown_loss_frauds_detected` | Count of TP with `null` loss | Never |
| `unknown_loss_frauds_missed` | Count of FN with `null` loss | Never |

An undefined metric is reported as `null`, never as NaN, infinity, or zero. Sums are exact integers; an implementation fails rather than report a sum above 2^53−1.

**Average precision (`pr_auc`).** Order records by score, highest first. Records with equal scores form one group and are never split. After including groups `1..k`, let `P_k = TP_k / (TP_k + FP_k)` and `R_k = TP_k / positives`. Then `pr_auc = Σ_k (R_k − R_{k−1}) · P_k`, with `R_0 = 0`. The result does not depend on the order of records within a group.

### 11.6 Group overlap

Group keys never become features, but they reveal leakage: the same payer account in train and test lets a model memorize it. For each run, the report states:

- the number of distinct group keys that appear among usable records of more than one partition;
- the number of usable validation and test records whose group key also appears among usable train records.

Records whose `group_key` is `null` are not counted. This protocol version discloses overlap and does not remove records because of it.

### 11.7 Leakage rules

1. Features come only from the embedded scoring input, which is point-in-time (ADR 0005) and never rebuilt after decision time ([section 6](#6-embedded-scoring-input)).
2. Outcome and group key never reach features, scoring, or threshold choice beyond what [11.3](#113-threshold) allows.
3. Partitions follow [11.1](#111-partitions) only, and each record belongs to one partition.
4. Immature labels are excluded from every partition.
5. Test is evaluated once, and training examples are never evaluated as if they were unseen.
6. Group overlap is measured and reported.
7. Every result is tied to the exact dataset bytes through the fingerprint ([section 12](#12-dataset-fingerprint)).

## 12. Dataset fingerprint

The dataset fingerprint is the SHA-256 digest (FIPS 180-4) of the exact bytes of the dataset file, from the first byte to the last, including invalid lines and the final LF if present. It is written as 64 lowercase hexadecimal digits, together with the byte count. No normalization is applied, so the same bytes always produce the same fingerprint, and any change produces a different one.

## 13. Dataset report

Validation and classification of a dataset produce a dataset report: a JSON object that records every exclusion instead of dropping records silently. Evaluation reports embed it.

| Member | Type | Meaning |
|---|---|---|
| `dataset_report_version` | string | `pix-training-dataset-report-v1`. |
| `training_example_version` | string | `pix-training-example-v2`. |
| `feature_schema` | string | The feature schema used to decide insufficiency, such as `pix-risk-v1`. |
| `label_maturity_days` | integer | `90`. |
| `dataset_sha256` | string | The fingerprint. |
| `dataset_bytes` | integer | The size of the dataset in bytes. |
| `records` | object | `total`, `usable`, `invalid`, `insufficient`, and `immature` counts. `total` equals the number of lines, and the four categories add up to it. |
| `usable_labels` | object | `fraud` and `legitimate` counts among usable records. |
| `exclusions` | array | One entry per non-usable record, sorted by line. |

Each exclusion entry has:

- `line`;
- `category`: `invalid`, `insufficient`, or `immature`;
- `issues`: present only for `invalid`. A list of `{path, code}`, in diagnostic order, without messages;
- `blockers`: present only for `insufficient`. A list of `{path, reason}`, prefixed by `/scoring_input`, in path order.

An evaluation report built on this dataset report MUST also record the partition boundaries, the per-partition counts of usable `fraud` and `legitimate`, insufficient, and immature records, the group overlap counts, the threshold, and the metrics of [11.5](#115-metrics) for the validation and test partitions. Its layout belongs to the model provenance work.

## 14. Examples

Every example in [`training-examples-v2/`](training-examples-v2/) is a complete dataset, paired with a `.expected.json` file holding its expected dataset report for `pix-risk-v1`. All examples use `label_as_of` `2026-10-01T03:00:00Z` and embed scoring inputs derived from [`pix-payer-input-v2` example 01](examples-v2/01-complete.json), moved to their decision dates. The expected reports were computed by an implementation independent of the toolkit.

| File | Case | Category |
|---|---|---|
| `01-usable-legitimate.jsonl` | A mature `legitimate` label with loss `0`, a registered device, and a recipient that is not pre-registered. | `usable` |
| `02-usable-fraud.jsonl` | A mature `fraud` label with a known loss of 120,000 on a 180,000 payment, from an unregistered device with no settled payments. | `usable` |
| `03-fraud-registered-device-pre-registered-recipient.jsonl` | A `fraud` label on a payment from a registered device to a pre-registered recipient. | `usable` |
| `04-legitimate-registered-device-pre-registered-recipient.jsonl` | A `legitimate` label on a payment from a registered device to a pre-registered recipient. | `usable` |
| `05-unknown-loss-no-pix-key.jsonl` | A `fraud` label with loss `null`, no group key, no Pix key, and no recipient account entry. | `usable` |
| `06-legitimate-no-device-no-period-limit.jsonl` | A `legitimate` label on a payment without a payer device or a limit per period. | `usable` |
| `07-invalid-label.jsonl` | Label `chargeback`. | `invalid`: `invalid_enum_value` at `/outcome/label` |
| `08-invalid-scoring-input.jsonl` | The embedded `reference_time_offset` is `-00:00`. | `invalid`: `invalid_format` at `/scoring_input/pix_payment/reference_time_offset` |
| `09-insufficient-stale-recent-count.jsonl` | The embedded 24-hour count is 6 minutes old. | `insufficient`: `stale` at `/scoring_input/observations/payer_account/initiated_outgoing_pix_24h/as_of` |
| `10-immature-label.jsonl` | Decision time about 41 days before `label_as_of`. | `immature` |
| `dataset.jsonl` | The ten records above, in order, on lines 1 to 10. | 6 usable, 2 invalid, 1 insufficient, 1 immature |

Because `dataset.jsonl` contains invalid records, it is not usable for training ([9.1](#91-invalid-records-fail-the-dataset)). It exists to show classification, line numbers, and the report.

## 15. Privacy

Training datasets carry no personal data beyond what this contract allows:

- the embedded scoring input follows ADR 0007, so it holds no personal identifiers;
- the outcome holds only a label, a timestamp, and an amount;
- the group key is the only pseudonymous value, under the rules of [section 8](#8-group-key).

Raw personal data, Pix keys, account numbers, document numbers, reversible identifiers, and scoring correlation IDs MUST NOT appear anywhere in a record. Dataset owners remain responsible for the lawful basis, retention, and access control of their datasets.
