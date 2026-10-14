# Scoring loads an explicit artifact once into an immutable scorer

Scoring takes the model from an explicit `pix-risk-model-v1` artifact, never from an implicit path. The `scoring` package loads and validates the artifact once into a `Scorer`, which scores any number of `pix-payer-input-v2` documents: it validates each document, prepares its `pix-risk-v1` features with the artifact's frozen preparation state, and runs the network. It never trains, refits, or looks anything up. `cmd/predict` requires `-model` and uses the same scorer, so the command and a Go integration score identically, and both score exactly as training prepares features.

A `Scorer` never changes after loading and does not expose the artifact's parameters or preparation state, so it is safe for concurrent use. Scoring one document is bounded CPU work without I/O, so it takes no context; callers cancel between calls.

Every result identifies the loaded artifact by the SHA-256 of its exact bytes, its feature schema, and the threshold it was selected with. Invalid and insufficient inputs are results with their issues or blockers, not errors. The threshold is metadata: no result approves or rejects a payment (ADR 0002), and the score is not called a probability, because nothing has shown it to be calibrated.

## Considered options

- **Keep `model.json` as the default model path:** convenient, but a command could silently score with whatever file happened to be in the working directory, and the committed educational model cannot score `pix-risk-v1` inputs anyway.
- **Pass a context to `Score`:** every call would accept cancellation, but a check before microseconds of CPU work cancels nothing in practice.
- **Return the decision at the stored threshold:** easy for integrators, but it would turn a model's selection metadata into an operational decision the toolkit does not own.
- **A JSON Lines batch mode in `cmd/predict`:** loads the model once for many inputs from other languages, but adds a per-batch exit code; Go integrations already load once.

## Consequences

- `cmd/predict` no longer reads `model.json` or `pix-payer-input-v1`. Version 1 inputs are invalid with `unsupported_contract_version`, and the educational model and demo stay only until they are removed (#33).
- The output of `cmd/predict` adds the model identity and the preparation notes, and moves `feature_schema` into `model`.
- CI runs the race detector on the scorer and the command.
