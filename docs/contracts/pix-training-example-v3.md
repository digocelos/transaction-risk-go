# Training contract `pix-training-example-v3`

**Status:** Normative for `pix-training-example-v3`. It replaces [`pix-training-example-v2`](pix-training-example-v2.md), which the toolkit no longer accepts (ADR 0013, ADR 0024): records embed [`pix-payer-input-v3`](pix-payer-input-v3.md) scoring inputs and are prepared for [`pix-risk-v3`](../features/pix-risk-v3.md); the record, classification, evaluation protocol, fingerprint, and dataset report are otherwise those of version 2. This document states the differences; everything it does not mention is as `pix-training-example-v2` specifies it, read with the version 3 names.

## 1. Record

A record is as in [`pix-training-example-v2` §5](pix-training-example-v2.md#5-record), with:

| Member | Rules |
|---|---|
| `training_example_version` | MUST be `pix-training-example-v3`. Any other value, including `pix-training-example-v2`, is rejected with `unsupported_training_example_version`. |
| `scoring_input` | One `pix-payer-input-v3` scoring input, validated exactly as [`pix-payer-input-v3`](pix-payer-input-v3.md) requires, with issue paths prefixed by `/scoring_input`. A `pix-payer-input-v2` input is rejected with `unsupported_contract_version` at `/scoring_input/contract_version`. |

## 2. Classification and preparation

Insufficiency is decided by `pix-risk-v3` preparation ([`pix-training-example-v2` §9](pix-training-example-v2.md#9-record-classification)). The dataset report's `feature_schema` is `pix-risk-v3` and its `training_example_version` is `pix-training-example-v3`.

## 3. Examples

[`training-examples-v3/`](training-examples-v3/) holds the datasets of version 2 with `amount_cents` added to every 24-hour history and the version strings updated, each paired with its expected dataset report. Their fingerprints and byte counts changed with the content.

## 4. Migration from version 2

Convert each record's scoring input as [`pix-payer-input-v3` §8](pix-payer-input-v3.md#8-migration-from-version-2) describes and set `training_example_version` to `pix-training-example-v3`. The dataset fingerprint changes, so a run on the converted dataset is a new run.
