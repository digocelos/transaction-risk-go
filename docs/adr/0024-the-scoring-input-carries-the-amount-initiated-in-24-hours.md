# The scoring input carries the amount initiated in 24 hours, and the contracts move to version 3

A transfer split into many small payments lowers each payment's amount feature, and the 24-hour window of `pix-payer-input-v2` carried only a count, which did not restore the risk: on the second scenario dataset, 14 % of the detected frauds fell below the threshold after a 10-way split. The toolkit cannot compute the amount initiated in the window from what it receives, so the integrator supplies it: `pix-payer-input-v3` adds `amount_cents` to `initiated_outgoing_pix_24h`, required, 0 exactly when the count is 0, and `pix-risk-v3` appends two features that read it, the amount initiated and the ratio of the payment to it, with an indicator for the ratio. The training example, the CSV layout, the template, and the committed scenario dataset follow, as `pix-training-example-v3`, `pix-training-csv-v2`, and `pix-training-scenarios-v3`.

Each new version replaces the previous one without coexistence, as ADR 0013 and ADR 0023 did: a build implements one input contract, one training example, one CSV layout, and one feature schema. The artifact format, `pix-risk-model-v2`, keeps its layout and records the schema it carries, so an artifact of `pix-risk-v2` is rejected on load and its model retrained; the configuration contract keeps its version and names `pix-risk-v3` as the schema the toolkit implements (ADR 0022).

## Considered options

- **Derive the amount from the count:** impossible; the count says nothing about sizes.
- **Make `amount_cents` optional:** an integrator that cannot sum amounts would send `null`, and the model would learn that the absence of a required fact is normal; the window is a required fact in version 2, and its amount is too.
- **A separate observation for the amount, with its own `as_of`:** two observations of one window could disagree on their instants; one observation with two members cannot.
- **Keep the version 1 and 2 datasets convertible:** `cmd/convert` would need to invent the amount; the datasets remain in the repository history instead.

## Consequences

- Every specification example, fixture, and dataset carries the member; the expected results of the examples gain three features and nothing else changes in them.
- The committed datasets `pix-training-scenarios-v1.csv` and `pix-training-scenarios-v2.csv` leave the tree, because the toolkit can no longer convert them; their README sections and the commits that added them remain, and version 3 is version 2 with the column filled.
- The network's input size is 65; configurations, inspection, and the artifact's `sizes` follow the schema's feature count.
