# Input contract `pix-payer-input-v3`

**Status:** Normative for `pix-payer-input-v3`. It replaces [`pix-payer-input-v2`](pix-payer-input-v2.md), which the toolkit no longer accepts (ADR 0013, ADR 0024): version 3 adds one required member to the 24-hour initiated history, the amount initiated, and changes nothing else. This document states the differences; everything it does not mention is as `pix-payer-input-v2` specifies it, read with `pix-payer-input-v3` in place of `pix-payer-input-v2`.

## 1. Scope

The scoring input an integrator supplies for a payer-side Pix payment, as [`pix-payer-input-v2` §1](pix-payer-input-v2.md#1-scope) defines it. The feature schema that reads it is [`pix-risk-v3`](../features/pix-risk-v3.md), and the training example that embeds it is [`pix-training-example-v3`](pix-training-example-v3.md).

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as described in RFC 2119 and RFC 8174 when, and only when, they appear in all capitals.

## 2. Version

`contract_version` MUST be `pix-payer-input-v3`. Any other value, including `pix-payer-input-v2` and `pix-payer-input-v1`, is rejected with `unsupported_contract_version` at `/contract_version`, before any other member is read ([`pix-payer-input-v2` §11.1](pix-payer-input-v2.md#111-stages)).

## 3. `initiated_outgoing_pix_24h`

The observation `observations.payer_account.initiated_outgoing_pix_24h` ([`pix-payer-input-v2` §9.1](pix-payer-input-v2.md#91-payer_account)) has exactly these members:

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `count` | integer | No | 0 to 2^53−1. As in version 2: initiated Pix payments from the payer account in the lookback window, whatever their outcome, with technical retries, retransmissions, and reprocessing of one instruction counted once. |
| `amount_cents` | integer | No | 0 to 2^53−1. The sum, in centavos of BRL, of the amounts of the payments that `count` counts: the same window, the same deduplication, each instruction's full amount once. It MUST be 0 if and only if `count` is 0. |
| `as_of` | timestamp | No | As in version 2. |

Lookback window: 24 hours. The amount excludes the payment being scored, as the count does. An integrator that cannot sum the amounts cannot supply this version: the observation is a required fact, and `null` makes the input insufficient as in version 2.

## 4. Consistency rules

The rules of [`pix-payer-input-v2` §10](pix-payer-input-v2.md#10-consistency-rules) apply, plus:

| Rule | Code | Path of the issue |
|---|---|---|
| In `payer_account.initiated_outgoing_pix_24h`, `amount_cents` is 0 if and only if `count` is 0. | `amount_count_mismatch` | `amount_cents` |

Like the median rule, this is a structural consistency rule: a window with no payments has no amount, and a window with payments has one. No ordering between `amount_cents` and the payment's own `amount_cents` is validated.

## 5. Validation codes

The catalog of [`pix-payer-input-v2` §11.3](pix-payer-input-v2.md#113-codes) plus `amount_count_mismatch`, with the condition above. `unsupported_contract_version` now covers `pix-payer-input-v2`.

## 6. Preparation

Preparation follows [`pix-risk-v3`](../features/pix-risk-v3.md), which reads `amount_cents` for two features and produces the note `no_history` at `/observations/payer_account/initiated_outgoing_pix_24h/amount_cents` when `count` is 0. The notes, blockers, and model notes are as `pix-risk-v3` §13 defines them.

## 7. Examples

[`examples-v3/`](examples-v3/) holds the examples of version 2 with `amount_cents` added to every 24-hour history, 25,000 centavos per counted payment, their `contract_version` set to `pix-payer-input-v3`, and one new example:

| File | Case | Expected |
|---|---|---|
| `01` to `20` | As [`pix-payer-input-v2` §16](pix-payer-input-v2.md#16-examples) and [`pix-risk-v3` §16](../features/pix-risk-v3.md#16-examples), with features 62 to 64 in every prepared result. | As before. |
| `21-invalid-amount-count-mismatch.json` | `initiated_outgoing_pix_24h` is `{count 0, amount_cents 50000}`. | `amount_count_mismatch` at `/observations/payer_account/initiated_outgoing_pix_24h/amount_cents`. |

Each example is paired with a `.expected.json` file holding its validation or preparation result, which the tests of `pixinput`, `pixrisk`, `scoring`, `cmd/predict`, and `internal/e2e` check.

## 8. Migration from version 2

- Add `amount_cents` to `initiated_outgoing_pix_24h`, summed over the same deduplicated instructions the count counts, and set `contract_version` to `pix-payer-input-v3`.
- Training records move to [`pix-training-example-v3`](pix-training-example-v3.md) and CSV files to [`pix-training-csv-v2`](pix-training-csv-v2.md), which adds the column `observations.payer_account.initiated_outgoing_pix_24h.amount_cents`.
- Models are retrained: an artifact whose schema is `pix-risk-v2` is rejected on load.
