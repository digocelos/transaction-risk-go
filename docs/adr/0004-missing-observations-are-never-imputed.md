# Missing observations are never imputed

An observation is either known or missing. Missing also applies to an individual provider value, such as one DICT counter or one window of it, when the source allows that value to be omitted, so that the known values around it are kept. A known zero or a known false is not missing, and missing carries no reason: why a value is absent (not collected, provider failure) belongs to the integrator's operational observability, not to the scoring input. A scoring input can be valid without containing everything a feature schema needs. In that case, conversion to features returns insufficient input, listing the paths of the fields it needs, in deterministic order, and the model does not run. We rejected imputing zero, false, or an average because a score built on guessed values looks as trustworthy as one built on facts. For the same reason, an omitted provider value, a partial response, or a failed query is never turned into a zero count.

Not applicable is not missing. A known fact that makes information not applicable, such as a Pix payment made without a Pix key or without a payer device, is stated by a closed discriminator in the Pix payment, and the subject it rules out is omitted. It is never stated with `null`. When a feature the schema requires does not apply, conversion reports it as insufficient input with the reason `not_applicable`, which is not a second kind of missing.

## Consequences

With the current four-feature schema (`transaction-features-v1`), populations such as new accounts can receive no score and must rely on the integrator's own rules. This is a known limitation of that feature schema, not a permanent property of the toolkit; later feature schemas can model absence explicitly.
