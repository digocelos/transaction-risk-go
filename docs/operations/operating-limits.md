# Operating limits

This document states what the toolkit supports: the platforms, the limits it enforces, the memory and time it needs, how quickly it stops when canceled, and where full-batch training ends. The numbers are measurements of synthetic workloads on one reference machine ([measurements](measurements/2026-10-14-darwin-arm64.md)). They describe software behavior, not a service-level objective, and they say nothing about fraud detection quality (ADR 0019).

## 1. Supported platforms

| | Supported |
|---|---|
| Operating systems and architectures | linux and darwin on amd64 and arm64. CI runs every test on linux/amd64 and darwin/arm64. Other 64-bit platforms, such as windows/amd64, build but are not tested. |
| 32-bit platforms | Not supported, and the toolkit does not build there: counts and sums of integers up to 2^53−1 are held in `int`. |
| Go | The version in `go.mod`, 1.27, or later. |

**Floating point.** The same dataset bytes, configuration, and build produce the same model and report bytes, and the same artifact produces the same scores, on one platform. Across architectures, fused multiply-add and `math.Exp` differ in their last bits, so scores agree within 10^−9, which CI checks on amd64 against scores recorded on arm64; on the bundled fixture they differ by about 6·10^−18. Compare scores across machines with that tolerance, never with `==`. A computation that would produce a non-finite value fails instead: decoded integers above 2^53−1 are invalid inputs, feature preparation stays finite at the largest valid values, a standardized feature that overflows is an error, training stops without changing parameters when a gradient or update is not finite, and metric sums above 2^53−1 fail the evaluation.

## 2. Enforced limits

| Limit | Default | Set with | When exceeded |
|---|---|---|---|
| Records per dataset | 1,000,000 | `-max-records` of `train` and `evaluate`; `training.Limits.MaxRecords`, `modelevaluation.Options.MaxRecords`, `trainingdata.Scan` | Reading stops at the first record beyond the limit, before it is decoded. Applies to training, evaluated, and reference datasets. Nothing is written. |
| Training work | 10^12 | `-max-training-work` of `train`; `training.Limits.MaxTrainingWork` | Refused after reading the dataset and before the first epoch. Nothing is written. |
| Epochs | 1,000,000 | `max_epochs` of the configuration | The configuration is invalid. |
| Architecture | 4 hidden layers of at most 256 neurons | `hidden_layers` of the configuration | The configuration is invalid. |
| Dataset record | 128 KiB per line | `trainingdata.MaxRecordSize` | The record is invalid (`record_too_large`), and so is the dataset. |
| Scoring input | 64 KiB | `pixinput.MaxDocumentSize` | `invalid_input` with `document_too_large`. |
| Training configuration | 64 KiB | `trainingconfig.MaxDocumentSize` | The configuration is invalid. |
| Model artifact | 16 MiB | `modelartifact.MaxArtifactSize` | Refused without reading past the limit. The largest architecture encodes to 7.1 MB. |
| Run files | Manifest 64 KiB; model and configuration within their limits | `trainingrun.Verify` | Refused before reading. No file is read past its manifest entry; the report grows with the dataset and is bounded through the record limit. |
| Integers in inputs and datasets | 2^53−1 | The contracts | The input or record is invalid (`out_of_range`). |

**Training work** is the number of network parameters times the samples a run passes through the network: train records times `max_epochs`, plus validation records times the validation evaluations of a `best_validation` checkpoint, ⌈`max_epochs` / `evaluation_interval`⌉. A validation pass runs forward only, so the count is conservative. For example, `[62, 4, 1]` has 257 parameters; 650,000 train records and 100 epochs are 1.7·10^10 units.

Every limit is a count or a size, so a run that passes once passes again; nothing depends on measured time or memory. Raise a limit only on a machine measured as in [section 6](#6-reproducing-the-measurements), and remember that full batch keeps everything in memory ([section 5](#5-where-full-batch-training-ends)).

## 3. Memory and time

Ranges are the fastest and slowest of the measured runs; the workstation was shared, so the slower runs had other work competing for it.

| Operation | Measured |
|---|---|
| Reading and classifying a dataset | 33 to 135 µs per record, 18 to 74 MB/s, in constant memory. |
| Preparing a dataset for training | 39 to 202 µs per record. Each usable record keeps 1.33 KB of live heap. |
| One epoch, `[62, 4, 1]` | 0.46 to 2.1 µs per train sample: 1.8 to 8.1 ns per unit of training work. |
| One epoch, `[62, 256, 256, 256, 256, 1]` | 0.66 to 1.4 ms per train sample: 3.1 to 6.7 ns per unit of training work. |
| Training run, 10,000 records, 100 epochs, `[62, 4, 1]` | 0.87 to 1.48 s. |
| Evaluating a dataset | 81 to 218 µs per record read. |
| Loading an artifact | 0.4 to 0.6 ms for `[62, 4, 1]`; 72 to 152 ms for the largest architecture. |
| Scoring one input | 54 to 80 µs for `[62, 4, 1]` and 0.29 to 0.41 ms for the largest architecture, allocating 42 to 50 KB. One scorer shared by 10 CPUs serves an input every 20 to 31 µs and every 75 to 157 µs. |

End to end, with `train` on `[62, 4, 1]` for 100 epochs:

| Records | `train` wall time | `train` peak memory | `evaluate` wall time | `evaluate` peak memory |
|---|---|---|---|---|
| 100,000 | 21 s | 317 MiB | 12 s | 38 MiB |
| 1,000,000 | 283 s | 2.0 GiB | 166 s | 305 MiB |

Training memory grows linearly with the records, at 2 to 3 KiB of peak resident memory per record read, including the runtime's headroom. The default training work of 10^12 is about 30 minutes to 2 hours of training on the reference machine, depending on the architecture and the load. Evaluation keeps only the usable records in scope, and the dataset report keeps one entry per excluded record.

## 4. Cancellation

`train` and `evaluate` stop on SIGINT or SIGTERM and write nothing. The API takes a `context.Context`. Cancellation is checked:

| Where | Interval | Bound on the reference machine |
|---|---|---|
| Reading a dataset, in training, evaluation, and reference datasets | Every 1,024 records | About 0.2 s. |
| Training | Before every epoch and every 1,024 samples within an epoch; a canceled epoch changes no parameter | About 2 ms on `[62, 4, 1]`; about 1.5 s on the largest architecture. |
| Scoring a partition for selection or metrics | Every 1,024 records | About 0.4 s on the largest architecture. |
| Writing a run or a report | After the work, before anything is written | Immediate. |
| Scoring one input with `scoring.Scorer` | Not cancelable: bounded CPU work without I/O | 0.41 ms on the largest architecture. |

## 5. Where full-batch training ends

Training is full batch: every usable record stays in memory, and every epoch passes the whole train partition through the network. The supported envelope is therefore:

- **Records:** up to 1,000,000 per dataset, which needs about 2 GiB of memory to train. Plan at least 4 GiB of free memory.
- **Training work:** up to 10^12 units, from 30 minutes to a few hours depending on the architecture and the machine.

Beyond it, raising the limits only moves the failure: memory and time keep growing linearly, and a single epoch over tens of millions of records on a large architecture takes hours before the first checkpoint. Larger data needs mini-batch or streaming training, which reads the train partition in batches instead of keeping it in memory. That changes the algorithm, the configuration, and the results, so it needs its own configuration version and its own evaluation; it is follow-up work outside this toolkit's current scope.

## 6. Reproducing the measurements

The workload is synthetic, made by `cmd/synthesize`: decision times from 2025-01-01 to 2026-06-30, labels known as of 2026-08-31, 1% fraud that shifts the amount, the payer's history with the recipient, the payer device, and DICT markers, and the absence patterns of real integrations. In 100,000 records, 2.0% are insufficient (half a stale recent count, half a missing payment history), 5.0% are immature, and among the usable test records:

| Source | Known | Missing | Stale | Not applicable | Zero limit |
|---|---|---|---|---|---|
| `period_limit` | 76.9% | 3.6% | 2.6% | 15.4% | 1.5% |
| `payer_device_history` | 83.3% | 3.6% | 2.8% | 10.3% | |
| `recipient_account_entry` | 93.2% | 4.0% | 2.9% | | |
| `recipient_pix_key_entry` | 74.1% | 3.1% | 2.2% | 20.6% | |
| `person_spi` | 89.8% | 7.2% | 3.0% | | |
| `key_spi` | 71.9% | 5.1% | 2.4% | 20.6% | |

Every supported population and every availability state occurs in the train partition, and every scoring input that is not insufficient is scored with explicit availability indicators and preparation notes. These shares exercise the pipeline; they are not estimates of real data.

Go benchmarks:

```bash
go test -run '^$' -bench . -benchmem -count 3 ./trainingdata/ ./training/ ./modelevaluation/ ./scoring/
```

End to end, measuring each step with `/usr/bin/time -l` (macOS) or `/usr/bin/time -v` (linux):

```bash
work=$(mktemp -d)   # about 3 GB for 1,000,000 records
go build -o "$work/" ./cmd/...
"$work/synthesize" -records 1000000 -seed 1 > "$work/data.jsonl"
"$work/train" -config docs/operations/benchmark-config.json -dataset "$work/data.jsonl" -run-dir "$work/run"
"$work/promote" -run "$work/run" -output "$work/model.json"
"$work/evaluate" -model "$work/model.json" -dataset "$work/data.jsonl" -partition test -output "$work/evaluation.json"
```

CI runs every benchmark once at its smallest size to keep them working, and never asserts a time. Record new measurements in [`measurements/`](measurements/) with the machine, the toolchain, and the exact commands.
