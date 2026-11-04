# Training options are versioned by the configuration, and the files that record them keep their layouts

A new training option, such as the initialization scheme, the loss, weight decay, or availability dropout, is added by a new configuration version, with a value that reproduces the previous version exactly. The toolkit accepts every configuration version it implements: a `pix-training-config-v2` document keeps decoding, keeps its canonical form and `config_sha256`, and trains the same model, bit for bit, while a `pix-training-config-v3` document can set the new options. The training report, the model artifact, and the evaluation and inspection reports record the configuration's version and the option values the configuration defines, in the members they already have, and keep their own layouts and versions. A build that does not implement a configuration version rejects artifacts and runs that name it, as it already rejected unknown versions.

This keeps ADR 0006 for what it protects, the layout of each document: no member is added to the report or the artifact, and an unknown member is still rejected everywhere. The vocabulary of a member that names a configuration option, such as `config_version` or `loss`, belongs to the configuration contract, as the vocabulary of a provider-defined enumeration belongs to the provider (ADR 0008), so a new option value is not a new report or artifact version.

## Considered options

- **One version for everything:** a new training option would bump the report, the artifact, and both evaluation reports, and every tool that reads them, for a change that only training sees.
- **Replace version 2 without coexistence**, as ADR 0013 did for the contracts: every existing run and artifact would stop verifying and loading, and a maintainer could not reproduce an old run with a new build.
- **Add the options to version 2 with defaults:** a document would then mean different things to different builds, against ADR 0014.

## Consequences

- A code-built configuration names its version; the options a version does not have must hold the values that reproduce it, or the configuration is invalid.
- A feature that no train record exercises is a fact about a dataset: `cmd/train` names such features on standard error, and the inspection report lists them, but the training report's layout does not change.
- The artifact's `config_version` and `loss` take the values the supported configuration versions define; the contract documents of the artifact and the report say so instead of naming one version.
