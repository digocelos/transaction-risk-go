# The educational workflow and version 1 are removed; history keeps them citable

The toolkit started as an educational workflow: `cmd/demo` trained a four-feature network on fictional data, a `model.json` was committed at the repository root, and `cmd/predict` scored with it. Once training, selection, packaging, evaluation, and scoring worked end to end on version 2 (ADR 0013, ADR 0016, ADR 0017, ADR 0018), the educational workflow had no consumer left. It is removed: `cmd/demo`, the root `model.json`, the `transaction` package and its feature schema `transaction-features-v1`, the `nn` model format version 1, the `pix-payer-input-v1` and `pix-training-example-v1` contracts, their decoders, and their examples. No command or test depends on them, and every example uses an explicit model path and a generated artifact.

The version 1 documents are deleted from the tree instead of being archived beside the current ones. The version 2 documents describe some changes as differences from version 1, so they link to the version 1 documents at the last commit that contained them, `6babe7c`, where they stay readable and citable with the repository history unchanged.

## Considered options

- **Archive the version 1 documents in the tree:** navigable offline, but keeps contracts that no code accepts next to the normative ones, where a reader can mistake them for supported.
- **Rewrite the version 2 documents to stand alone:** the cleanest result, but it rewrites normative sections that are already implemented and tested, with a risk of changing their meaning for no functional gain.
- **Keep the demo as a tutorial:** easy to try, but it taught a model format, a feature schema, and a scoring path that the toolkit no longer uses.

## Consequences

- `pixinput` accepts `pix-payer-input-v2` only, still through the versions its caller states (ADR 0006), and `trainingdata` reads `pix-training-example-v2` only.
- The neural network keeps its numerical tests; its parameters are saved only inside a `pix-risk-model-v1` artifact.
- Tests that covered shared validation rules through version 1 documents cover them through version 2 documents.
- An end-to-end test builds every command from the checkout and runs the documented workflow on a synthetic dataset.
