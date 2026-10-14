# Version 2 contracts replace version 1 without a coexistence period

`pix-payer-input-v2` and `pix-training-example-v2` replace `pix-payer-input-v1` and `pix-training-example-v1`, and the feature schema `pix-risk-v1` replaces `transaction-features-v1`. The toolkit stops accepting the version 1 identifiers instead of supporting both. Version 1 served an educational workflow with no production integrators or saved models to preserve, and keeping both would double the decoders, examples, and preparation paths while the toolkit is still being built.

The versions stay separate (ADR 0003): the contract, the training example, the feature schema, the training configuration, the training report, and the model artifact format each keep their own identifier.

## Consequences

- Inputs, datasets, and models built for version 1, including the committed `model.json`, stop working when the migration ends; the migration is listed in the `pix-risk-v1` specification.
- During the migration, each consumer moves to version 2 in its own issue, and the decoders accept only the versions their caller states, so no command silently accepts both. Version 1 is removed with the educational workflow (#33).
- Every later change still follows ADR 0006: a new optional member is a new contract version.
