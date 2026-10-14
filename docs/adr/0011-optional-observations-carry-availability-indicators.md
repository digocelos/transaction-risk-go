# Optional observations carry explicit availability indicators

Starting with the feature schema `pix-risk-v1`, information a feature schema consumes is either a required fact or an optional observation. A required fact that is missing or stale still makes the input insufficient, and no score is produced. An optional observation that is missing, stale, or not applicable no longer blocks scoring: the features it feeds take the value 0 together with an availability indicator of 0, and a discriminator indicator marks what is not applicable. After standardization, an unavailable feature is still exactly 0. The neutral value never appears without the indicator that says it is not a fact, so nothing is imputed silently, and preparation reports every unavailable source with its own status, keeping missing, stale, not applicable, no history, and a known zero limit distinguishable.

This amends the consequence of ADR 0004 that any missing or not applicable information blocks scoring. ADR 0004's rule stands: a missing value is never turned into a zero, a false, or an average that the model would read as a fact.

## Considered options

- **Block on everything consumed**, as `transaction-features-v1` does: new accounts, payments without a key or device, and partial DICT responses receive no score at all.
- **Impute** zero or a typical value: the model could not tell a guessed value from a fact, which ADR 0004 rejects.

## Consequences

- New accounts, payments without a Pix key or payer device, payments without a recipient account entry, and payments without a limit per period can be scored when their required facts are present and fresh.
- The model can learn from absence only if training data contains it, so training data must exercise every availability state, and the training report shows that coverage. Training stops when a feature is never available in the train partition.
- Missing and stale optional observations look the same to the model; the preparation notes keep them apart for the integrator.
