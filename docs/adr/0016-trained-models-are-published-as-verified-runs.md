# Trained models are artifacts published as verified runs and promoted explicitly

A trained model is a `pix-risk-model-v1` artifact: the network, the exact `pix-risk-v1` descriptor and fitted preparation state, the selection, and the identities of the configuration, dataset, report, and software build that produced it. It is enough to score inputs without training. It replaces model format version 2 of ADR 0010, which carried provenance but no preparation state and so could not score `pix-risk-v1` inputs.

`cmd/train` publishes a run directory, not loose files: the artifact, the exact report bytes, the canonical configuration, and a manifest that binds them by SHA-256. The files are written to a staging directory, read back and verified, and published by a single directory rename, so a run exists completely or not at all, and an existing run is never overwritten. Per-file atomic writes cannot guarantee that a model and its report belong together after a failure between two renames.

Promotion is a separate, explicit step: `cmd/promote` verifies a run and copies its artifact atomically to the place a scorer reads it from. A successful training run is not an accepted model.

The SHA-256 values detect accidental or deliberate changes; they are not signatures and do not authenticate who produced a model.

## Considered options

- **Extend model format 2 with the preparation state:** would keep the network serialization in `nn`, but mix the generic network with `pix-risk-v1` concerns, and still leave model and report as loose files.
- **Write the manifest last as the publication marker:** cheaper than a staging directory, but readers would have to treat any directory without a manifest as incomplete, and a failed run would still leave files behind.
- **Promote at the end of training:** fewer steps, but any run that finishes would replace the model in use without review.

## Consequences

- `nn` reads only model format version 1, for the educational demo, until it is retired (#33).
- The artifact records the software build from Go build information; binaries built outside a VCS checkout record `unknown`.
- Scoring with an artifact is the scoring API of #31.
