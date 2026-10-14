# Trained models carry compact provenance and reference a separate report

A model trained from a dataset is saved in model format version 2. It contains everything format version 1 contains, plus a `provenance` object: the training algorithm, seed, epochs, learning rate, training example version, the dataset's SHA-256 and byte count, its record counts by category, the temporal boundaries, the decision threshold, and the SHA-256 of the training report. Evaluation details, such as the dataset report with every exclusion, the partition counts, and the validation and test metrics, live only in that separate, deterministic training report (`pix-training-report-v1`), which the model binds by hash.

The inference artifact therefore states which dataset bytes and configuration produced it, without carrying audit detail that inference never reads. No value comes from the system clock, so identical inputs produce identical artifacts.

## Considered options

- **Format version 1 plus a report:** keeps the model format unchanged, but a model separated from its report would no longer say where it came from.
- **Everything in the model:** a single file, but the inference artifact would grow with exclusion lists and curves and mix audit data with parameters.

## Consequences

- `nn.Load` keeps reading format version 1, including the committed `model.json`, exactly as before, and also reads format version 2. A format version 2 file is decoded strictly, and malformed or out-of-range provenance makes loading fail. Unknown format versions fail as before.
- `cmd/predict` accepts both formats, and its output does not change.
- Metrics appear only in the report. Anyone holding a model checks a report against it by recomputing the report's SHA-256.
- The report must be written before the model, because the model records the report's hash.

Refined by [ADR 0015](0015-selection-uses-validation-only-and-test-is-explicit.md): the report is now `pix-training-report-v2`, and the provenance records the selected epoch and threshold.
