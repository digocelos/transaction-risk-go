# Resource limits are deterministic runtime guards, outside the model configuration

The toolkit refuses work beyond a supported envelope instead of implying unlimited scale. Full-batch training keeps every usable record in memory and passes every train record through the network in every epoch, so memory grows with the records and time with the records, the parameters, and the epochs. The limits are checked on counts and sizes, which are deterministic, never on measured time or memory:

- **Records per dataset** (`-max-records`, default 1,000,000): reading stops at the first record beyond the limit, before it is decoded. It applies to training, evaluation, and reference datasets, and it bounds memory and the size of the reports.
- **Training work** (`-max-training-work`, default 10^12): the parameters times the samples passed through the network, train records times `max_epochs` plus validation records times the validation evaluations. A run above it is refused after reading and before training.
- **File sizes**: a model artifact is at most 16 MiB, more than twice the largest architecture; a run's manifest, model, and configuration are read only within their limits, and no file is read past its manifest entry.

The limits depend on the machine, not the model, so they are command-line flags and API options rather than members of the training configuration: raising one never changes a configuration's identity. Their defaults come from measurements of synthetic workloads on reference hardware, recorded with the commands that reproduce them in `docs/operations/`.

Cancellation stays responsive within a documented bound: reading checks it every 1,024 records, training before every epoch and every 1,024 samples within an epoch, and scoring a partition every 1,024 records. Scoring one input is bounded CPU work and is not cancelable (ADR 0018).

The toolkit supports 64-bit platforms only, and does not build where `int` has 32 bits, because counts and sums up to 2^53−1 are held in `int`. CI runs on linux/amd64 and darwin/arm64. Scores are reproducible bit for bit on one platform and agree within 10^−9 across architectures.

## Considered options

- **Limits in the training configuration:** auditable in the report, but a change of machine would change the configuration's identity, and a new configuration version would be needed.
- **Measure memory or time while running:** closer to the real constraint, but nondeterministic: the same run could pass on one attempt and fail on the next, and CI assertions on time are fragile.
- **Support 32-bit platforms:** would need every count to be checked against a 32-bit `int`, for no known use.
- **Mini-batch training now:** removes the memory bound of full batch, but changes the algorithm, the configuration, and the results; it is follow-up work.

## Consequences

- `training.Run` and `training.PrepareDataset` take limits, `trainingdata.Scan` takes a record limit, and `modelevaluation.Options` has `MaxRecords`; every caller states them, with `training.DefaultLimits` and `trainingdata.DefaultMaxRecords` as the supported envelope.
- `nn.Network.TrainBatchContext` makes an epoch cancelable without changing parameters.
- Datasets beyond the envelope need a different training algorithm, not a larger limit.
