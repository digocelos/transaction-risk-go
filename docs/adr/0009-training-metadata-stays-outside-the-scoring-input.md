# Training-only metadata stays outside the scoring input

A training example embeds the exact `pix-payer-input-v1` scoring input that existed at decision time, unchanged, and puts everything known only afterwards in a separate envelope: the outcome (label, label as-of time, and loss) and an optional group key. The envelope never reaches scoring, never becomes a feature, and never changes the scoring input contract. Training therefore sees exactly what production scoring saw, the same decoder validates both, and labels cannot leak into features through a shared shape.

We rejected adding outcome members to the scoring input, which would have created a scoring contract that sometimes carries labels, and rejected a flat training schema with its own copy of the observations, which would drift from the production contract.

## Consequences

- The group key is the only pseudonymous value the toolkit accepts. ADR 0007 still holds for scoring inputs; the key is allowed only in the training envelope, as an opaque, dataset-local value governed by the dataset owner, and only for partitioning and leakage analysis.
- A training example is invalid whenever its embedded scoring input is invalid, and it is insufficient whenever the feature schema being trained cannot convert that input.
- Changing what training needs beyond the scoring input requires a new training example version, independent of the scoring input contract version (ADR 0003).
