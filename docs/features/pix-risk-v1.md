# Feature schema `pix-risk-v1`

**Status:** Specification, implemented by the `pixrisk` package and used by `cmd/train`, `cmd/evaluate`, `cmd/predict`, and the `scoring` package.

## 1. Scope

This specification defines the feature schema `pix-risk-v1`: the exact ordered numeric inputs of a payer-side Pix fraud model, how each is prepared from one scoring input, how missing, stale, not applicable, and known-zero information is represented, and which transformation state is fitted during training. It also defines the input contract changes that the schema needs, which make up `pix-payer-input-v2` and `pix-training-example-v2`.

Out of scope:

- Implementation of the contracts, the preparation, training, and artifacts (issues #26 to #29).
- Obtaining data from DICT, providers, or the PSP's own systems (ADR 0001).
- Enforcing Pix limits, device rules, or any other regulatory rule, and choosing an operational decision (ADR 0002, ADR 0012).
- Selecting or adapting a real dataset, and any claim about detection quality.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as described in RFC 2119 and RFC 8174 when, and only when, they appear in all capitals.

## 2. Versions

Each identifier changes for its own reasons and is recorded separately (ADR 0003).

| Identifier | What it versions | Defined by |
|---|---|---|
| `pix-payer-input-v2` | The scoring input an integrator supplies. Replaces `pix-payer-input-v1` (ADR 0013). | [`pix-payer-input-v2`](../contracts/pix-payer-input-v2.md); [section 5](#5-input-contract-pix-payer-input-v2) summarizes the changes. |
| `pix-training-example-v2` | A training example embedding a `pix-payer-input-v2` scoring input. Replaces `pix-training-example-v1`. | [`pix-training-example-v2`](../contracts/pix-training-example-v2.md). |
| `pix-risk-v1` | The ordered features, their formulas, availability rules, fixed transformations, and the layout of the fitted preparation state. Replaces `transaction-features-v1`. | This document. |
| Preparation state | The values fitted on the train partition for one model ([section 11](#11-fitted-standardization)). It has no version of its own: its layout belongs to `pix-risk-v1`. | This document. |
| Training configuration | Architecture, optimizer, epochs, boundaries, selection policy. | #27. |
| Training report | Dataset, partition, coverage, and metric results of a run. | #28. |
| Model artifact format | The saved file holding parameters, the feature descriptor, and the preparation state. | [`pix-risk-model-v1`](../contracts/pix-risk-model-v1.md). |

A feature schema version says nothing about a model's quality, and an artifact format version says nothing about the features it carries.

## 3. Sources

Facts that come from DICT or from Pix regulation were checked on 2026-10-13 against these primary sources. This specification describes data an integrator supplies; it does not certify regulatory compliance.

| Source | Version | Used for |
|---|---|---|
| DICT API, <https://www.bcb.gov.br/content/estabilidadefinanceira/pix/API-DICT.html> | v2.12.1, 2026-07-14. Still the latest stable version on 2026-10-13; release candidates 2.13.0_rc1 and rc2 change no statistics schema. | Entry and statistics fields, unchanged from `pix-payer-input-v1` §13. |
| Manual Operacional do DICT | v8.5, 2026-09-01 (not re-checked for this issue). | As in `pix-payer-input-v1`. |
| Regulamento Pix, annex to Resolução BCB nº 1/2020, <https://www.bcb.gov.br/estabilidadefinanceira/exibenormativo?tipo=Resolução%20BCB&numero=1> | Version 43, text updated through Resolução BCB nº 597/2026. | Art. 37 (value limits), art. 89 §§6º–9º (access devices registered in advance, from Resolução BCB nº 403/2024), art. 3º XXVIII (access device). |
| Instrução Normativa BCB nº 512/2024, <https://www.bcb.gov.br/estabilidadefinanceira/exibenormativo?tipo=Instrução%20Normativa%20BCB&numero=512> | Consolidated text including IN BCB 746/2026. | Art. 3º (limits per period; daytime and nighttime periods when the recipient is a natural person; daily limit when the recipient is a legal person; the payer's registered address or Brasília time as the time reference, at the participant's choice), art. 10 §2º V and art. 14 (registration of recipient accounts or users for specific limits, effective 24 to 48 hours after registration). |
| Instrução Normativa BCB nº 491/2024 | Consolidated text including IN BCB 594/2025. | Value limits for access devices never used to initiate a Pix payment. |

## 4. Principles

1. **Point-in-time.** Preparation reads only the scoring input. Every age and freshness check is relative to `decision_time`; the system clock is never read (ADR 0005).
2. **Integrators supply facts.** The integrator determines every regulatory or operational fact, such as which limit period applies, which time reference the PSP uses, or whether a registration is in effect. The toolkit never decides which rule applies and never enforces one (ADR 0002, ADR 0012).
3. **Required facts and optional observations.** A missing or stale required fact makes the input insufficient. An optional observation that is not available feeds features with a neutral value and an availability indicator; no value is imputed silently (ADR 0011).
4. **No identifiers.** No feature uses a label, a group key, an observation later than `decision_time`, or a personal identifier (ADR 0007, ADR 0009).
5. **Determinism.** The same scoring input and the same preparation state always produce the same features, whatever the offset in which `decision_time` is written.

## 5. Input contract `pix-payer-input-v2`

`pix-payer-input-v2` is `pix-payer-input-v1` with the changes below. Everything else, including the document rules, value types, nullability hierarchy, DICT observations, consistency rules, validation stages, and validation codes, is unchanged. `contract_version` is `pix-payer-input-v2`, and it is the only supported version (ADR 0013).

### 5.1 New Pix payment members

Both members are required and never `null`, like every member of `pix_payment`.

| Member | Type | Rules |
|---|---|---|
| `reference_time_offset` | offset string | The UTC offset of the time reference the payer's PSP uses for this payer's limit periods at `decision_time`: the payer's registered address or Brasília time, at the PSP's choice (IN BCB 512 art. 3º §4º). |
| `payer_limit_period` | closed enumeration | The limit period that the payer's PSP applies to this payment, determined by the integrator using the same time reference. |

**Offset string.** A JSON string that matches `^[+-][0-9]{2}:[0-9]{2}$`:

- the minutes are `00` to `59`;
- the offset is at most 14:00 in either direction, so the supported range is `-14:00` to `+14:00`. This range is a choice of this contract, not a regulatory requirement;
- `+00:00` is a valid, concrete reference. `-00:00`, which RFC 3339 defines as an unknown local offset, is rejected, and so is `Z`;
- precision is one minute.

A value that does not match the syntax, has minutes above `59`, or is `-00:00` is rejected with `invalid_format`. A well-formed value above 14:00, such as `+14:30` or `-15:00`, is rejected with `out_of_range`. The toolkit does not check whether an offset is plausible for Brazil, does not restrict it to Brazilian time zones, and never infers it from the offset in which `decision_time` is written.

**`payer_limit_period` values.**

| Value | Meaning |
|---|---|
| `daytime` | A limit per period applies, and this payment falls in the daytime period. Used when the recipient is a natural person. |
| `nighttime` | A limit per period applies, and this payment falls in the nighttime period. Used when the recipient is a natural person. |
| `daily` | A daily limit applies, as for a legal-person recipient. It identifies the kind of limit only; it does not say whether the payment happened at night. The local time of day stays available through `reference_time_offset`. |
| `no_period_limit` | No limit of this kind applies to the payment, for example for some legal-person payers. It does not mean that no operational or regulatory restriction applies. |

### 5.2 New observations

| Path | Presence | Members |
|---|---|---|
| `/observations/payer_account/period_limit` | Only when `payer_limit_period` is not `no_period_limit` | `limit_cents`, `consumed_cents`, `as_of` |
| `/observations/payer_device/registration` | Whenever the `payer_device` subject is present | `status`, `as_of` |
| `/observations/recipient_account/pre_registration` | Always | `status`, `as_of` |

Each new observation is an object or `null` (missing), and its members are required and non-nullable, as for every observation the integrator computes (`pix-payer-input-v1` §6.2). Their `as_of` follows the usual rules: it MUST NOT be later than `decision_time` (`as_of_after_decision_time`).

#### `period_limit`

| Member | Type | Rules |
|---|---|---|
| `limit_cents` | integer | 0 to 2^53−1. The limit in effect at `as_of` that the PSP applies to this payment for the period named by `payer_limit_period`, for this payer account, including a specific limit that applies because the recipient is pre-registered. `0` is a known limit of zero, for example at the payer's request (IN BCB 512 art. 3º §14). |
| `consumed_cents` | integer | 0 to 2^53−1. The amount already counted against that limit in the current period, as the PSP's limit mechanism counts it, excluding this payment. |
| `as_of` | timestamp | |

#### `registration`

| Member | Type | Rules |
|---|---|---|
| `status` | closed enumeration | `registered`: the payer device is an access device registered in advance for the payer and the registration is in effect at `as_of` (Regulamento Pix art. 89 §6º). `not_registered`: it is not. |
| `as_of` | timestamp | |

Registration is not usage. A registered device can have no settled Pix payments, and a device with settled payments can be unregistered; usage stays in `payer_device.settled_outgoing_pix_90d`. When the PSP keeps no registration for a payer, for example because the rule does not apply to legal persons, the device is `not_registered`: the status states a fact about the device, not whether the rule applies. `null` means that the integrator could not obtain the status.

#### `pre_registration`

| Member | Type | Rules |
|---|---|---|
| `status` | closed enumeration | `registered`: at `as_of`, a registration made by the payer through the PSP's recipient registration mechanism (IN BCB 512 art. 10 §2º V) is in effect and applies to the recipient of this payment. `not_registered`: no such registration applies. |
| `as_of` | timestamp | |

A registration counts only once it is in effect, not when it was requested. It applies to the recipient of this payment when it covers the recipient account that receives the payment, or when the PSP's mechanism registers the recipient person and therefore covers that account. A registration of another account of the same person does not apply by itself. The integrator determines applicability according to its own mechanism, and the contract records no registration scope, because the feature only needs to know whether an effective registration applies.

A pre-registered recipient is context for the model, not a guarantee of legitimacy: a fraudster can induce a payer to register a recipient.

### 5.3 Conditional presence

[`pix-payer-input-v1` §6.5](../contracts/pix-payer-input-v1.md#65-conditional-subjects) gains one row. The same rules apply: an omitted member is not applicable, and a present one with `null` is missing.

| Discriminator | Value | Member | Rule |
|---|---|---|---|
| `/pix_payment/payer_limit_period` | `daytime`, `nighttime`, `daily` | `/observations/payer_account/period_limit` | Required, as an object or `null`. |
| `/pix_payment/payer_limit_period` | `no_period_limit` | `/observations/payer_account/period_limit` | MUST be omitted. If present, even as `null`, it is rejected with `member_not_allowed`. |

`/observations/payer_device/registration` is part of the `payer_device` subject, so it is omitted with the subject when `payer_device_usage` is `no_payer_device`.

### 5.4 Validation codes

No code is added. The new members use `required`, `null_not_allowed`, `wrong_type`, `invalid_format`, `out_of_range`, `invalid_enum_value`, `member_not_allowed`, `unknown_member`, and `as_of_after_decision_time` with their existing meaning. An observation whose `as_of` is later than `decision_time` makes the input invalid; it is never treated as stale.

### 5.5 Unchanged members

All `pix-payer-input-v1` members keep their paths, types, and semantics. In particular, the three payer account histories keep representing an empty history as a known count of `0`: the integrator supplies them for every payer account, including new ones, and `null` means only that the history could not be obtained.

### 5.6 Training example `pix-training-example-v2`

`pix-training-example-v2` is `pix-training-example-v1` with `training_example_version` set to `pix-training-example-v2` and `scoring_input` holding a `pix-payer-input-v2` scoring input. The outcome, group key, classification, label maturity, evaluation protocol, fingerprint, and dataset report keep their v1 definitions. A record is insufficient when `pix-risk-v1` preparation returns `insufficient_input` for its scoring input.

## 6. Required facts and optional observations

**Required facts.** A required fact that is missing or stale makes preparation return `insufficient_input`, with one blocker per problem, and no features.

| Path | Maximum age | Absence |
|---|---|---|
| Every member of `/pix_payment` | Not applicable | Impossible in a valid input. |
| `/observations/payer_account/settled_outgoing_pix_90d` | 24 hours | Blocker `missing`. |
| `/observations/payer_account/initiated_outgoing_pix_24h` | 5 minutes | Blocker `missing`. |
| `/observations/recipient_person/settled_pix_from_payer_account_365d` | 24 hours | Blocker `missing`. |

**Optional observations.** An optional observation that is missing, stale, or not applicable makes the features it feeds unavailable ([section 7](#7-availability)); preparation still produces features.

| Path | Maximum age | Not applicable when |
|---|---|---|
| `/observations/payer_account/period_limit` | 5 minutes | `payer_limit_period` is `no_period_limit` |
| `/observations/payer_device/settled_outgoing_pix_90d` | 24 hours | `payer_device_usage` is `no_payer_device` |
| `/observations/payer_device/registration` | 5 minutes | `payer_device_usage` is `no_payer_device` |
| `/observations/recipient_account/pre_registration` | 5 minutes | Never |
| `/observations/recipient_account/entry` | 24 hours | Never |
| `/observations/recipient_pix_key/entry` | 24 hours | `recipient_key_usage` is `no_pix_key` |
| `/observations/recipient_person/{spi,fraud_markers,infraction_reports,entries}` | 24 hours | Never |
| `/observations/recipient_pix_key/{spi,fraud_markers,infraction_reports,entries}` | 24 hours | `recipient_key_usage` is `no_pix_key` |

An observation is fresh when `decision_time − as_of ≤ maximum age`; the limit is inclusive. Maximum ages are engineering choices of `pix-risk-v1`, not regulatory requirements, and changing one requires a new feature schema version. Five minutes applies to facts that can change within a session, such as limit consumption, device registration, and pre-registration, which takes effect 24 to 48 hours after it is requested.

## 7. Availability

Every optional source of a feature is in exactly one state:

| State | Meaning | Features it feeds |
|---|---|---|
| known | The value is present and fresh. | Prepared from the value. |
| `missing` | The observation, DICT group, member, or window is `null`. | Unavailable. |
| `stale` | The observation is present but older than its maximum age. | Unavailable. |
| `not_applicable` | A discriminator rules the observation out. | Unavailable, and the discriminator's indicator is 1. |
| `no_history` | `settled_outgoing_pix_90d.count` is `0`, so no median exists. | `amount_to_median_90d` is unavailable; the count itself is a known 0. |
| `zero_limit` | `period_limit.limit_cents` is a known `0`. | `limit_utilization` is unavailable and `limit_zero` is 1. |

An unavailable numeric feature has the value 0 and its availability indicator 0. An available one has its indicator 1. The value 0 never appears without an indicator that says whether it is a fact. The states stay distinguishable in two places:

- **Features:** an availability indicator, a discriminator indicator (`no_pix_key`, `no_payer_device`, `limit_period_none`), and `limit_zero` separate unavailable, not applicable, and zero limit. Missing and stale are both unavailable to the model.
- **Preparation notes:** preparation returns one note for every optional source that is not known, with its JSON Pointer `path` and its `status` ([section 13](#13-preparation-result-and-codes)), so missing, stale, not applicable, no history, and zero limit are always reported separately.

A known false is a fact: `not_registered` gives a feature value of 0 with indicator 1. A DICT member or window that is `null` inside a known group makes only the features computed from it unavailable; the other members keep their known values. A feature computed from several members, such as a sum, is unavailable when any member it needs is `null`. A partial sum is never computed.

## 8. Time

- **Freshness** uses `decision_time` and each `as_of`, compared as instants ([section 6](#6-required-facts-and-optional-observations)).
- **Local time of day.** The local instant is `decision_time` shifted by `reference_time_offset`. The local seconds since midnight are `s = ((unix_seconds(decision_time) + offset_seconds) mod 86400)`, using floor division, so fractions of a second are dropped and the result is from 0 to 86399. The angle is `θ = 2π·s / 86400`. The offset in which `decision_time` is written never matters: `2026-10-13T14:03:07.120-03:00` and `2026-10-13T17:03:07.120Z` give the same features for the same `reference_time_offset`.
- **Ages in days** count complete blocks of 24 hours between a date and `decision_time`: `floor((decision_time − date) / 24h)`, computed from exact instants without calendar arithmetic, time zones, or a saturating duration type. The consistency rules guarantee `date ≤ as_of ≤ decision_time`, so every age is at least 0. An age in hours is never computed (`pix-payer-input-v1` §9.4).

## 9. Features

`pix-risk-v1` has **62 features**, in this order. Notation:

- `a` is `amount_cents`; `log1p` and `ln` are the natural-logarithm functions of IEEE 754 binary64.
- **Kind** is `standardized` (fixed transformation followed by fitted standardization, [section 11](#11-fitted-standardization)), `indicator` (0 or 1), or `cyclic` (from −1 to 1, not standardized).
- **Indicator** is the index of the availability indicator a standardized feature or a known boolean depends on; "always" means the feature is always available.

### 9.1 Payment and context

| # | Name | Kind | Value | Indicator |
|---|---|---|---|---|
| 0 | `amount` | standardized | `log1p(a)` | always |
| 1 | `recipient_legal_person` | indicator | 1 if `recipient_person_type` is `LEGAL_PERSON` | always |
| 2 | `no_pix_key` | indicator | 1 if `recipient_key_usage` is `no_pix_key` | always |
| 3 | `no_payer_device` | indicator | 1 if `payer_device_usage` is `no_payer_device` | always |
| 4 | `local_time_sin` | cyclic | `sin(θ)` | always |
| 5 | `local_time_cos` | cyclic | `cos(θ)` | always |
| 6 | `limit_period_nighttime` | indicator | 1 if `payer_limit_period` is `nighttime` | always |
| 7 | `limit_period_daily` | indicator | 1 if `payer_limit_period` is `daily` | always |
| 8 | `limit_period_none` | indicator | 1 if `payer_limit_period` is `no_period_limit` | always |

`daytime` is the value with indicators 6, 7, and 8 all at 0.

### 9.2 Payer account history (required)

| # | Name | Kind | Value | Indicator |
|---|---|---|---|---|
| 9 | `settled_count_90d` | standardized | `log1p(settled_outgoing_pix_90d.count)` | always |
| 10 | `initiated_count_24h` | standardized | `log1p(initiated_outgoing_pix_24h.count)` | always |
| 11 | `recipient_payments_365d` | standardized | `log1p(settled_pix_from_payer_account_365d.count)` | always |
| 12 | `amount_to_median_90d` | standardized | `ln(a) − ln(median_amount_cents)` | 13 |
| 13 | `amount_to_median_90d_known` | indicator | 1 if `settled_outgoing_pix_90d.count` > 0 | always |

Feature 11 replaces the binary `NewRecipient`: a known count of 0 is a new recipient.

### 9.3 Period limit

| # | Name | Kind | Value | Indicator |
|---|---|---|---|---|
| 14 | `limit_utilization` | standardized | `log1p((float64(consumed_cents) + float64(a)) / float64(limit_cents))`, in that order | 15 |
| 15 | `limit_utilization_known` | indicator | 1 if `period_limit` is known and `limit_cents` > 0 | always |
| 16 | `limit_zero` | indicator | 1 if `period_limit` is known and `limit_cents` is 0 | always |

The utilization is projected: it includes this payment, which `consumed_cents` excludes. It can exceed 1; enforcing the limit is the integrator's responsibility. With a zero limit, the ratio is never computed. When `period_limit` is missing, stale, or not applicable, features 14, 15, and 16 are all 0, and the notes and indicator 8 tell those cases apart.

### 9.4 Recipient pre-registration

| # | Name | Kind | Value | Indicator |
|---|---|---|---|---|
| 17 | `recipient_pre_registered` | indicator | 1 if `pre_registration.status` is `registered` | 18 |
| 18 | `recipient_pre_registered_known` | indicator | 1 if `pre_registration` is known | always |

### 9.5 Payer device

| # | Name | Kind | Value | Indicator |
|---|---|---|---|---|
| 19 | `device_settled_count_90d` | standardized | `log1p(payer_device.settled_outgoing_pix_90d.count)` | 20 |
| 20 | `device_settled_count_90d_known` | indicator | 1 if that observation is known | always |
| 21 | `device_registered` | indicator | 1 if `registration.status` is `registered` | 22 |
| 22 | `device_registered_known` | indicator | 1 if `registration` is known | always |

Features 19 and 21 have separate indicators because usage and registration are separate observations. With `no_payer_device`, features 19 to 22 are 0 and feature 3 is 1.

### 9.6 Recipient account

| # | Name | Kind | Value | Indicator |
|---|---|---|---|---|
| 23 | `recipient_account_age` | standardized | `log1p(age in days of recipient_account.entry.opening_date)` | 24 |
| 24 | `recipient_account_age_known` | indicator | 1 if `recipient_account.entry` is known | always |

### 9.7 Recipient Pix key entry

All members of `recipient_pix_key.entry` are required whenever the entry is an object, so features 25 to 32 share one indicator.

| # | Name | Kind | Value | Indicator |
|---|---|---|---|---|
| 25 | `key_link_age` | standardized | `log1p(age in days of creation_date)` | 33 |
| 26 | `key_ownership_age` | standardized | `log1p(age in days of key_ownership_date)` | 33 |
| 27 | `key_type_cpf` | indicator | 1 if `key_type` is `CPF` | 33 |
| 28 | `key_type_cnpj` | indicator | 1 if `key_type` is `CNPJ` | 33 |
| 29 | `key_type_phone` | indicator | 1 if `key_type` is `PHONE` | 33 |
| 30 | `key_type_email` | indicator | 1 if `key_type` is `EMAIL` | 33 |
| 31 | `key_type_evp` | indicator | 1 if `key_type` is `EVP` | 33 |
| 32 | `key_type_other` | indicator | 1 if `key_type` is any other value | 33 |
| 33 | `key_entry_known` | indicator | 1 if `recipient_pix_key.entry` is known | always |

`key_type_other` is a known, unrecognized type (ADR 0008), not a missing one: a missing entry has features 27 to 33 all at 0, while an unrecognized type has 32 and 33 at 1. Matching is exact and case-sensitive.

### 9.8 DICT statistics of the recipient person

Each feature has its own indicator, so known members of a partial response are kept.

| # | Name | Kind | Value | Indicator |
|---|---|---|---|---|
| 34 | `person_settlements_d90` | standardized | `log1p(spi.settlements.d90)` | 35 |
| 35 | `person_settlements_d90_known` | indicator | | always |
| 36 | `person_settlements_m12` | standardized | `log1p(spi.settlements.m12)` | 37 |
| 37 | `person_settlements_m12_known` | indicator | | always |
| 38 | `person_fraud_markers_d90` | standardized | `log1p(F.d90)` | 39 |
| 39 | `person_fraud_markers_d90_known` | indicator | | always |
| 40 | `person_fraud_markers_m12` | standardized | `log1p(F.m12)` | 41 |
| 41 | `person_fraud_markers_m12_known` | indicator | | always |
| 42 | `person_fraud_reporters_m12` | standardized | `log1p(fraud_markers.distinct_fraud_reporters.m12)` | 43 |
| 43 | `person_fraud_reporters_m12_known` | indicator | | always |
| 44 | `person_open_report_reporters` | standardized | `log1p(infraction_reports.open_reports_distinct_reporters)` | 45 |
| 45 | `person_open_report_reporters_known` | indicator | | always |
| 46 | `person_registered_accounts` | standardized | `log1p(entries.registered_accounts)` | 47 |
| 47 | `person_registered_accounts_known` | indicator | | always |

`F.w` is the sum, for window `w`, of `application_frauds`, `mule_accounts`, `scammer_accounts`, `other_frauds`, and `unknown_frauds`. It is computed exactly as an integer and is available only when the group is known and all five values for that window are non-null. Each indicator is 1 when its group is known and every value its feature needs is non-null.

### 9.9 DICT statistics of the recipient Pix key

Same definitions as [9.8](#98-dict-statistics-of-the-recipient-person), over `recipient_pix_key`. All of them are unavailable when `recipient_key_usage` is `no_pix_key`.

| # | Name | Kind | Value | Indicator |
|---|---|---|---|---|
| 48 | `key_settlements_d90` | standardized | `log1p(spi.settlements.d90)` | 49 |
| 49 | `key_settlements_d90_known` | indicator | | always |
| 50 | `key_settlements_m12` | standardized | `log1p(spi.settlements.m12)` | 51 |
| 51 | `key_settlements_m12_known` | indicator | | always |
| 52 | `key_fraud_markers_d90` | standardized | `log1p(F.d90)` | 53 |
| 53 | `key_fraud_markers_d90_known` | indicator | | always |
| 54 | `key_fraud_markers_m12` | standardized | `log1p(F.m12)` | 55 |
| 55 | `key_fraud_markers_m12_known` | indicator | | always |
| 56 | `key_fraud_reporters_m12` | standardized | `log1p(fraud_markers.distinct_fraud_reporters.m12)` | 57 |
| 57 | `key_fraud_reporters_m12_known` | indicator | | always |
| 58 | `key_open_report_reporters` | standardized | `log1p(infraction_reports.open_reports_distinct_reporters)` | 59 |
| 59 | `key_open_report_reporters_known` | indicator | | always |
| 60 | `key_distinct_accounts_m12` | standardized | `log1p(entries.distinct_accounts.m12)` | 61 |
| 61 | `key_distinct_accounts_m12_known` | indicator | | always |

### 9.10 Summary

| Kind | Count | Indices |
|---|---|---|
| standardized | 24 | 0, 9–12, 14, 19, 23, 25, 26, 34, 36, 38, 40, 42, 44, 46, 48, 50, 52, 54, 56, 58, 60 |
| cyclic | 2 | 4, 5 |
| indicator | 36 | all others |

## 10. Fixed transformations and numeric safety

Every fixed transformation is defined on a known, valid value and produces a finite binary64 number:

| Transformation | Input range | Output range |
|---|---|---|
| `log1p(count)` | 0 to 2^53−1 | 0 to about 36.74 |
| `log1p(F.w)`, with `F.w` up to 5·(2^53−1), summed exactly in 64-bit integers | 0 to about 4.5·10^16 | 0 to about 38.35 |
| `ln(a) − ln(median)` | both 1 to 2^53−1 | about −36.74 to 36.74 |
| `log1p(utilization)` | `consumed_cents` 0 to 2^53−1, `a` and `limit_cents` 1 to 2^53−1 | 0 to about 37.43 |
| `log1p(age in days)` | 0 to about 3.65 million days | 0 to about 15.11 |
| `sin(θ)`, `cos(θ)` | `s` 0 to 86399 | −1 to 1 |

`ln(a) − ln(median)` is computed as a difference of logarithms, so no ratio is formed. Integers are converted to binary64 by rounding to nearest; every value up to 2^53−1 converts exactly. Nothing is clipped. A non-finite value at any step is an internal error, never a feature.

## 11. Fitted standardization

The 24 standardized features are standardized with values fitted on the train partition. Indicators and cyclic features keep their values.

**Fitting.** For each standardized feature `j`, take the fixed-transformed values `x` of the usable train records in which feature `j` is available, in dataset line order. With `n` such values:

```text
mean_j  = (Σ x) / n
var_j   = (Σ (x − mean_j)²) / n      population variance, two passes, summed in line order
std_j   = sqrt(var_j)
scale_j = std_j, or 1 when std_j is exactly 0
```

- Values that are unavailable never take part, and validation and test records are never read.
- When `std_j` is exactly 0, `scale_j` is 1 and the state records `constant_in_train` for feature `j`.
- When `n` is 0, training stops with `feature_never_known` naming the feature. No model is produced that never saw the feature available.

**Applying.** The final value of standardized feature `j` is `(x − mean_j) / scale_j` when it is available, and exactly `0` when it is not. Standardization never turns an unavailable feature into a non-zero value.

**Preparation state.** The fitted state of a model is `pix-risk-v1`, the list of 62 feature names in order, and, for each standardized feature, `mean`, `scale`, and whether it was `constant_in_train`. It is frozen before the validation partition is scored and stored in the model artifact ([`pix-risk-model-v1`](../contracts/pix-risk-model-v1.md)). Preparation for training, evaluation, and inference uses the same code and the same state, so the same input always produces the same vector.

## 12. Vocabularies

| Field | Values | Unrecognized values |
|---|---|---|
| `recipient_person_type` | `NATURAL_PERSON`, `LEGAL_PERSON` | Impossible; closed enumeration. |
| `payer_device_usage`, `recipient_key_usage` | As in `pix-payer-input-v1` | Impossible; closed enumerations. |
| `payer_limit_period` | `daytime`, `nighttime`, `daily`, `no_period_limit` | Impossible; closed enumeration. |
| `registration.status`, `pre_registration.status` | `registered`, `not_registered` | Impossible; closed enumerations. |
| `key_type` | `CPF`, `CNPJ`, `PHONE`, `EMAIL`, `EVP` | Open string (ADR 0008): any valid value outside the list sets `key_type_other` and adds a note with status `unrecognized`. |

## 13. Preparation result and codes

Preparation applies to a valid `pix-payer-input-v2` scoring input. An invalid input never reaches it; contract validation reports it with the contract's codes.

The result is one of:

- **Prepared:** the 62 values, before or after standardization, and a list of notes.
- **Insufficient input:** a non-empty list of blockers and no values. Each blocker has a `path` and a `reason`, `missing` or `stale`, as in `pix-payer-input-v1` §14.2. `no_history` and `not_applicable` are no longer blockers in `pix-risk-v1`.

Blockers and notes are sorted by the UTF-8 bytes of `path`, then of the code.

**Notes** describe optional sources that are not known. They never block preparation. The catalog is closed:

| Status | Path |
|---|---|
| `missing` | The `null` observation, group, member, or window. |
| `stale` | The `as_of` of the stale observation. |
| `not_applicable` | The discriminator: `/pix_payment/recipient_key_usage`, `/pix_payment/payer_device_usage`, or `/pix_payment/payer_limit_period`. |
| `no_history` | `/observations/payer_account/settled_outgoing_pix_90d/median_amount_cents` |
| `zero_limit` | `/observations/payer_account/period_limit/limit_cents` |
| `unrecognized` | `/observations/recipient_pix_key/entry/key_type` |

Only sources that a feature reads produce notes: a `null` `m60` window, for example, produces none.

**Training codes:** `feature_never_known` stops training ([section 11](#11-fitted-standardization)), and `constant_in_train` is recorded in the preparation state without stopping it.

## 14. Supported populations

A payment receives features, and therefore can receive a score, when its scoring input is valid and every required fact is present and fresh. In particular, these populations are supported, with the indicators that identify them:

| Population | Indicators |
|---|---|
| New payer account, with no settled payments in 90 days | `settled_count_90d` at its known zero value, `amount_to_median_90d_known` = 0 |
| Payment without a Pix key | `no_pix_key` = 1 |
| Payment without a payer device | `no_payer_device` = 1 |
| Manual Pix to another PSP, with no recipient account entry | `recipient_account_age_known` = 0 |
| Payment without a limit per period | `limit_period_none` = 1 |

Support means the software produces a vector for these payments. It says nothing about detection quality for any population.

## 15. Training data and coverage

- Training fixtures and examples MUST include fraud and legitimate payments with registered and unregistered devices, with pre-registered and not pre-registered recipients, in every supported population, and with every availability state of [section 7](#7-availability), so that no standardized feature is unavailable in every usable train record.
- The training report of #28 MUST show coverage for every partition: the number of usable records in each population of [section 14](#14-supported-populations), in each `payer_limit_period`, and with each availability state of each optional source. An availability state that the train partition never contains is listed as not exercised. Coverage shows what the model has seen; it does not measure detection quality.

## 16. Examples

### 16.1 Complete example `E01`

```json
{
  "contract_version": "pix-payer-input-v2",
  "decision_time": "2026-10-13T14:03:07.120-03:00",
  "pix_payment": {
    "amount_cents": 250000,
    "payer_device_usage": "payer_device",
    "recipient_key_usage": "pix_key",
    "recipient_person_type": "NATURAL_PERSON",
    "reference_time_offset": "-03:00",
    "payer_limit_period": "daytime"
  },
  "observations": {
    "payer_account": {
      "settled_outgoing_pix_90d": { "count": 31, "median_amount_cents": 25000, "as_of": "2026-10-13T06:00:00Z" },
      "initiated_outgoing_pix_24h": { "count": 2, "as_of": "2026-10-13T17:03:06.874Z" },
      "period_limit": { "limit_cents": 500000, "consumed_cents": 120000, "as_of": "2026-10-13T17:03:06.874Z" }
    },
    "payer_device": {
      "settled_outgoing_pix_90d": { "count": 14, "as_of": "2026-10-13T06:00:00Z" },
      "registration": { "status": "registered", "as_of": "2026-10-13T17:03:06.874Z" }
    },
    "recipient_person": {
      "settled_pix_from_payer_account_365d": { "count": 0, "as_of": "2026-10-13T06:00:00Z" },
      "spi": "… as in pix-payer-input-v1 example 01 …",
      "fraud_markers": "… as in pix-payer-input-v1 example 01 …",
      "infraction_reports": "… as in pix-payer-input-v1 example 01 …",
      "entries": "… as in pix-payer-input-v1 example 01 …"
    },
    "recipient_account": {
      "entry": { "opening_date": "2019-05-14T03:00:00Z", "as_of": "2026-10-13T17:03:05.912Z" },
      "pre_registration": { "status": "not_registered", "as_of": "2026-10-13T17:03:06.874Z" }
    },
    "recipient_pix_key": "… as in pix-payer-input-v1 example 01 …"
  }
}
```

The elided groups are exactly those of [`pix-payer-input-v1` example 01](../contracts/examples/01-complete.json); the full file is [`examples-v2/01-complete.json`](../contracts/examples-v2/01-complete.json), and every variant below has its own file and expected outcome in that directory. Every source is known and fresh, so preparation returns 62 values and no notes. Values before standardization, rounded to six decimals for presentation; golden tests fix the exact binary64 values:

| # | Name | Value | # | Name | Value |
|---|---|---|---|---|---|
| 0 | `amount` | `log1p(250000)` = 12.429220 | 23 | `recipient_account_age` | `log1p(2709)` = 7.904704 |
| 1 | `recipient_legal_person` | 0 | 24 | `recipient_account_age_known` | 1 |
| 2 | `no_pix_key` | 0 | 25 | `key_link_age` | `log1p(451)` = 6.113682 |
| 3 | `no_payer_device` | 0 | 26 | `key_ownership_age` | `log1p(1342)` = 7.202661 |
| 4 | `local_time_sin` | `sin(2π·50587/86400)` = −0.511731 | 27–30 | `key_type_cpf` … `key_type_email` | 0 |
| 5 | `local_time_cos` | `cos(2π·50587/86400)` = −0.859146 | 31 | `key_type_evp` | 1 |
| 6–8 | limit period indicators | 0 | 32 | `key_type_other` | 0 |
| 9 | `settled_count_90d` | `log1p(31)` = 3.465736 | 33 | `key_entry_known` | 1 |
| 10 | `initiated_count_24h` | `log1p(2)` = 1.098612 | 34 | `person_settlements_d90` | `log1p(12)` = 2.564949 |
| 11 | `recipient_payments_365d` | `log1p(0)` = 0 | 36 | `person_settlements_m12` | `log1p(40)` = 3.713572 |
| 12 | `amount_to_median_90d` | `ln(250000) − ln(25000)` = 2.302585 | 38, 40, 42, 44 | person fraud and report features | `log1p(0)` = 0 |
| 13 | `amount_to_median_90d_known` | 1 | 46 | `person_registered_accounts` | `log1p(2)` = 1.098612 |
| 14 | `limit_utilization` | `log1p(370000/500000)` = 0.553885 | 48 | `key_settlements_d90` | `log1p(9)` = 2.302585 |
| 15 | `limit_utilization_known` | 1 | 50 | `key_settlements_m12` | `log1p(31)` = 3.465736 |
| 16 | `limit_zero` | 0 | 52, 54, 56, 58 | key fraud and report features | `log1p(0)` = 0 |
| 17 | `recipient_pre_registered` | 0 | 60 | `key_distinct_accounts_m12` | `log1p(1)` = 0.693147 |
| 18 | `recipient_pre_registered_known` | 1 | 35–47 odd, 49–61 odd | DICT indicators | 1 |
| 19 | `device_settled_count_90d` | `log1p(14)` = 2.708050 | | | |
| 20 | `device_settled_count_90d_known` | 1 | | | |
| 21 | `device_registered` | 1 | | | |
| 22 | `device_registered_known` | 1 | | | |

The local time is 14:03:07, so `s` = 50587. The ages count complete 24-hour blocks up to `2026-10-13T17:03:07.120Z`.

### 16.2 Variants

Each variant changes E01 as stated. Features not mentioned keep their E01 values.

| ID | Change | Outcome | Expected |
|---|---|---|---|
| E02 | Partial DICT: `recipient_person.fraud_markers.mule_accounts.d90` is `null`, and `recipient_pix_key.fraud_markers` is `null`. | Prepared | 38, 39 = 0; 40, 41 keep the m12 values, because the m12 sum is complete; 52–57 = 0. Notes: `missing` at both paths. |
| E03 | New account: `settled_outgoing_pix_90d` is `{count 0, median null}`, `initiated_outgoing_pix_24h.count` 1, `payer_device.settled_outgoing_pix_90d.count` 0. | Prepared | 9 = `log1p(0)`, 10 = `log1p(1)` = 0.693147, 12 = 0, 13 = 0, 19 = `log1p(0)`, 20 = 1. Note: `no_history`. |
| E04 | No key: `recipient_key_usage` is `no_pix_key`, `recipient_pix_key` is omitted, and `recipient_account.entry` is `null` (manual Pix to another PSP). | Prepared | 2 = 1; 23–33 = 0; 48–61 = 0. Notes: `missing` at `/observations/recipient_account/entry`, `not_applicable` at `/pix_payment/recipient_key_usage`. |
| E05 | No device: `payer_device_usage` is `no_payer_device`, and `payer_device` is omitted. | Prepared | 3 = 1; 19–22 = 0. Note: `not_applicable` at `/pix_payment/payer_device_usage`. |
| E06 | Optional stale: the `as_of` of `period_limit` and of `payer_device.registration` is `2026-10-13T16:58:07.119Z`, 5 minutes and 1 ms before `decision_time`. | Prepared | 14, 15, 16 = 0; 21, 22 = 0. Notes: `stale` at both `as_of` paths. |
| E07 | Zero limit: `period_limit.limit_cents` is 0. | Prepared | 14 = 0, 15 = 0, 16 = 1. Note: `zero_limit`. |
| E08 | No period limit: `payer_limit_period` is `no_period_limit`, and `period_limit` is omitted. | Prepared | 8 = 1; 14, 15, 16 = 0. Note: `not_applicable` at `/pix_payment/payer_limit_period`. |
| E09 | Same instant: `decision_time` is `2026-10-13T17:03:07.120Z`. | Prepared | Identical to E01, bit for bit. |
| E10 | Other reference: `reference_time_offset` is `-04:00`. | Prepared | Local time 13:03:07: 4 = −0.271930, 5 = −0.962317. |
| E11 | Unrecognized key type: `key_type` is `FUTURE_KEY_TYPE`. | Prepared | 31 = 0, 32 = 1, 33 = 1. Note: `unrecognized`. |
| E12 | Pre-registered recipient, unregistered device: `pre_registration.status` is `registered`, `registration.status` is `not_registered`. | Prepared | 17 = 1, 18 = 1; 21 = 0, 22 = 1. |
| E13 | Required stale: `initiated_outgoing_pix_24h.as_of` is `2026-10-13T16:58:07.119Z`, 5 minutes and 1 ms before `decision_time`. | Insufficient | Blocker `stale` at `/observations/payer_account/initiated_outgoing_pix_24h/as_of`. |
| E14 | Required missing: `settled_outgoing_pix_90d` is `null`. | Insufficient | Blocker `missing` at `/observations/payer_account/settled_outgoing_pix_90d`. |
| E15 | `payer_limit_period` is `no_period_limit`, but `period_limit` is present. | Invalid | `member_not_allowed` at `/observations/payer_account/period_limit`. |
| E16 | `reference_time_offset` is `-00:00`. | Invalid | `invalid_format` at `/pix_payment/reference_time_offset`. |
| E17 | `reference_time_offset` is `+14:30`. | Invalid | `out_of_range` at `/pix_payment/reference_time_offset`. |
| E18 | `reference_time_offset` is `null`. | Invalid | `null_not_allowed` at `/pix_payment/reference_time_offset`. |
| E19 | `payer_device.registration.as_of` is 1 ms after `decision_time`. | Invalid | `as_of_after_decision_time` at `/observations/payer_device/registration/as_of`. |
| E20 | `contract_version` is `pix-payer-input-v1`. | Invalid | Only `unsupported_contract_version` at `/contract_version`. |

## 17. Excluded and deferred candidates

These candidates are not in `pix-risk-v1`. The reasons are engineering hypotheses to evaluate with data later, not evidence that a signal is redundant or weak.

| Candidate | Hypothesis or reason |
|---|---|
| DICT `m60` windows | Hypothesis: mostly overlaps `m12` for payer-side risk. |
| Fraud markers by type | Hypothesis: individual types are sparse, and the sum keeps most of the signal with a fifth of the features. |
| `distinct_fraud_reporters` `d90` | Hypothesis: `m12` reporters plus the `d90` marker sum cover recent activity. |
| `rejected_reports` | Hypothesis: rejected reports carry a weaker or ambiguous signal. |
| `open_reports` | Hypothesis: one participant can inflate the raw count; distinct reporters are more robust. |
| `TotalFraudTransactionAmount` | Excluded by the contract: its unit is not established by a normative source. |
| Time since the last limit change | Needs a new observation; not requested for this version. |
| Hour features beyond sine and cosine, such as the day of the week | Not requested for this version. |

Adding any of them requires a new feature schema version.

## 18. Leakage and privacy

- No outcome, label as-of time, loss, or group key reaches preparation (ADR 0009).
- Every observation used has `as_of` ≤ `decision_time`; a later one makes the input invalid.
- The fitted state uses only usable train records.
- No new member carries a personal identifier. `reference_time_offset` can reveal the coarse time zone of the payer's registered address when the PSP uses it as the reference; it identifies no person, and it is supplied only because the local time features need it (ADR 0012).

## 19. Breaking migration

No compatibility with the v1 workflow is kept once the migration ends (ADR 0013). Each consumer moves to version 2 in its own issue, so the toolkit keeps working between them:

1. `pix-payer-input-v2` and `pix-training-example-v2`, their decoders, and `pix-risk-v1` preparation, shared by training, evaluation, and inference, are added (#26). Callers state which contract versions they accept. `no_history` and `not_applicable` are not blockers in `pix-risk-v1`.
2. Training moves to `pix-risk-v1` with a versioned configuration (#27), and model selection follows (#28).
3. A new artifact format carries the preparation state (#29). Models trained on `transaction-features-v1`, including the committed `model.json`, cannot score `pix-risk-v1` inputs.
4. `cmd/predict` requires an explicit model path, reads `pix-payer-input-v2`, and reports `pix-risk-v1` (#31).
5. `pix-payer-input-v1`, `pix-training-example-v1`, `transaction-features-v1`, `cmd/demo`, and `model.json` are removed (#33), after which the toolkit accepts version 2 only.
