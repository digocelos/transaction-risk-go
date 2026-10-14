# Saved models are evaluated read-only on an explicit scope

Evaluating a saved model is a separate operation from training. `cmd/evaluate` reads a `pix-risk-model-v1` artifact by its exact bytes and a `pix-training-example-v2` dataset, and scores the usable records of one explicit scope with the artifact's own preparation state, network, and threshold. It never trains, refits the preparation state, selects a checkpoint, or tunes a threshold, and it writes only its report, never over the model or a dataset.

The scope is a partition of the artifact's boundaries, `validation` or `test`, or a window of decision times that starts at or after the artifact's `train_end`, so the train partition is never evaluated. A threshold override applies to one evaluation only and is labeled in the report; the artifact keeps its threshold. Group overlap is measured against the usable train records of the training dataset: the evaluated dataset when its SHA-256 is the artifact's `dataset_sha256`, or a reference dataset with that SHA-256.

An evaluation whose scope has no usable records, lacks a class, or has frauds with unknown losses is incomplete. Its report names each reason and the metrics it affects, and `cmd/evaluate` still writes the report but exits with code 3, so a script cannot take it for a complete evaluation. A complete evaluation is not a quality pass.

## Conflict with the test protocol

[`pix-training-example-v2` §11.2](../contracts/pix-training-example-v2.md#112-roles) says the test partition is evaluated exactly once. `cmd/evaluate` can score a model's test partition any number of times, and the issue requires it to reproduce the training run's test metrics. Rescoring a frozen model reproduces the same numbers and changes nothing, so it does not break the rule's purpose: test results must not change a model. That purpose cannot be enforced by software. A person can look at held-out results repeatedly and change a model afterwards; the documentation says so, and a changed model needs new held-out data. The protocol rule stays as written, with a note pointing here.

## Considered options

- **Allow windows before `train_end` and label them:** would let another dataset be evaluated over the training period, but reports metrics on training examples whenever the dataset is the training dataset, which the protocol forbids.
- **Exit with code 0 for incomplete evaluations:** simpler for callers that read the JSON, but `evaluate && promote` would pass on an evaluation with no fraud or no legitimate records.
- **Fail on a single-class scope:** clearer than a flag, but drops the counts, coverage, and partial metrics a reviewer needs to see why the scope cannot support a full evaluation.
- **Overlap only with the evaluated dataset:** no extra input, but a new dataset could never show leakage through shared group keys.

## Consequences

- An artifact evaluated on its training dataset's validation and test partitions reproduces the training report's metrics exactly.
- `cmd/evaluate` exits with 0 for a complete evaluation, 1 for a failure, 2 for an invalid dataset, and 3 for a written but incomplete evaluation.
- Evaluation reads every record into the dataset report and keeps the usable records in scope in memory; operating limits are #32.
