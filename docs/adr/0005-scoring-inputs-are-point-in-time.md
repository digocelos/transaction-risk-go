# Scoring inputs are point-in-time

Every scoring input carries a decision time supplied by the integrator. The toolkit never reads the system clock to give meaning to an input, so the same input always produces the same result, and historical decision times can be replayed for training and evaluation.

## As-of time

Each historical or external observation carries an as-of time: the instant up to which the integrator asserts that the observation is a known, usable snapshot. The source's documented semantics decide the as-of time, not the name of a field:

- For a direct DICT query, the as-of time is the `ResponseTime` of the DICT response, which DICT API v2.12.1 always provides.
- For the PSP's own database, the as-of time is the real instant the consulted snapshot was generated or last completely updated, never the instant the integrator read it.
- A query instant is the as-of time only when the source guarantees that its data was complete up to that instant.
- A provider watermark is the as-of time only when the provider explicitly defines it as the limit of data completeness. A DICT group watermark marks the last event DICT considered in that group, so it never sets an as-of time or measures freshness.

An as-of time later than the decision time is rejected with zero tolerance, because it may leak future information. Integrators correct clock skew before they build the input.

## Windows

Contract-defined lookback windows apply to observations the integrator computes, for example over 24 hours, 90 days, or 365 days. They are rolling, independent of time zones, and cover `(as_of - duration, as_of]`.

Provider-defined windows arrive already aggregated and keep the provider's semantics exactly. DICT's `d90`, `m12`, and `m60` follow DICT's definition exactly, and `m12` and `m60` keep excluding the current month. DICT's worked example places the boundaries at 03:00Z, but the documentation does not state Brasília time as a general rule, so the contract does not turn the example into one. Neither the integrator nor the toolkit converts or recalculates them, and the contract keeps the provider's names so they are not mistaken for rolling windows.

Local-calendar rules, such as the regulatory night period, belong to the rules layer (ADR 0002).

## Normative sources for `pix-payer-input-v1`

- DICT API v2.12.1, 2026-07-14.
- Manual Operacional do DICT v8.5, 2026-09-01.
- Regulamento Pix, consolidated version 43.

The DICT API defines the names, structure, types, requiredness, representation, and semantics of the data the integrator receives. The Manual complements it with operational behavior, delays, usage rules, and anything the API leaves undefined, and the Regulamento defines regulatory obligations. When the API and the Manual diverge, the contract follows the API and records the divergence in its specification.

Release candidates, such as DICT API v2.13.0, and the archived `bacen/pix-dict-api` repository are not normative sources. A new DICT version requires a new contract version only when it materially changes the semantics, structure, or unit of a field the contract uses.
