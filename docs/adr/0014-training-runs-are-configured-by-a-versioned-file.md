# Training runs are configured by a strict, versioned file

A training run is configured by a `pix-training-config-v1` document instead of command-line flags: the feature schema, the architecture, the algorithm, the loss, the seed, the learning rate, the number of epochs, the temporal boundaries, and the selection policies. It is decoded as strictly as a scoring input, and it is validated before any dataset is read or any artifact is written. The dataset and the output paths stay command-line flags, because they describe where a run reads and writes, not how it trains.

Every option has only the values the toolkit implements. Validation-based checkpoints, early stopping, and threshold selection are named in the configuration but accepted only once implemented, under a new configuration version, so a configuration never contains an option that is silently ignored.

## Considered options

- **Flags only**, as `cmd/train` had: architecture and selection policies would need many more flags, and a run could not be repeated from one reviewable file.
- **Accept planned options early**: configurations written today would keep working later, but options with no effect would make results look configured when they were not.

## Consequences

- The training report and the model provenance of #13 record the configuration values they already had; the architecture is recorded by the model's layer sizes. Later report and artifact versions can bind the configuration by hash (#28, #29).
- `cmd/train` no longer accepts `-seed`, `-epochs`, `-learning-rate`, `-threshold`, `-train-end`, or `-validation-end`.
