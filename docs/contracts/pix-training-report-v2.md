# Training report `pix-training-report-v2`

**Status:** Normative for `pix-training-report-v2`. It replaces `pix-training-report-v1`, which only `cmd/train` wrote before model selection existed.

## 1. Scope

The training report is the JSON document `cmd/train` writes next to a trained model. It records what produced the model and how it was chosen: the configuration and its identity, the dataset report, the partitions, the coverage of populations and availability states, the model selection, and the validation and test metrics. The model artifact binds the report by the SHA-256 of its exact bytes ([`pix-risk-model-v1`](pix-risk-model-v1.md)), and both are published together in a run (ADR 0016).

The report describes software behavior on the data supplied. Its metrics are not a claim of detection quality, calibration, or regulatory compliance.

## 2. Encoding

The report is one JSON object, indented with two spaces and followed by a newline. The same inputs produce the same bytes: member order is fixed, map keys are sorted, timestamps are written in UTC, numbers use the shortest representation that round-trips, and no value comes from the system clock. Undefined metrics are `null`, never NaN, infinity, or zero.

## 3. Members

| Member | Type | Meaning |
|---|---|---|
| `training_report_version` | string | `pix-training-report-v2`. |
| `configuration` | object | `config_sha256`, the configuration's identity, and `config`, its canonical form ([`pix-training-config-v2` §6](pix-training-config-v2.md#6-canonical-form-and-identity)). |
| `dataset` | object | The dataset report of [`pix-training-example-v2` §13](pix-training-example-v2.md#13-dataset-report). |
| `partitions` | object | `train_end`, `validation_end`, the per-partition counts of usable `fraud` and `legitimate`, insufficient, and immature records, and the group overlap ([`pix-training-example-v2` §11](pix-training-example-v2.md#11-temporal-evaluation-protocol)). |
| `coverage` | object | See [section 4](#4-coverage). |
| `selection` | object | See [section 5](#5-selection). |
| `validation` | object | The metrics of [`pix-training-example-v2` §11.5](pix-training-example-v2.md#115-metrics) for the validation partition, at the selected threshold. |
| `test_evaluated` | boolean | Whether the configuration asked for the test evaluation. |
| `test` | object or `null` | The test metrics at the selected threshold when `test_evaluated` is `true`; otherwise `null`, and the test partition was never scored. |

## 4. Coverage

`coverage` has `train`, `validation`, and `test`, each counting the usable records of that partition, and `not_exercised_in_train`. Coverage shows what the model saw; it does not measure detection quality for any group.

| Member | Meaning |
|---|---|
| `usable_records` | The number of usable records. |
| `populations` | For each supported population of [`pix-risk-v1` §14](../features/pix-risk-v1.md#14-supported-populations), the number of records in it: `new_payer_account`, `no_payer_device`, `no_period_limit`, `no_pix_key`, and `no_recipient_account_entry`. A record can be in several. |
| `limit_periods` | The number of records with each `payer_limit_period`: `daytime`, `nighttime`, `daily`, and `no_period_limit`. |
| `sources` | For each optional source, the number of records in each availability state the source can take: `known`, `missing`, `stale`, `not_applicable` for sources a discriminator can rule out, and `zero_limit` for `period_limit`. A member or window missing inside a known DICT group leaves the group `known`. |

The optional sources are `period_limit`, `payer_device_history`, `payer_device_registration`, `recipient_pre_registration`, `recipient_account_entry`, `recipient_pix_key_entry`, and the DICT groups `person_spi`, `person_fraud_markers`, `person_infraction_reports`, `person_entries`, `key_spi`, `key_fraud_markers`, `key_infraction_reports`, and `key_entries`.

`not_exercised_in_train` lists every `{source, state}` that no usable train record has, in source order and then in the state order above. The model never saw those states during training.

## 5. Selection

| Member | Type | Meaning |
|---|---|---|
| `requested_epochs` | integer | The configured `max_epochs`. |
| `actual_epochs` | integer | The epochs trained, at most the requested ones. |
| `selected_epoch` | integer | The number of epochs that produced the saved network: the last epoch trained for `final_epoch`, or the epoch of the best validation evaluation. |
| `stop_reason` | string | `max_epochs`, or `early_stopping` when patience ended the run. |
| `checkpoint_policy` | string | `final_epoch` or `best_validation`. |
| `objective` | string or `null` | The validation objective of `best_validation`; `null` for `final_epoch`. |
| `evaluations` | integer | The number of validation evaluations; 0 for `final_epoch`. |
| `selected_objective_value` | number or `null` | The objective value of the selected epoch; `null` for `final_epoch`. |
| `threshold` | object | `policy`, `value`, the decision threshold used for every metric, and, for `max_recall_at_max_false_positive_rate`, the configured `max_false_positive_rate` and the number of `candidates`; both are `null` for a `fixed` threshold. |

A run that cannot select produces no report: a validation partition that does not meet the requirements of [`pix-training-config-v2` §3.2](pix-training-config-v2.md#32-selection), or a threshold policy that no candidate satisfies, stops the run before anything is written.

## 6. Consistency

`Marshal` refuses a report that contradicts its configuration: a dataset with invalid records or another feature schema, other boundaries, epochs outside `1 ≤ selected ≤ actual ≤ requested`, a `max_epochs` stop that did not train every requested epoch, early stopping without the `patience` policy, a checkpoint or threshold that is not the configured one, metrics at another threshold, or test metrics that do not follow `test_evaluation`.
