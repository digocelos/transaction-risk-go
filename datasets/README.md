# Scenario datasets

Two synthetic, scenario-driven training datasets in the [`pix-training-csv-v1`](../docs/contracts/pix-training-csv-v1.md) layout. Each file is an artifact with its own name: [`pix-training-scenarios-v1.csv`](pix-training-scenarios-v1.csv), the first one, and [`pix-training-scenarios-v2.csv`](pix-training-scenarios-v2.csv), which holds every v1 row and adds the scenarios the [inspection](../docs/contracts/pix-model-inspection-v1.md) of a v1 model showed to be missing. Both are for exercising the pipeline, not for certifying a model; [`docs/readiness.md`](../docs/readiness.md) applies to any metric computed on them.

- [`pix-training-scenarios-v2`](#scenario-dataset-pix-training-scenarios-v2): 8,757 rows, 787 frauds in 81 campaigns across 16 fraud archetypes, and the hard negatives that mirror them.
- [`pix-training-scenarios-v1`](#scenario-dataset-pix-training-scenarios-v1): 8,000 rows, 480 frauds in 48 campaigns across 12 fraud archetypes.

## Scenario dataset `pix-training-scenarios-v2`

`pix-training-scenarios-v2.csv` is `pix-training-scenarios-v1.csv` plus 757 rows: four fraud archetypes, their hard negatives, and a share of unavailable DICT observations that is higher for frauds than for legitimate payments. The v1 rows are unchanged, so the [v1 catalog](#scenario-catalog) still describes them; this section describes what v2 adds and what it changed in a trained model.

### Why

The [inspection](../docs/contracts/pix-model-inspection-v1.md) of the model trained on v1 with the [example configuration](#training-on-it) found shortcuts the dataset made true: a Pix key linked to two accounts in twelve months was a signature of legitimate recipients who moved banks and never of a fraud, so forcing `key_distinct_accounts_m12` to 3 dropped the share of flagged frauds from 96 % to 79 %, and to 2 % at 50; no fraud paid by typed account data without a key was a scam of the main archetypes; no campaign split a transfer into many small payments, so `initiated_count_24h` reached 16 at most; twelve-month fraud markers reached 12 at most; and unavailable observations were drawn independently of the label, so the model could learn nothing about them. v2 adds rows for each of these, so a model trained on it can be inspected for the same questions.

### Shape

| Property | Value |
|---|---|
| Rows | 8,757: 787 `fraud` (9.0 %) and 7,970 `legitimate` |
| Decision times | 2025-04-01 to 2026-06-30, written with the offset `-03:00` |
| `label_as_of` | `2026-10-01T00:00:00.000-03:00` for every record; every label is mature |
| Classification | 8,715 usable (785 `fraud`, 7,930 `legitimate`), 42 insufficient, 0 immature, 0 invalid |
| Losses | Legitimate: `0`. Fraud: the full amount for 653, a partial loss for 94, unknown for 40 |
| Group keys | 81 campaigns: the 48 of v1 and 33 new ones, `c049` to `c081`; empty for legitimate payments |

### Added scenarios

Fraud, 307 rows in 33 campaigns:

| Id | Scenario | Rows | Campaigns | Signature |
|---|---|---:|---:|---|
| F13 | Ported mule key: a mule buys an account whose key was linked elsewhere before | 140 | 16 | `distinct_accounts.m12` of 2 to 6 (most campaigns 3 or more), key ownership 400 to 2,500 days old, link 1 to 14 days old, account opened within 30 days of the link; R$500 to R$5,000; first payment; the marker progression of a cloned-WhatsApp, fake-store, fake-rental, or fake-call-center campaign. |
| F14 | Keyless scam: a fake call center or cloned WhatsApp paid by typed account data | 68 | 7 | `no_pix_key`; the recipient person's statistics as in the mirrored campaign, the recipient account entry present for half of the rows; the amounts and timing of F1 and F2. |
| F15 | Split payments: a ghost hand or takeover drains an account in small payments | 63 | 6 | 8 to 12 payments of R$50 to R$200 from one payer within a few hours, `initiated_outgoing_pix_24h.count` rising from 3 to 8 up to about 60, the period limit's consumption growing with each payment; the device and recipient of F5 or F6. |
| F16 | Heavily marked mule: a long-running mule or aggregator | 36 | 4 | `mule_accounts` 20 to 500 in `d90` and 20 to 200 in `m12`, 15 to 100 distinct reporters in `m12`, 5 to 40 open reports, settlements in the thousands, 3 to 8 registered accounts, a key linked to 3 or 4 accounts; the amounts and payers of F11. |
| F17 | Correlated missingness | — | — | Applied to F13 to F16: 15 % of their rows have one DICT group of the person or the key unavailable, and 5 % every group, against 3 % of the added legitimate rows with one group unavailable. In v1, unavailability was independent of the label. |

Legitimate, 450 rows, as hard negatives:

| Id | Scenario | Rows | Signature |
|---|---|---:|---|
| L13 | Movers | 150 | Recipients whose key is linked to a second account in twelve months, 15 of them to a third, with the key owned for years: more of L1 and L3, so a ported key alone stays ambiguous. |
| L14 | Keyless legitimate payments | 120 | More of L7: Pix Automático bills and corporate payments by account data, companies and persons. |
| L15 | Frequent small payments | 120 | A known recipient, R$20 to R$200, with 10 to 40 payments initiated in 24 hours: merchants paid many times a day and bill days. Mirrors F15. |
| L16 | Cleared aggregators | 60 | Companies settling thousands of payments in 90 days, no fraud marker, 1 to 3 rejected reports in twelve months and at most one open. Mirrors F16. |

Every added row is a v1 row of the mirrored scenario, moved to a new decision time with every timestamp shifted by the same amount, so ages, staleness, and the point-in-time rule hold, and then changed in the columns the signature names. The added fraud campaigns are placed so that each archetype has campaigns in the train, validation, and test partitions of the example configuration.

### By partition

With the boundaries of the [example configuration](#training-on-it):

| Partition | Decision times | Usable fraud | Usable legitimate |
|---|---|---:|---:|
| Train | up to 2026-01-31 | 447 | 4,832 |
| Validation | 2026-02-01 to 2026-04-30 | 181 | 1,757 |
| Test | from 2026-05-01 | 157 | 1,341 |

Four campaigns cross the train boundary; the training report shows 4 shared group keys and 5 records sharing a train key.

### What changed in a trained model

The example configuration, trained on v2 and inspected with `cmd/inspect`, against the same configuration trained on v1 (validation partition, partial dependence with every other feature as the record has it):

| Question | v1 model | v2 model |
|---|---|---|
| Mean fraud score, share of frauds flagged, with `key_distinct_accounts_m12` forced to 1 / 2 / 3 / 5 | 0.913, 0.814, 0.680, 0.481; 96 %, 89 %, 79 %, 58 % | 0.895, 0.881, 0.866, 0.836; 98 %, 98 %, 95 %, 94 % |
| Train range of `key_distinct_accounts_m12`, `initiated_count_24h`, `person_fraud_markers_m12` | 0 to 3, 0 to 16, 0 to 12 | 0 to 6, 0 to 66, 0 to 153 |
| Moves against the expected direction inside the train range, `initiated_count_24h` | 0 of 4 | 0 of 4 |
| Detected validation and test frauds still flagged after their amount is split 10 ways and the 24-hour count raised accordingly | 94 % (165 of 175) | 86 % (283 of 329) |
| Validation frauds falling below the threshold when the key entry is masked | 14 of 110 | 14 of 181 |
| Test precision, recall, PR-AUC at the selected threshold | 0.773, 0.883, 0.936 | 0.873, 0.962, 0.971 |

The score still falls as `key_distinct_accounts_m12` grows, by 0.06 from 1 to 5 instead of 0.43, because v2 keeps the 1,174 legitimate movers of v1 at 2 and adds 150 more, against about 130 frauds at 2 or more: in this data, a key on a second account remains mostly legitimate, and a model trained with the mean squared error learns that contrast more strongly than the rarer frauds at 3 to 6. The clipping of [`pix-risk-v2`](../docs/contracts/pix-risk-v2.md) bounds the extrapolation beyond the train range; the direction inside it is a property of the data. A [`pix-training-config-v3`](../docs/contracts/pix-training-config-v3.md) run with the cross-entropy, scaled initialization, and availability dropout on v2 cuts the direction violations from 74 to 29, removes every violation of `initiated_count_24h` and `person_fraud_markers_m12`, and keeps 88 % of the detected frauds flagged after a 10-way split, but does not change this one. The split test is not part of the inspection report; it was measured with a scratch program that divides each detected fraud's amount by 10, scales its ratio to the median and its limit utilization accordingly, and adds 9 to its 24-hour count.

### Converting and training

```bash
mkdir -p data/runs
go run ./cmd/convert -input datasets/pix-training-scenarios-v2.csv -output data/scenarios-v2.jsonl
go run ./cmd/train -config data/scenarios-config.json -dataset data/scenarios-v2.jsonl -run-dir data/runs/scenarios-v2
go run ./cmd/promote -run data/runs/scenarios-v2 -output data/scenarios-v2-model.json
go run ./cmd/inspect -run data/runs/scenarios-v2 -dataset data/scenarios-v2.jsonl -output data/scenarios-v2-inspection.json
go run ./cmd/evaluate -model data/scenarios-v2-model.json -dataset data/scenarios-v2.jsonl -partition test -output data/scenarios-v2-evaluation.json
```

`convert` reports 8,757 rows: 8,715 usable (785 fraud, 7,930 legitimate), 42 insufficient, and 0 immature. `data/scenarios-config.json` is the [example configuration](#training-on-it) of v1, whose boundaries fit v2 as well.

### How v2 was made

A standalone Python program with a fixed seed, 53, read the v1 file and wrote v2. It is not versioned: the CSV file is the artifact. `cmd/convert` validated its output, and every problem was fixed in the program until `convert` reported none. `TestScenarioDataset` in [`internal/e2e`](../internal/e2e/dataset_test.go) converts both files with the `convert` command built from the checkout and checks their summaries.

### Caveats

Those of v1, and: the added rows are mutations of v1 rows, so a payer, a device, or a recipient of v1 reappears with a shifted history; the new frauds are as separable by design as the old ones; and the fraud share, 9 %, is further from a real rate than v1's 6 %.

## Scenario dataset `pix-training-scenarios-v1`

[`pix-training-scenarios-v1.csv`](pix-training-scenarios-v1.csv) is a synthetic, scenario-driven training dataset for payer-side Pix fraud models, in the [`pix-training-csv-v1`](../docs/contracts/pix-training-csv-v1.md) layout. Each row is one Pix payment, with the scoring input as it existed at decision time and the outcome observed later. The rows come from named fraud and legitimate scenarios played out over 15 months, so every record has a story you can read in its values.

## What it is and is not

- **Synthetic.** No row comes from a real payment. It holds no CPF, CNPJ, Pix key, account number, name, or device identifier. Group keys are campaign surrogates such as `c001_fake_call_center`.
- **Scenario-driven.** Every fraud belongs to one of 12 archetypes and every legitimate payment to one of 12 others, each with a designed signature ([catalog](#scenario-catalog)). Several legitimate scenarios exist only to mirror a fraud signature, as hard negatives.
- **Oversampled.** 6% of the rows are frauds. Real Pix fraud rates are orders of magnitude lower, so precision and alert counts measured on this file overstate what the same model would show at real prevalence.
- **For exercising the pipeline, not for certifying a model.** It trains, selects, packages, and evaluates a model with realistic structure: campaigns, point-in-time DICT statistics, hard negatives, missing and stale observations, and unknown losses. Because the scenarios are designed, a model separates most of them easily; that says nothing about real detection quality. [`docs/readiness.md`](../docs/readiness.md) applies: no metric computed on this file is evidence that a model detects fraud.

## Shape

| Property | Value |
|---|---|
| Rows | 8,000: 480 `fraud` (6.0%) and 7,520 `legitimate` |
| Decision times | 2025-04-01 to 2026-06-30, written with the offset `-03:00`; volume grows over the period, with fewer payments at weekends |
| `label_as_of` | `2026-10-01T00:00:00.000-03:00` for every record, one export ([`pix-training-example-v2` §7](../docs/contracts/pix-training-example-v2.md#7-outcome)). The shortest gap to a decision time is 92 days, so every label is mature (§9.2). |
| Classification | 7,960 usable (478 `fraud`, 7,482 `legitimate`), 40 insufficient, 0 immature, 0 invalid |
| Losses | Legitimate: `0`. Fraud: the full amount for most, a partial loss after a MED refund for 63, and unknown (empty) for 23 |
| Group keys | One per fraud campaign, 48 in all; empty for legitimate payments, which have no campaign |
| Payers | 1,500 payer accounts reused across rows, each with a stable history that drifts slowly: 1,180 established natural persons, 70 companies, and 250 accounts opened during the period or just before it |

The 40 insufficient rows are spread across both labels: 20 have a 24-hour count older than its 5-minute maximum age, 10 a payment history older than 24 hours, and 10 no count of earlier payments to the recipient. `train` excludes and counts them, as it would in a real export.

## Scenario catalog

### Fraud (480 rows, 48 campaigns)

| Id | Scenario | Rows | Campaigns | Signature |
|---|---|---:|---:|---|
| F1 | Fake call center: a fake bank employee talks the victim into a transfer | 90 | 8 | 3 to 15 times the payer's median, from R$1,500 up to what the daytime limit allows; first payment to the recipient; a natural person whose EVP key is under 30 days old and account under 90 days old; registered device; weekday business hours. |
| F2 | Cloned WhatsApp: a "relative" with a new number asks for money | 70 | 7 | R$500 to R$3,000; a natural person with a phone or CPF key owned for days; half the accounts are years old, rented from their owners; first payment; late afternoon and evening, so some fall under the night limit. A few arrive without a key, from typed account data. |
| F3 | Fake store or auction | 60 | 6 | R$200 to R$5,000; a company with a weeks-old CNPJ key, or a natural person; an account under four months old; long campaigns, so scammer markers appear midway. |
| F4 | Fake investment | 35 | 3 | R$1,000 to R$20,000; a recipient that receives from hundreds of payers, so its settlement counts are high; victims report late, so markers rise only late in the campaign; some victims pay more than once. |
| F5 | Ghost hand: remote access to the victim's own device | 30 | 3 | Registered device; night or off-hours; the amount takes 90% to 100% of what the limit has left; 3 to 10 payments initiated in the last 24 hours. |
| F6 | Account takeover on a new device | 35 | 4 | An unregistered device with no history, so at most R$200 per payment and R$1,000 per night; several payments in a row; a mule already carrying open infraction reports. Two rows are companies whose API credentials were stolen: no device, no period limit, larger amounts. |
| F7 | Night coercion | 25 | 3 | 20h to 6h, night limit period; the amount is at or just under the night limit, R$1,000 by default; a fresh EVP key; first payment. |
| F8 | Tampered QR code | 30 | 3 | Purchase-like amounts, R$80 to R$2,000; an EVP key under a week old where a merchant was expected; campaigns of a few days, so victims almost never see markers. |
| F9 | Fake rental | 25 | 3 | R$800 to R$4,000; an account months or years old; campaigns that run into a second month, so later victims see reporters of the month before in `m12`. |
| F10 | First-party fraud: the payer stages the fraud | 20 | 3 | Built to look legitimate: a known recipient or an established merchant, an ordinary amount, business hours, registered device, clean recipient. Models are expected to miss these. |
| F11 | Mature mule reused | 40 | 3 | A mule carrying markers from earlier campaigns: `mule_accounts` 2 to 7 in `d90` and 7 to 12 in `m12`, 6 to 10 distinct reporters in `m12`, 1 to 7 open infraction reports. The easy cases. |
| F12 | Fake job with an upfront fee | 20 | 2 | R$50 to R$500; a fresh key; an account weeks old; small losses, and only 40% of victims report. |

### Legitimate (7,520 rows)

| Id | Scenario | Rows | Signature |
|---|---|---:|---|
| L1 | Recurring known recipient | 1,800 | At least 5 earlier payments in 365 days; amount within ±50% of the payer's median; clean recipient. Some recipients move to a new bank during the period: a new account and key link, with the key's old ownership. |
| L2 | Established merchant | 1,500 | A company with an account and a key years old, thousands of settlements, no markers; 40% are the payer's first purchase there. Large merchants carry a few rejected or open disputes. |
| L3 | Family and friends | 1,200 | Natural persons with CPF or phone keys owned for years; some history; small to medium amounts. Some move to a new bank, as in L1. |
| L4 | Large known bill | 500 | Rent-like amounts of about R$1,200 to R$6,300, usually 5 to 30 times the median, to a recipient paid at least 3 times; days 1 to 10 of the month; a pre-registered recipient's specific limit when the period limit is too low. Mirrors F1 amounts. |
| L5 | Small night payment | 400 | 20h to 6h; R$20 to R$200; a known person or a bar or delivery merchant. Mirrors the night scenarios. |
| L6 | New payer | 350 | A payer account weeks old with 0 to 3 settled payments, so the median is empty when the count is 0; a first purchase from an established merchant. |
| L7 | No device | 300 | `no_payer_device`, the whole device block empty: Pix Automático bills and corporate API payments, 40% without a key, some companies without a period limit. |
| L8 | Fresh key, established person | 250 | An EVP key under 30 days old on an account years old with years of settlements; 60% with earlier payments from the payer. Mirrors F1 keys. |
| L9 | Cleared recipient | 150 | 1 to 3 infraction reports rejected in the last 12 months and no fraud marker; a few with a report still open. |
| L10 | First payment to a clean recipient | 700 | No earlier payment; a clean recipient whose account is 30 days to 5 years old, with a key created any time up to the day before; 15% are large first payments, 3 to 15 times the median. Most first payments are legitimate. |
| L11 | Bill day near the limit | 170 | Consumption plus amount at 86% to 99.5% of the limit on days 5 to 10 of the month; known recipient. Mirrors limit-draining frauds. |
| L12 | Large atypical purchase | 200 | 10 to 25 times the median to an established company, such as a car down payment; when the daily limit is too low, the payer raised it or pre-registered the seller. Mirrors F1 amounts. |

### By partition

With the boundaries of the [example configuration](#training-on-it), the usable records split as follows. Every partition has frauds from several scenarios, including first-party fraud.

| Partition | Decision times | Usable fraud | Usable legitimate |
|---|---|---:|---:|
| Train | up to 2026-01-31 | 291 | 4,550 |
| Validation | 2026-02-01 to 2026-04-30 | 110 | 1,646 |
| Test | from 2026-05-01 | 77 | 1,286 |

Two campaigns cross the train boundary, so the training report shows 2 shared group keys and 5 validation records sharing a train key: the overlap disclosure has something to disclose.

## Campaign lifecycle and the point-in-time rule

Every fraud campaign runs for 3 to 45 days. A first-party campaign is a ring of payers paying ordinary recipients; every other campaign has one recipient, a mule account with one Pix key, and plays out like this:

1. The mule account opens and its key is linked, at the times the archetype dictates. A key's ownership date never follows its link date (`pix-payer-input-v2` §10).
2. Victims across the whole Pix system pay the mule during the campaign, 1 to 20 times as many as the dataset holds, from 40 other participants. The dataset's rows are this PSP's victims.
3. Each payment is reported with a probability of 0.65 (0.45 for investments, 0.75 for coercion, 0.40 for fake jobs), 1 to 10 days later. Investment victims report 7 to 40 days later.
4. A report opens an infraction report, which stays open for 1 to 7 days. 80% are accepted and become a fraud marker of the archetype's type, such as `scammer_accounts` or `mule_accounts`, from the reporting participant. 20% are rejected and count in `rejected_reports`.
5. Every settled payment adds to the recipient's settlement counts. Mature mules and takeover mules also carry markers and open reports from before the campaign.

**Point-in-time rule.** A DICT statistic in a row counts only the events that happened before that row's `as_of`, which is never later than `decision_time`. The windows follow DICT's definitions exactly, in Brasília days and months: `d90` is the last 89 days and the current one; `m12` and `m60` are the 12 and 60 months before the current month, so an event of the current month counts in `d90` but not in `m12` (`pix-payer-input-v2` §7). As a result:

- early victims of a campaign see a clean recipient, the hard cases;
- later victims see open reports first, then markers in `d90`, and, once a month has turned, reporters in `m12`, the easy cases;
- no row ever sees a report filed after its decision.

Key statistics are a subset of the person's, because the person's statistics cover all of their keys and accounts, and both come from one DICT statistics response with the same `as_of`.

## Regulatory anchors

The scenarios use these rules and figures for calibration only; the toolkit enforces none of them (ADR 0002).

| Anchor | Use |
|---|---|
| DICT fraud markers and infraction reports (Resolução BCB nº 343/2023, nº 506/2025) | The marker types, distinct reporters, and open and rejected infraction reports of the campaign model. |
| Special Return Mechanism, MED (Resolução BCB nº 103/2021; MED 2.0, Resolução BCB nº 493/2025) | Reports filed days after a payment, an analysis of up to 7 days, and partial refunds in fraud losses. The 80-day MED contestation period of the Manual Operacional do DICT is why every label is at least 90 days old. |
| Nighttime limit (Resolução BCB nº 142/2021; IN BCB nº 512/2024 art. 3º) | 20h to 6h, or 22h to 6h at the payer's request, with a default of R$1,000 when the recipient is a natural person; a daily limit when it is a company. |
| Unregistered devices (IN BCB nº 491/2024 art. 9) | At most R$200 per payment and R$1,000 per day from an unregistered device, which bounds takeovers and payments from new phones. |
| Average scam ticket, about R$1,735 (2025 market reporting) | Calibrates the social-engineering amounts. |

## Missing and stale observations

Each row draws its availability independently of its label, at the rates of a typical integration: a DICT statistics response that failed (about 1.2% of rows) or came from a stale cache (0.4%), single DICT groups or windows omitted, missing or stale limits, device data, pre-registrations, and entries, and manual payments without a key whose recipient account entry is usually missing. Empty cells follow [`pix-training-csv-v1` §4](../docs/contracts/pix-training-csv-v1.md#4-values) exactly: an empty `as_of` empties its observation, and an omitted subject leaves every cell of it empty. A zero limit is never used, so `train` reports the `zero_limit` state as not exercised.

## Converting and training

Keep converted files and runs in `data/`, which git ignores:

```bash
mkdir -p data/runs
go run ./cmd/convert -input datasets/pix-training-scenarios-v1.csv -output data/scenarios.jsonl
```

`convert` reports 8,000 rows: 7,960 usable (478 fraud, 7,482 legitimate), 40 insufficient, and 0 immature.

### Training on it

A configuration that gives every partition enough frauds, with the selection policy of the integration tests: the checkpoint with the best validation loss, evaluated every 10 epochs, and the threshold with the highest validation recall at a false positive rate of at most 0.05.

```json
{
  "config_version": "pix-training-config-v2",
  "feature_schema": "pix-risk-v1",
  "architecture": { "hidden_layers": [16, 8], "activation": "sigmoid" },
  "algorithm": "full-batch-gradient-descent",
  "loss": "mean-squared-error",
  "seed": 42,
  "learning_rate": 1.0,
  "max_epochs": 5000,
  "boundaries": {
    "train_end": "2026-01-31T23:59:59-03:00",
    "validation_end": "2026-04-30T23:59:59-03:00"
  },
  "selection": {
    "checkpoint": { "policy": "best_validation", "objective": "validation_loss", "evaluation_interval": 10, "min_improvement": 0 },
    "early_stopping": { "policy": "none" },
    "threshold": { "policy": "max_recall_at_max_false_positive_rate", "max_false_positive_rate": 0.05 },
    "test_evaluation": "after_selection"
  }
}
```

Save it as `data/scenarios-config.json`, then train, promote, and evaluate on the test partition:

```bash
go run ./cmd/train -config data/scenarios-config.json -dataset data/scenarios.jsonl -run-dir data/runs/scenarios-v1
go run ./cmd/promote -run data/runs/scenarios-v1 -output data/scenarios-model.json
go run ./cmd/inspect -run data/runs/scenarios-v1 -dataset data/scenarios.jsonl -output data/scenarios-inspection.json
go run ./cmd/evaluate -model data/scenarios-model.json -dataset data/scenarios.jsonl -partition test -output data/scenarios-evaluation.json
```

`inspect` reports, among other things, that no train record exercises `limit_zero` or `key_type_other`, that the score falls as `key_distinct_accounts_m12` grows past 1, and that masking the recipient's key entry lets frauds fall below the threshold: the model learned the shortcuts this dataset makes true ([`pix-model-inspection-v1`](../docs/contracts/pix-model-inspection-v1.md)). A [`pix-training-config-v3`](../docs/contracts/pix-training-config-v3.md) configuration with the scaled initialization, zeroed unexercised inputs, the cross-entropy, weight decay, and availability dropout, such as `docs/contracts/training-config-examples/15-v3-regularized.json` with these boundaries, addresses the training-side causes; the data-side ones need another dataset.

`evaluate` exits with code 3 and marks the report incomplete for `unknown_loss_frauds`: some test frauds have an unknown loss by design, so the known-loss sums are partial ([`pix-evaluation-report-v1` §5.5](../docs/contracts/pix-evaluation-report-v1.md#55-completeness)).

## How it was made

The file was written once by a standalone Go program that uses only the standard library and a fixed seed, 49. The program is not versioned: the CSV file is the artifact, and changing it means writing a new dataset with its own name. The program never imported the toolkit; the toolkit's `cmd/convert` validated its output, and every problem was fixed in the program until `convert` reported none.

## How it stays honest

`TestScenarioDataset` in [`internal/e2e`](../internal/e2e/dataset_test.go) converts this file and the v2 file with the `convert` command built from the checkout and checks the summaries: for v1, 8,000 rows, 7,960 usable records with 478 frauds, 40 insufficient, no immature or invalid record. An edit to a file that breaks a contract rule or changes these counts fails CI, and so does a contract change the file no longer meets.

## Caveats

- **Separable by design.** Each scam scenario has several signals at once, such as a young account, a young key, an atypical amount, and a first payment, so a small network detects most of them. The first-party scenario and the hard negatives are where models make their mistakes. Real fraud is noisier.
- **Clean labels.** Every `legitimate` label is true and every fraud is labeled; real data has unreported frauds and disputed labels.
- **Simplified counters.** Settlement counts come from a rate per recipient, and the payer's limits, consumption, and histories are approximations. Only the campaign events are simulated one by one.
- **One PSP's view.** Every payer belongs to the same PSP, and relationships between payers and recipients are drawn per row, so the same pair is not always consistent across rows.
