# Input contract `pix-payer-input-v1`

**Status:** Normative for `pix-payer-input-v1`.

## 1. Scope

This specification defines the scoring input for payer-side analysis of one Pix payment before settlement: the JSON document an integrator supplies, how the toolkit validates it, how a valid scoring input converts to the feature schema `transaction-features-v1`, and the output of the reference command `cmd/predict`.

Out of scope:

- Obtaining data from DICT, providers, customer history, devices, or limits (ADR 0001).
- Regulatory rules and operational decisions (ADR 0002).
- Fraud labels and the format of training examples.
- Receiver-side analysis, which will be a separate contract (ADR 0006).

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as described in RFC 2119 and RFC 8174 when, and only when, they appear in all capitals.

## 2. Normative references

- DICT API v2.12.1, 2026-07-14: <https://www.bcb.gov.br/content/estabilidadefinanceira/pix/API-DICT.html>
- Manual Operacional do DICT v8.5, 2026-09-01: <https://www.bcb.gov.br/content/estabilidadefinanceira/pix/Regulamento_Pix/X_ManualOperacionaldoDICT.pdf>
- Regulamento Pix (Resolução BCB nº 1/2020), consolidated version 43.
- RFC 8259: The JavaScript Object Notation (JSON) Data Interchange Format.
- RFC 3339: Date and Time on the Internet: Timestamps.
- RFC 6901: JavaScript Object Notation (JSON) Pointer.

DICT API v2.13.0 release candidates and the archived `bacen/pix-dict-api` repository are not normative sources. A later DICT version can be used without changing this contract when it keeps the semantics, structure, and unit of every field this contract uses (ADR 0005). [Section 13.4](#134-source-precedence) defines the precedence between the DICT sources.

## 3. Related decisions

| ADR | Decision |
|---|---|
| [0001](../adr/0001-integrators-supply-all-information.md) | Integrators supply all information; the toolkit performs no lookups. |
| [0002](../adr/0002-scores-are-separate-from-rules-and-decisions.md) | Scores are separate from regulatory rules and operational decisions. |
| [0003](../adr/0003-separate-versions-for-contract-features-and-model-format.md) | Contract, feature schema, and model file format have separate versions. |
| [0004](../adr/0004-missing-observations-are-never-imputed.md) | Missing observations are never imputed; not applicable is not missing. |
| [0005](../adr/0005-scoring-inputs-are-point-in-time.md) | Scoring inputs are point-in-time. |
| [0006](../adr/0006-strict-contract-versions.md) | Unknown fields are rejected and every change is a new version. |
| [0007](../adr/0007-no-personal-identifiers-in-scoring-inputs.md) | No personal identifiers in scoring inputs. |
| [0008](../adr/0008-provider-extensible-enumerations-are-open.md) | Provider-extensible enumerations are open strings. |

Terms such as observation, subject, as-of time, missing, not applicable, empty history, and stale are defined in [`GLOSSARY.md`](../../GLOSSARY.md).

## 4. Document

A scoring input is one JSON document (RFC 8259) that satisfies all of these rules:

1. Its size is at most 65,536 bytes, counted on the raw document. A reader MUST check the limit before processing the whole document; when reading a stream, it stops as soon as it reads byte 65,537.
2. It does not start with a byte order mark (`EF BB BF`).
3. It is valid UTF-8.
4. It holds exactly one JSON value, and that value is an object. An empty document is invalid. Only JSON whitespace may follow the top-level object.
5. No object in it has two members with the same name, at any level. Member names are compared after JSON unescaping. A string with an unpaired UTF-16 surrogate escape, such as `"\ud800"`, is invalid JSON.
6. Every object contains only the members that this specification defines for it. Any other member is an unknown member (ADR 0006).

The document carries no personal data or identifiers, hashes of identifiers, or correlation IDs (ADR 0007). The closed member set in this specification enforces this.

The standard JSON decoder of the reference implementation silently accepts duplicate members and replaces invalid UTF-8. The reference implementation therefore checks rules 2, 3, and 5 explicitly, using only the Go standard library.

### 4.1 Top-level members

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `contract_version` | string | No | MUST be a supported contract version. The only supported version is `pix-payer-input-v1`. |
| `decision_time` | timestamp | No | The decision time, supplied by the integrator. |
| `pix_payment` | object | No | See [section 8](#8-pix-payment). |
| `observations` | object | No | See [section 9](#9-observations). |

## 5. Value types

### 5.1 Integer

A JSON number token that matches:

```text
^(0|[1-9][0-9]*)$
```

The token has no sign (neither `-` nor `+`), no fraction, no exponent, and no whitespace inside it. A token that is a JSON number but does not match this grammar, including `-0`, `-1`, `1.0`, and `1e3`, is rejected with `invalid_number_format`. A token that matches the grammar but whose value falls outside the field's range is rejected with `out_of_range`. Every integer field allows at most 2^53−1 (`9007199254740991`).

| Token | Field | Code |
|---|---|---|
| `-0` | any integer | `invalid_number_format` |
| `-1` | any integer | `invalid_number_format` |
| `1.0` | any integer | `invalid_number_format` |
| `1e3` | any integer | `invalid_number_format` |
| `0` | `amount_cents` | `out_of_range` |
| `9007199254740992` | any integer | `out_of_range` |

### 5.2 Timestamp

A JSON string holding a strict RFC 3339 `date-time`:

```text
YYYY-MM-DDTHH:MM:SS[.F]Z
YYYY-MM-DDTHH:MM:SS[.F]+hh:mm
YYYY-MM-DDTHH:MM:SS[.F]-hh:mm
```

- `YYYY` is from `0001` to `9999`, and the date exists in the proleptic Gregorian calendar.
- `T` and `Z` are uppercase.
- `HH` is `00` to `23`, `MM` is `00` to `59`, and `SS` is `00` to `59`. Seconds are required, and the leap second `60` is rejected.
- `.F` is optional: a period followed by 1 to 9 digits.
- The offset is required. `Z` is a valid offset. A numeric offset has `hh` from `00` to `23` and `mm` from `00` to `59`. The offset `-00:00`, which RFC 3339 defines as an unknown local offset, is rejected.
- Time zone names are not allowed.

Any violation is rejected with `invalid_format`. Timestamps are compared as instants, after normalizing their offsets. The value received is never rewritten.

### 5.3 Closed enumeration

A JSON string that equals one of the listed values exactly. Any other string is rejected with `invalid_enum_value`.

### 5.4 Open string

A JSON string for a list that the provider declares extensible (ADR 0008). The specification lists the known values and a syntax. Any value that matches the syntax is valid, raises no issue, and is preserved exactly as received: no case change, no trimming, and no conversion to `null`. A value that does not match the syntax is rejected with `invalid_format`.

### 5.5 Window counter

A JSON object that mirrors a DICT `WindowedValue`: the counters of one DICT statistic over its provider-defined windows. It contains exactly the members `d90`, `m12`, and `m60`. Each member is an integer from 0 to 2^53−1, or `null` when DICT omitted that window. In DICT API v2.12.1, every windowed statistic uses `WindowedValue`, which defines all three windows and makes each of them optional.

## 6. Missing, not applicable, and presence

`null` means missing when it stands in place of an observation, or in place of a provider value that the source allows to be omitted. `null` never means not applicable and is never converted implicitly to zero. Missing carries no reason.

### 6.1 Nullability hierarchy

```text
subject                       object, never null; presence can depend on a discriminator
└── observation               object or null (missing)
    ├── as_of                 required and never null when the observation is an object
    ├── member                value or object; null only where the member is a provider value
    │                         that DICT allows to be omitted, or where this specification
    │                         declares a semantic null
    └── window (d90/m12/m60)  integer, or null when DICT omitted that window
```

- Every member that this specification defines for an object MUST be present in it, even when its value is `null`, except a subject that a discriminator makes not applicable ([6.5](#65-conditional-subjects)).
- Members of the top level and of `pix_payment` are never nullable. Subjects are never nullable.

### 6.2 Observations computed by the integrator

For observations the integrator computes, such as the payer history counts:

- the whole observation is either an object or `null` (missing);
- every member of a known observation is required and non-nullable, unless this specification declares a semantic null;
- a semantic null never means missing. Its meaning comes from sibling members: `median_amount_cents` is `null` exactly when `count` is `0`, which states a known, empty history.

### 6.3 Observations mirroring DICT

For observations that mirror DICT data:

- a group that the source omitted is `null`;
- a group that is present is an object, and its `as_of` is required and non-nullable;
- a member that DICT allows to be omitted, and that the source omitted, is `null`;
- a window counter that is present has all three window members, and a window that the source omitted is `null`;
- members that DICT declares required, such as the members of `entry`, are non-nullable;
- if a group is present but the source omitted all of its members, the group stays an object with its `as_of`, and every member is `null`. This records that a snapshot existed at that instant while no value was supplied.

The integrator normalizes every omission that DICT allows into an explicit `null`. Absent members, partial responses, errors, and uncertain provider behavior MUST NOT be turned into a zero count. When a DICT query fails operationally, the affected observations are `null`, and the integrator records the failure in its own observability.

### 6.4 Known values

A known zero and a known false are not missing. An empty history is a known observation whose count is `0`.

### 6.5 Conditional subjects

A known fact that makes a subject not applicable is stated by a closed discriminator in `pix_payment`. The subject is then omitted. Not applicable is never stated with `null`, and never by omission alone.

| Discriminator | Value | Subject | Rule |
|---|---|---|---|
| `/pix_payment/recipient_key_usage` | `pix_key` | `/observations/recipient_pix_key` | Required, as an object with all of its observations, each an object or `null`. |
| `/pix_payment/recipient_key_usage` | `no_pix_key` | `/observations/recipient_pix_key` | MUST be omitted. If present, even as `null`, it is rejected with `member_not_allowed`. |
| `/pix_payment/payer_device_usage` | `payer_device` | `/observations/payer_device` | Required, as an object with all of its observations, each an object or `null`. |
| `/pix_payment/payer_device_usage` | `no_payer_device` | `/observations/payer_device` | MUST be omitted. If present, even as `null`, it is rejected with `member_not_allowed`. |

This keeps two cases apart: a subject that is absent because it is known not to apply, and a subject that is present with `null` observations because their data is missing. When no data was obtained for an applicable subject, the subject is still present:

```json
"recipient_pix_key": {
  "entry": null,
  "spi": null,
  "fraud_markers": null,
  "infraction_reports": null,
  "entries": null
}
```

## 7. Time

- `decision_time` is supplied by the integrator. The toolkit never reads the system clock to give meaning to an input.
- Every observation carries `as_of`: the instant up to which the integrator asserts that the observation is a known, usable snapshot (ADR 0005).
  - For a direct DICT query, `as_of` is the `ResponseTime` of the DICT response that carried the data. `ResponseTime` is required in `GetEntryResponse`, `GetEntryStatisticsResponse`, and `GetPersonStatisticsResponse`.
  - For the PSP's own database, `as_of` is the real instant at which the consulted snapshot was generated or last completely updated. It is never the instant at which the integrator read the database.
  - A query instant is a valid `as_of` only when the source guarantees that its data was complete up to that instant.
  - A DICT group watermark is never used as `as_of` and is not part of this contract.
- Members of `pix_payment` have no `as_of`.
- `as_of` MUST NOT be later than `decision_time`. There is no tolerance for clock skew.
- A contract-defined lookback window of duration `D` covers the interval `(as_of − D, as_of]`. It is rolling and does not depend on any time zone.
- DICT's provider-defined windows `d90`, `m12`, and `m60` follow DICT's definition exactly. Neither the integrator nor the toolkit converts or recalculates them. Per DICT API v2.12.1 (tag `Statistics`), all three are anchored at the query instant:
  - `d90`: "últimos 89 dias, incluindo o dia corrente";
  - `m12`: "últimos 12 meses, sem incluir o mês corrente";
  - `m60`: "últimos 60 meses, sem incluir o mês corrente".

  The 89 previous days plus the current day, and the Manual's "últimos 90 dias", describe the same period; they do not diverge. DICT's worked example places the boundaries at 03:00Z, but the documentation does not state Brasília time as a general rule, and this specification does not turn the example into one.

## 8. Pix payment

Path: `/pix_payment`. Members of the Pix payment are facts the payer's PSP knows when it builds the payment. They are never `null` and have no `as_of`.

| Member | Type | Rules |
|---|---|---|
| `amount_cents` | integer | 1 to 2^53−1. The full amount the payer ordered, in centavos of BRL. There is no currency member, and no limit beyond the technical one. |
| `payer_device_usage` | closed enumeration | `payer_device`: the payer authorized this specific Pix payment on a device. `no_payer_device`: no device authorized this specific payment, for example a recurring execution under a Pix Automático authorization, or a corporate API payment without per-payment device authorization. A scheduled payment that the payer authorized on a device is `payer_device`. |
| `recipient_key_usage` | closed enumeration | `pix_key`: a Pix key was actually used to identify or resolve the recipient, whatever the initiation method. `no_pix_key`: the payment was built without a Pix key, for example from account data. |
| `recipient_person_type` | closed enumeration | `NATURAL_PERSON` or `LEGAL_PERSON`. Taken from DICT `Owner.Type`, from the data the payer entered for a manual Pix, or from the PSP's own database. |

Neither discriminator describes the initiation method. QR codes, payment initiators, channels, and Pix Automático are not represented in this version.

## 9. Observations

Path: `/observations`. Members, one per subject:

| Member | Presence | Section |
|---|---|---|
| `payer_account` | Always | [9.1](#91-payer_account) |
| `payer_device` | Only when `payer_device_usage` is `payer_device` | [9.2](#92-payer_device) |
| `recipient_person` | Always | [9.3](#93-recipient_person) |
| `recipient_account` | Always | [9.4](#94-recipient_account) |
| `recipient_pix_key` | Only when `recipient_key_usage` is `pix_key` | [9.5](#95-recipient_pix_key) |

All payer history is computed for the payer account, which is the account the Pix payment debits, and never for other accounts of the same person. Relationships between payer and recipient are computed from the payer account's perspective, so a recipient known to account A can be new to account B.

Unless stated otherwise, a count excludes the Pix payment being scored.

### 9.1 `payer_account`

#### `settled_outgoing_pix_90d`

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `count` | integer | No | 0 to 2^53−1. Settled outgoing Pix payments from the payer account in the lookback window. |
| `median_amount_cents` | integer | Yes (semantic) | 1 to 2^53−1. Lower median of the amounts of those payments, in centavos. `null` if and only if `count` is `0`. |
| `as_of` | timestamp | No | |

Lookback window: 90 days. With an even count, the lower median is the smaller of the two middle values, so the result is always a whole number of centavos.

#### `initiated_outgoing_pix_24h`

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `count` | integer | No | 0 to 2^53−1. Initiated Pix payments from the payer account in the lookback window, whatever their outcome: settled, rejected, cancelled, or pending. |
| `as_of` | timestamp | No | |

Lookback window: 24 hours. An initiated Pix payment is a business instruction: technical retries, retransmissions, and reprocessing of the same instruction count once. The integrator performs this deduplication.

### 9.2 `payer_device`

The payer device is the device on which the payer authorized this Pix payment, as identified by the integrator. It is not the regulatory registered device.

#### `settled_outgoing_pix_90d`

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `count` | integer | No | 0 to 2^53−1. Settled outgoing Pix payments from the payer account that were initiated on the payer device in the lookback window. |
| `as_of` | timestamp | No | |

Lookback window: 90 days.

### 9.3 `recipient_person`

The recipient person is identified by CPF or CNPJ, across all of the person's accounts and Pix keys.

#### `settled_pix_from_payer_account_365d`

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `count` | integer | No | 0 to 2^53−1. Settled Pix payments from the payer account to the recipient person, through any account or key of that person, in the lookback window. |
| `as_of` | timestamp | No | |

Lookback window: 365 days.

#### DICT person statistics: `spi`, `fraud_markers`, `infraction_reports`, `entries`

Each group is a separate observation with its own `as_of`. Each mirrors the matching group of DICT `PersonStatistics`, which `OwnerStatistics` shares, without the group's `Watermark`. The integrator MAY obtain a group from `OwnerStatistics`, from `getPersonStatistics`, or from the PSP's own database. The contract does not record which source was used.

| Group | Members |
|---|---|
| `spi` | `settlements` (window counter) |
| `fraud_markers` | `application_frauds`, `mule_accounts`, `scammer_accounts`, `other_frauds`, `unknown_frauds`, `distinct_fraud_reporters` (window counters) |
| `infraction_reports` | `open_reports` (integer), `open_reports_distinct_reporters` (integer), `rejected_reports` (window counter) |
| `entries` | `registered_accounts` (integer) |

Every group also has `as_of`. Every member is nullable, because DICT allows each of them to be omitted ([6.3](#63-observations-mirroring-dict)). Types and semantics are in [section 13](#13-dict-traceability).

### 9.4 `recipient_account`

#### `entry`

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `opening_date` | timestamp | No | DICT `Account.OpeningDate`, or the opening date from the PSP's own database. |
| `as_of` | timestamp | No | |

`opening_date` is preserved as the source provides it. DICT states that when the time of day is unknown, the value is set to the start of the day in BRT, so the time component does not always carry real precision. A feature MUST NOT compute an account age in hours from this value.

For a manual Pix between different PSPs, the payer's PSP has no DICT entry, so this observation is usually missing.

### 9.5 `recipient_pix_key`

Present only when `recipient_key_usage` is `pix_key` ([6.5](#65-conditional-subjects)).

#### `entry`

| Member | Type | Nullable | Rules |
|---|---|---|---|
| `key_type` | open string | No | Known values: `CPF`, `CNPJ`, `PHONE`, `EMAIL`, `EVP`. Syntax: `^[A-Z][A-Z0-9_]{0,31}$`. |
| `creation_date` | timestamp | No | DICT `CreationDate`: creation of the link between the key and the current account. |
| `key_ownership_date` | timestamp | No | DICT `KeyOwnershipDate`: start of the current owner's uninterrupted ownership of the key, possibly across different accounts. |
| `as_of` | timestamp | No | |

#### DICT key statistics: `spi`, `fraud_markers`, `infraction_reports`, `entries`

Each group is a separate observation with its own `as_of`. Each mirrors the matching group of DICT `KeyStatistics`, without the group's `Watermark`.

| Group | Members |
|---|---|
| `spi` | `settlements` (window counter) |
| `fraud_markers` | `application_frauds`, `mule_accounts`, `scammer_accounts`, `other_frauds`, `unknown_frauds`, `distinct_fraud_reporters` (window counters) |
| `infraction_reports` | `open_reports` (integer), `open_reports_distinct_reporters` (integer), `rejected_reports` (window counter) |
| `entries` | `distinct_accounts` (window counter) |

Every group also has `as_of`. Every member is nullable, because DICT allows each of them to be omitted ([6.3](#63-observations-mirroring-dict)). Types and semantics are in [section 13](#13-dict-traceability).

### 9.6 DICT data excluded from this version

- `TotalFraudTransactionAmount`: its unit is not established by a normative source.
- Group `Watermark` fields (ADR 0005).
- `OpenClaimCreationDate`.
- `Account.AccountType`.
- Pix key values, account data, owner names, and tax identifiers (ADR 0007).

## 10. Consistency rules

A valid scoring input satisfies all of these rules:

| Rule | Code | Path of the issue |
|---|---|---|
| Every `as_of` ≤ `decision_time`. | `as_of_after_decision_time` | The `as_of` |
| In `payer_account.settled_outgoing_pix_90d`, `median_amount_cents` is `null` if and only if `count` is `0`. | `median_count_mismatch` | `median_amount_cents` |
| In `recipient_pix_key.entry`, `creation_date` ≤ `as_of`. | `date_after_as_of` | `creation_date` |
| In `recipient_pix_key.entry`, `key_ownership_date` ≤ `as_of`. | `date_after_as_of` | `key_ownership_date` |
| In `recipient_pix_key.entry`, `key_ownership_date` ≤ `creation_date`. | `key_ownership_after_creation` | `key_ownership_date` |
| In `recipient_account.entry`, `opening_date` ≤ `as_of`. | `date_after_as_of` | `opening_date` |

These are temporal and structural consistency rules derived from the normative semantics of the fields: ownership can span several accounts, so it starts no later than the link to the current account, and both precede the snapshot. They are not risk or plausibility rules. Each rule is checked independently, so one input can raise several of them.

The following are deliberately not validated:

- Any ordering between `d90`, `m12`, and `m60`. `d90` includes the current day, while `m12` and `m60` exclude the current month, so `d90` can exceed `m12`.
- Internal invariants of the provider, such as `open_reports_distinct_reporters` ≤ `open_reports`. The toolkit preserves valid facts from the source and does not reproduce provider rules that no normative source guarantees.

## 11. Validation

Validation decides whether a document is a valid scoring input. It checks the document, the contract version, structure, types, presence, unknown members, formats, ranges, and the consistency rules. It does not judge plausibility or business policy, such as transaction limits, amount risk, or regulatory decisions.

The result is either valid or a non-empty list of issues. Each issue has:

- `path`: a JSON Pointer (RFC 6901) to the problem, with `~` escaped as `~0` and `/` as `~1`. An issue about the whole document has the empty pointer `""`.
- `code`: a code from the closed catalog in [section 12](#12-validation-codes).
- `message`: an English message for humans. It is not part of the contract.

### 11.1 Stages

Validation runs in four stages. Stages 1 and 2 stop validation when they find an issue; stages 3 and 4 collect every issue they can detect safely.

1. **Document.** Checks the rules of [section 4](#4-document) in this order: size, byte order mark, UTF-8, JSON syntax and trailing data, top-level object. The first failure is the only issue reported. If those pass, every duplicate member is reported, each at the path of the repeated member. If any duplicate exists, validation stops, because the document is ambiguous.
2. **Contract version.** Locates `/contract_version`. If it is absent, the issue is `required`; if it is `null`, `null_not_allowed`; if it is not a string, `wrong_type`; if it is a string that is not a supported version, `unsupported_contract_version`. On any of these, validation stops: it does not assume `pix-payer-input-v1`, does not apply its structure, and reports no `unknown_member` or other version-specific issue.
3. **Structure and values.** Applies the structure of `pix-payer-input-v1` and collects every issue: unknown members, required members, conditional subjects, nulls, types, formats, ranges, and enumeration values. It does not descend into a value with the wrong type, nor into a subject rejected with `member_not_allowed`. A conditional subject's presence is checked only when its discriminator is valid. A `null` is checked only for whether its member is nullable.
4. **Consistency.** Checks the rules of [section 10](#10-consistency-rules). A rule is checked only when every value it involves passed stage 3 and is not `null`.

### 11.2 Ordering

Issues are sorted by the UTF-8 bytes of `path`, in ascending order, then by the UTF-8 bytes of `code`. The message does not take part in the ordering.

## 12. Validation codes

The catalog is closed. A new code requires a change to this specification.

| Code | Typical path | Condition | Stage | Example |
|---|---|---|---|---|
| `document_too_large` | `""` | The document exceeds 65,536 bytes. | 1 | A 70,000-byte document. |
| `bom_not_allowed` | `""` | The document starts with `EF BB BF`. | 1 | A document saved with a UTF-8 BOM. |
| `invalid_utf8` | `""` | The document is not valid UTF-8. | 1 | A byte `0xFF` inside a string. |
| `invalid_json` | `""` | The document is empty or is not valid JSON. | 1 | `{"contract_version":` |
| `trailing_data` | `""` | Something other than JSON whitespace follows the top-level value. | 1 | `{...} {...}` |
| `wrong_type` | `""` or member path | The top-level value is not an object, or a member has the wrong JSON type. | 1, 2, 3 | `"amount_cents": "250000"` |
| `duplicate_member` | Repeated member | A member name appears twice in one object. | 1 | `"count": 2, "count": 9` |
| `required` | Absent member | A required member is absent, including an applicable conditional subject. | 2, 3 | `recipient_pix_key` absent with `pix_key` |
| `unsupported_contract_version` | `/contract_version` | The version is a string that is not supported. | 2 | `"pix-payer-input-v2"` |
| `unknown_member` | Unknown member | A member that this specification does not define. | 3 | `"settled_outgoing_pix_90days"` |
| `member_not_allowed` | Subject path | A subject is present although its discriminator makes it not applicable. | 3 | `recipient_pix_key` with `no_pix_key` |
| `null_not_allowed` | Member path | A non-nullable member is `null`. | 2, 3 | `"as_of": null` in a known observation |
| `invalid_number_format` | Member path | An integer token does not match `^(0\|[1-9][0-9]*)$`. | 3 | `-0`, `1.0`, `1e3` |
| `out_of_range` | Member path | An integer is outside its field's range. | 3 | `"amount_cents": 0` |
| `invalid_format` | Member path | A timestamp is not strict RFC 3339, or an open string does not match its syntax. | 3 | `"2026-10-13T17:03:07"`, `"evp"` |
| `invalid_enum_value` | Member path | A closed enumeration has a value outside its list. | 3 | `"recipient_key_usage": "qr_code"` |
| `as_of_after_decision_time` | `as_of` path | `as_of` is later than `decision_time`. | 4 | `as_of` 1 ms after `decision_time` |
| `median_count_mismatch` | `median_amount_cents` | `median_amount_cents` is `null` while `count` > 0, or not `null` while `count` = 0. | 4 | `count` 0 with median 25000 |
| `date_after_as_of` | Date path | `creation_date`, `key_ownership_date`, or `opening_date` is later than its observation's `as_of`. | 4 | `creation_date` after `as_of` |
| `key_ownership_after_creation` | `/observations/recipient_pix_key/entry/key_ownership_date` | `key_ownership_date` is later than `creation_date`. | 4 | ownership in 2026, link in 2025 |

## 13. DICT traceability

Source: DICT API v2.12.1, the specification embedded in <https://www.bcb.gov.br/content/estabilidadefinanceira/pix/API-DICT.html>, unless a row says otherwise. Descriptions are quoted from the API. "W" is a window counter (DICT `WindowedValue`, with windows `d90`, `m12`, and `m60`). Contract names are mechanical `snake_case` forms of the DICT names.

### 13.1 Entry data (`GetEntryResponse`)

| Contract path | DICT element | Type | Required in DICT | Semantics |
|---|---|---|---|---|
| `/pix_payment/recipient_person_type` | `Entry.Owner.Type` | closed enumeration | Yes | `NATURAL_PERSON` or `LEGAL_PERSON`, the titles of the two `Person` branches. DICT declares no `enum` keyword. |
| `/observations/recipient_pix_key/entry/key_type` | `Entry.KeyType` | open string | Yes | "Tipo de chave. Novos tipos podem surgir." Enum in v2.12.1: `CPF`, `CNPJ`, `PHONE`, `EMAIL`, `EVP`. |
| `/observations/recipient_pix_key/entry/creation_date` | `Entry.CreationDate` | timestamp | Yes | "Data de criação do vínculo." |
| `/observations/recipient_pix_key/entry/key_ownership_date` | `Entry.KeyOwnershipDate` | timestamp | Yes | "Data a partir da qual o dono tem posse ininterrupta da chave de endereçamento. Posse da chave aqui é definida pela existência de um vínculo associando a chave ao dono, possivelmente com contas distintas." |
| `/observations/recipient_pix_key/entry/as_of` | `GetEntryResponse.ResponseTime` | timestamp | Yes | |
| `/observations/recipient_account/entry/opening_date` | `Entry.Account.OpeningDate` | timestamp | Yes | "Data de abertura da conta. Para o horário, caso não seja conhecido, convenciona-se o início do dia em BRT." |
| `/observations/recipient_account/entry/as_of` | `GetEntryResponse.ResponseTime` | timestamp | Yes | |

### 13.2 Key statistics (`KeyStatistics`)

Prefix: `/observations/recipient_pix_key`. Returned by `getEntryStatistics`, or by `getEntry` with `IncludeStatistics=true`. Each group's `as_of` is the `ResponseTime` of the response that carried it.

| Contract path | DICT element | Type | Semantics |
|---|---|---|---|
| `/spi/settlements` | `Spi.Settlements` | W | "Número de liquidações em que a chave foi usada no SPI". Counts only events after 2023-11-05T03:00:00Z. |
| `/fraud_markers/application_frauds` | `FraudMarkers.ApplicationFrauds` | W | Fraud markers of type `APPLICATION_FRAUD`. |
| `/fraud_markers/mule_accounts` | `FraudMarkers.MuleAccounts` | W | Fraud markers of type `MULE_ACCOUNT`. |
| `/fraud_markers/scammer_accounts` | `FraudMarkers.ScammerAccounts` | W | Fraud markers of type `SCAMMER_ACCOUNT`. |
| `/fraud_markers/other_frauds` | `FraudMarkers.OtherFrauds` | W | Fraud markers of type `OTHER`. |
| `/fraud_markers/unknown_frauds` | `FraudMarkers.UnknownFrauds` | W | Fraud markers without a type, created with API v1. |
| `/fraud_markers/distinct_fraud_reporters` | `FraudMarkers.DistinctFraudReporters` | W | Distinct participants that created fraud markers, excluding type `UNKNOWN`. |
| `/infraction_reports/open_reports` | `InfractionReports.OpenReports` | integer | Infraction reports neither concluded nor cancelled. |
| `/infraction_reports/open_reports_distinct_reporters` | `InfractionReports.OpenReportsDistinctReporters` | integer | Distinct participants that created infraction reports neither concluded nor cancelled. |
| `/infraction_reports/rejected_reports` | `InfractionReports.RejectedReports` | W | Infraction reports concluded with rejection. |
| `/entries/distinct_accounts` | `Entries.DistinctAccounts` | W | "Quantidade de contas distintas em que houve registro da chave". |

### 13.3 Person statistics (`PersonStatistics`, also used as `OwnerStatistics`)

Prefix: `/observations/recipient_person`. Returned by `getPersonStatistics`, by `getEntryStatistics` as `OwnerStatistics` (always present there), or by `getEntry` with `IncludeStatistics=true`.

| Contract path | DICT element | Type | Semantics |
|---|---|---|---|
| `/spi/settlements` | `Spi.Settlements` | W | "Número de liquidações como recebedor no SPI". Counts only events after 2023-11-05T03:00:00Z. |
| `/fraud_markers/*` | `FraudMarkers.*` | W | Same six members and semantics as in [13.2](#132-key-statistics-keystatistics). |
| `/infraction_reports/*` | `InfractionReports.*` | integer, integer, W | Same three members and semantics as in [13.2](#132-key-statistics-keystatistics). |
| `/entries/registered_accounts` | `Entries.RegisteredAccounts` | integer | "Número de contas da pessoa associadas a chaves atualmente registradas". |

### 13.4 Source precedence

For data the integrator receives from DICT:

1. DICT API v2.12.1 defines field names, structure, types, requiredness, representation, and the semantics of each returned value.
2. Manual Operacional do DICT v8.5 complements the API with operational behavior, delay and update rules, usage rules, procedures, and anything the API does not define. It never silently changes the structure or meaning that the API gives to a returned field.
3. Regulamento Pix, consolidated version 43, defines regulatory obligations and participant responsibilities. Those rules stay outside scoring (ADR 0002).

When the API and the Manual diverge, the contract follows the API to model the payload, records the divergence in [13.6](#136-divergences-between-api-and-manual), does not reconcile values by inference, and does not create hybrid fields.

### 13.5 Requiredness in DICT

- No statistics group declares required members, so every member in [13.2](#132-key-statistics-keystatistics) and [13.3](#133-person-statistics-personstatistics-also-used-as-ownerstatistics), and every `WindowedValue` attribute, is optional in DICT's schema. DICT's examples always show every member and all three windows. The contract represents each omission as `null` ([6.3](#63-observations-mirroring-dict)).
- `KeyStatistics` lists `["SPI", "InfractionReports", "Entries"]` as required, and `PersonStatistics` lists `["SPI", "FraudMarkers", "InfractionReports", "Entries"]`. The property is spelled `Spi`, so the `SPI` entry matches nothing, and `FraudMarkers` is not required in `KeyStatistics`. The contract allows any group to be `null`.
- The behavior when DICT has no statistics for a key or person is not established: the Manual §18 mentions an error message, while the API changelog removed the 404 error. When the integrator receives no value, it supplies `null` and never zero.
- `TotalFraudTransactionAmount` is an integer, "truncado na parte inteira", with no stated unit, so it stays excluded ([9.6](#96-dict-data-excluded-from-this-version)).

### 13.6 Divergences between API and Manual

Sources: DICT API v2.12.1 (schemas `KeyStatistics` and `PersonStatistics`) and Manual Operacional do DICT v8.5, §18.

| Contract path | API definition | Manual definition | Decision | Rationale |
|---|---|---|---|---|
| `/observations/recipient_pix_key/entries/distinct_accounts` | `Entries.DistinctAccounts`, a `WindowedValue`: "Quantidade de contas distintas em que houve registro da chave". | Item "l" names the counter `RegisteredAccounts` for both key and person, without windows. | Use `DistinctAccounts` with windows. | The API defines the payload the integrator receives ([13.4](#134-source-precedence)). |
| `/observations/recipient_pix_key/spi/settlements` | `Spi.Settlements`: "Número de liquidações em que a chave foi usada no SPI". | Settlements "como recebedor" for both key and person. | Describe settlements in which the key was used in SPI; do not restrict them to "como recebedor". | The API defines the semantics of the returned value ([13.4](#134-source-precedence)). |

The 89 previous days plus the current day (API) and "últimos 90 dias" (Manual) describe the same `d90` period, so they are not a divergence.

## 14. Conversion to `transaction-features-v1`

Conversion applies only to a valid scoring input. It returns either the four features of `transaction-features-v1` or `insufficient_input`. It never returns partial features.

### 14.1 Consumed information and maximum age

| Path | Maximum age |
|---|---|
| `/observations/payer_account/initiated_outgoing_pix_24h` | 5 minutes |
| `/observations/payer_account/settled_outgoing_pix_90d` | 24 hours |
| `/observations/recipient_person/settled_pix_from_payer_account_365d` | 24 hours |
| `/observations/payer_device/settled_outgoing_pix_90d` | 24 hours |
| `/pix_payment/amount_cents` | Not applicable |
| `/pix_payment/payer_device_usage` | Not applicable |

An observation is fresh when `decision_time − as_of ≤ maximum age`. The limit is inclusive.

DICT observations are not consumed by `transaction-features-v1` and never block conversion, whether they are `null` or contain `null` members. A later feature schema that consumes them declares which paths it needs.

### 14.2 Insufficient input

Conversion checks all consumed information and collects every blocker. Each blocker has a `path`, which is a JSON Pointer, and a `reason` from this closed catalog:

| Reason | When | Path |
|---|---|---|
| `missing` | A consumed observation is `null`. | The observation, for example `/observations/payer_device/settled_outgoing_pix_90d`. |
| `stale` | A consumed observation is known but older than its maximum age. | Its `as_of`, for example `/observations/payer_account/initiated_outgoing_pix_24h/as_of`. |
| `no_history` | `settled_outgoing_pix_90d.count` is `0`, so no typical amount exists. | `/observations/payer_account/settled_outgoing_pix_90d/median_amount_cents` |
| `not_applicable` | `payer_device_usage` is `no_payer_device`, so `NewDevice` does not apply to how the payment was authorized. | `/pix_payment/payer_device_usage` |

`not_applicable` is not a second kind of missing: the scoring input is valid and complete, but a feature the preserved model requires does not apply to the payment.

Blockers are sorted by the UTF-8 bytes of `path`, then by the UTF-8 bytes of `reason`. When any blocker exists, conversion returns `insufficient_input` with all blockers. The model does not run, and no score is produced. Values are never imputed: missing is never turned into zero, false, an average, or any other value.

With this feature schema, some populations, such as new accounts and payments without a payer device, receive no score and depend on the integrator's own rules. This is a known limitation of `transaction-features-v1` (ADR 0004).

### 14.3 Feature mapping

Features are IEEE 754 binary64 values, computed in the order shown:

| Index | Feature | Value |
|---|---|---|
| 0 | Amount ratio | `(amount_cents / settled_outgoing_pix_90d.median_amount_cents) / 10` |
| 1 | Recent count | `initiated_outgoing_pix_24h.count / 20` |
| 2 | `NewRecipient` | `1` if `recipient_person.settled_pix_from_payer_account_365d.count` is `0`, otherwise `0` |
| 3 | `NewDevice` | `1` if `payer_device.settled_outgoing_pix_90d.count` is `0`, otherwise `0` |

The scaling constants and the operation order are those of `transaction-features-v1`, so a scoring input and the equivalent legacy values produce bit-identical features and scores. `NewDevice` does not mean an unregistered device in the regulatory sense.

## 15. Complete normative example

A valid scoring input with every subject present. It is also [`examples/01-complete.json`](examples/01-complete.json). Its features are the legacy values of the README's `cmd/predict` example, so its score is `0.18734535214416673`, printed as `0.187345` with six decimal places.

```json
{
  "contract_version": "pix-payer-input-v1",
  "decision_time": "2026-10-13T14:03:07.120-03:00",
  "pix_payment": {
    "amount_cents": 250000,
    "payer_device_usage": "payer_device",
    "recipient_key_usage": "pix_key",
    "recipient_person_type": "NATURAL_PERSON"
  },
  "observations": {
    "payer_account": {
      "settled_outgoing_pix_90d": {
        "count": 31,
        "median_amount_cents": 25000,
        "as_of": "2026-10-13T06:00:00Z"
      },
      "initiated_outgoing_pix_24h": {
        "count": 2,
        "as_of": "2026-10-13T17:03:06.874Z"
      }
    },
    "payer_device": {
      "settled_outgoing_pix_90d": {
        "count": 14,
        "as_of": "2026-10-13T06:00:00Z"
      }
    },
    "recipient_person": {
      "settled_pix_from_payer_account_365d": {
        "count": 0,
        "as_of": "2026-10-13T06:00:00Z"
      },
      "spi": {
        "settlements": {
          "d90": 12,
          "m12": 40,
          "m60": 95
        },
        "as_of": "2026-10-13T17:03:06.105Z"
      },
      "fraud_markers": {
        "application_frauds": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "mule_accounts": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "scammer_accounts": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "other_frauds": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "unknown_frauds": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "distinct_fraud_reporters": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "as_of": "2026-10-13T17:03:06.105Z"
      },
      "infraction_reports": {
        "open_reports": 0,
        "open_reports_distinct_reporters": 0,
        "rejected_reports": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "as_of": "2026-10-13T17:03:06.105Z"
      },
      "entries": {
        "registered_accounts": 2,
        "as_of": "2026-10-13T17:03:06.105Z"
      }
    },
    "recipient_account": {
      "entry": {
        "opening_date": "2019-05-14T03:00:00Z",
        "as_of": "2026-10-13T17:03:05.912Z"
      }
    },
    "recipient_pix_key": {
      "entry": {
        "key_type": "EVP",
        "creation_date": "2025-07-19T10:02:45.318Z",
        "key_ownership_date": "2023-02-08T19:41:12.004Z",
        "as_of": "2026-10-13T17:03:05.912Z"
      },
      "spi": {
        "settlements": {
          "d90": 9,
          "m12": 31,
          "m60": 70
        },
        "as_of": "2026-10-13T17:03:06.105Z"
      },
      "fraud_markers": {
        "application_frauds": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "mule_accounts": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "scammer_accounts": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "other_frauds": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "unknown_frauds": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "distinct_fraud_reporters": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "as_of": "2026-10-13T17:03:06.105Z"
      },
      "infraction_reports": {
        "open_reports": 0,
        "open_reports_distinct_reporters": 0,
        "rejected_reports": {
          "d90": 0,
          "m12": 0,
          "m60": 0
        },
        "as_of": "2026-10-13T17:03:06.105Z"
      },
      "entries": {
        "distinct_accounts": {
          "d90": 1,
          "m12": 1,
          "m60": 2
        },
        "as_of": "2026-10-13T17:03:06.105Z"
      }
    }
  }
}
```

The entry data and the statistics come from two DICT responses, `getEntry` and `getEntryStatistics`, so they have two different `as_of` values. The payer history comes from a nightly snapshot of the PSP's own database, generated at 06:00Z, and the 24-hour count from a live counter.

## 16. Examples

Every example is a file in [`examples/`](examples/), paired with a `.expected.json` file that holds the expected `cmd/predict` result ([section 17](#17-reference-command-output)). Tests compare parsed JSON values, not bytes. For invalid inputs, expected issues list `path` and `code` only, because `message` is not part of the contract.

Examples 02 to 08 each change one aspect of example 01, so that no two cases share an implicit interpretation.

| File | Case | Status | Expected result |
|---|---|---|---|
| `01-complete.json` | Every subject present and known. | `scored` | Score `0.18734535214416673`. |
| `02-dict-group-missing.json` | `recipient_pix_key.fraud_markers` is `null`: the whole DICT group is missing. | `scored` | Same score as 01, because DICT observations are not consumed. |
| `03-dict-member-missing.json` | `recipient_pix_key.fraud_markers.mule_accounts` is `null`: one member is missing inside a known group. | `scored` | Same score as 01. |
| `04-dict-window-missing.json` | `recipient_pix_key.fraud_markers.mule_accounts.d90` is `null`: one window is missing inside a known window counter. | `scored` | Same score as 01. |
| `05-known-zero.json` | `payer_device.settled_outgoing_pix_90d.count` and `recipient_person.settled_pix_from_payer_account_365d.count` are a known `0`, so `NewDevice` and `NewRecipient` are `1`. | `scored` | Score `0.9961758913026726`. |
| `06-integrator-observation-missing.json` | `payer_device.settled_outgoing_pix_90d` is `null`. | `insufficient_input` | `missing` at `/observations/payer_device/settled_outgoing_pix_90d`. |
| `07-median-semantic-null.json` | `settled_outgoing_pix_90d` has `count` `0` and `median_amount_cents` `null`: a known, empty history. | `insufficient_input` | `no_history` at `/observations/payer_account/settled_outgoing_pix_90d/median_amount_cents`. |
| `08-no-pix-key.json` | `recipient_key_usage` is `no_pix_key`, so `recipient_pix_key` is omitted. A manual Pix to another PSP, so `recipient_account.entry` is `null`. | `scored` | Score `0.0008914722633056818`. |
| `09-no-payer-device.json` | `payer_device_usage` is `no_payer_device`, so `payer_device` is omitted. | `insufficient_input` | `not_applicable` at `/pix_payment/payer_device_usage`. |
| `10-multiple-blockers.json` | The 24-hour count is 6 minutes old, and the device observation is `null`. | `insufficient_input` | `stale` at `/observations/payer_account/initiated_outgoing_pix_24h/as_of`, then `missing` at `/observations/payer_device/settled_outgoing_pix_90d`. |
| `11-invalid-unknown-member.json` | The device observation is misspelled `settled_outgoing_pix_90days`. | `invalid_input` | `required` at `/observations/payer_device/settled_outgoing_pix_90d`, then `unknown_member` at `/observations/payer_device/settled_outgoing_pix_90days`. |
| `12-invalid-negative-zero.json` | `amount_cents` is `-0`. | `invalid_input` | `invalid_number_format` at `/pix_payment/amount_cents`. |
| `13-invalid-as-of-after-decision-time.json` | The 24-hour count's `as_of` is 1 ms after `decision_time`. | `invalid_input` | `as_of_after_decision_time` at `/observations/payer_account/initiated_outgoing_pix_24h/as_of`. |
| `14-invalid-key-subject-not-allowed.json` | `recipient_key_usage` is `no_pix_key`, but `recipient_pix_key` is present. | `invalid_input` | `member_not_allowed` at `/observations/recipient_pix_key`. |
| `15-invalid-key-ownership-after-creation.json` | `key_ownership_date` is later than `creation_date` but not later than `as_of`. | `invalid_input` | `key_ownership_after_creation` at `/observations/recipient_pix_key/entry/key_ownership_date`. |
| `16-invalid-unsupported-version.json` | `contract_version` is `pix-payer-input-v2`, and the document also has a member unknown to v1. | `invalid_input` | Only `unsupported_contract_version` at `/contract_version`. |
| `17-invalid-duplicate-member.json` | `count` appears twice in `initiated_outgoing_pix_24h`. | `invalid_input` | Only `duplicate_member` at `/observations/payer_account/initiated_outgoing_pix_24h/count`. |

## 17. Reference command output

`cmd/predict` reads one scoring input from the file named by its only argument, or from standard input when there is no argument or the argument is `-`. It validates the input, converts it, and scores it with `model.json`.

For exit codes 0, 2, and 3, standard output holds exactly one compact JSON object followed by a newline, and nothing else. Lists keep the ordering defined above. Logs never go to standard output.

| Exit code | Status | Output |
|---|---|---|
| 0 | `scored` | `{"status":"scored","contract_version":"pix-payer-input-v1","feature_schema":"transaction-features-v1","score":0.18734535214416673}` |
| 2 | `invalid_input` | `{"status":"invalid_input","issues":[{"path":"/pix_payment/amount_cents","code":"out_of_range","message":"amount_cents must be at least 1"}]}` |
| 3 | `insufficient_input` | `{"status":"insufficient_input","blockers":[{"path":"/pix_payment/payer_device_usage","reason":"not_applicable"}]}` |
| 1 | None | Standard output is empty. A human-readable message goes to standard error. |

- The score uses the shortest decimal representation that round-trips the binary64 value, as produced by the Go standard library's JSON encoding. No presentation rounding or fixed number of decimals is applied.
- The score is bit-identical on every CPU architecture: prediction rounds each product before adding it, so no architecture fuses the two operations into a single rounding.
- The four features stay available in the internal conversion result, but `cmd/predict` does not print them.
- Exit code 1 covers operational and internal failures: an unreadable input file, a missing or invalid model, wrong command-line usage, or an internal error. It is never used for problems that belong to the input contract, which always produce exit code 2 or 3.

## 18. Migration notes

- **M1.** The feature schema identifier `transaction-features-v1` contains "transaction", a term the glossary avoids. It stays unchanged, because saved models record it.
- **M2.** `transaction.Features` accepts an amount of `0`, while this contract requires at least `1`. Conversion never produces `0`, and the legacy check stays as it is.
- **M3.** The legacy `transaction.Transaction` type cannot represent missing information. It remains an internal detail of the adaptation to `transaction-features-v1`, is built only when all consumed information is usable, and is not part of the public contract.
- **M4.** `cmd/predict` stops scoring a hard-coded value and reads a scoring input instead, with the output of [section 17](#17-reference-command-output). `cmd/demo`, its training data, and `model.json` stay unchanged.
