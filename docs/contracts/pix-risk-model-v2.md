# Model artifact `pix-risk-model-v2` and training runs `pix-risk-run-v1`

**Status:** Normative for `pix-risk-model-v2` and `pix-risk-run-v1`. `pix-risk-model-v2` replaces [`pix-risk-model-v1`](pix-risk-model-v1.md), which the toolkit no longer loads (ADR 0023): the preparation state it carries has the train range and the exercised flags of `pix-risk-v2` and later schemas; everything else is unchanged, and `pix-risk-run-v1` is unchanged. The artifact records the feature schema the toolkit implements, [`pix-risk-v3`](../features/pix-risk-v3.md), whose descriptor and feature count it carries; its layout does not change when the schema gains features (ADR 0022, ADR 0024).

## 1. Scope

This specification defines:

- the model artifact `pix-risk-model-v2`: everything the toolkit needs to score `pix-payer-input-v3` inputs with a trained `pix-risk-v3` model, without training, and the identities of what produced it;
- the training run `pix-risk-run-v1`: the directory `cmd/train` publishes, holding the artifact, its report, its configuration, and a manifest that binds them;
- promotion: the explicit step that copies the artifact of an accepted run to the place a scorer reads it from (ADR 0016).

The SHA-256 values in an artifact and a manifest detect accidental or deliberate changes to files. They are not signatures: anyone who can change a file can recompute them, so they prove nothing about who produced a model.

## 2. Model artifact

The artifact is one JSON object, indented with two spaces and followed by a newline. The same inputs produce the same bytes, and no value comes from the system clock. It is at most 16 MiB, more than twice the largest architecture, and a larger file is refused without being read past the limit. It is decoded strictly: valid UTF-8, no duplicate or unknown members, no trailing data.

| Member | Content |
|---|---|
| `artifact_version` | `pix-risk-model-v2`. |
| `software` | `module`, `version`, and `revision` of the toolkit build that trained the model, from the binary's Go build information; `revision` ends with `+dirty` when the build's working tree had changes. A value the build does not carry is `unknown`. The toolkit never runs git to find them, so one binary always writes the same identity. Each is printable ASCII of 1 to 256 bytes. |
| `contracts` | `scoring_input` `pix-payer-input-v3` and `training_example` `pix-training-example-v3`. |
| `features` | `feature_schema`, the feature schema the toolkit implements, `pix-risk-v3`; `descriptor`, its 65 features in order with `name`, `kind`, and `indicator` (`null` when always available), equal to [`pix-risk-v3` §9](../features/pix-risk-v3.md#9-features); `preparation`, the fitted state of [`pix-risk-v3` §11](../features/pix-risk-v3.md#11-fitted-standardization) with `feature_schema` and one `{name, mean, scale, constant_in_train, min, max, exercised_in_train}` per feature, `min` and `max` being `null` for features that are not standardized; and `preparation_sha256`. |
| `network` | `sizes`, `[65, hidden layers…, 1]`, the schema's feature count first, within the limits of [`pix-training-config-v2` §3.1](pix-training-config-v2.md#31-architecture); `activation` `sigmoid`; `loss`, the configured loss, `mean-squared-error` or `binary-cross-entropy`, as the configuration version defines it (ADR 0022); `algorithm` `full-batch-gradient-descent`; `layers`, one `{weights, biases}` per layer, with one row of weights per neuron; and `parameters_sha256`. |
| `selection` | `selected_epoch`, the epochs that produced the parameters, and `threshold`, the `policy` and `value` the model was selected with. The threshold is metadata for the integrator's decision; a score is not a decision (ADR 0002). |
| `training` | `config_version`, the version of the configuration that trained the model, one of the versions the toolkit supports, `pix-training-config-v2` or [`pix-training-config-v3`](pix-training-config-v3.md); `config_sha256`, `dataset_sha256`, `dataset_bytes`, `records` (with no invalid record and at least one usable record), `train_end`, `validation_end` (UTC), `report_version` `pix-training-report-v2`, and `report_sha256`, the SHA-256 of the report's exact bytes. |

**Self-hashes.** `preparation_sha256` is the SHA-256 of the compact JSON encoding of `preparation`, and `parameters_sha256` of the compact JSON encoding of `layers`, with numbers in their shortest round-trip form. Decoding recomputes both and rejects an artifact whose content does not match them.

**Validation on load.** Besides the self-hashes, decoding rejects an unsupported version, including `pix-risk-model-v1`, another feature schema, including `pix-risk-v2`, another descriptor, an invalid preparation state, including a train range that is missing, reversed, or non-finite, or given to a feature that is not standardized, parameters that do not match `sizes` or are not finite, an unsupported configuration version, activation, loss, algorithm, or threshold policy, a threshold outside `[0, 1]`, a selected epoch outside `1` to `1,000,000`, a malformed SHA-256, and inconsistent record counts or boundaries. A build that does not implement the configuration version an artifact names cannot reproduce how it was trained and rejects it.

**Precision.** Every number is written in its shortest round-trip form, so a decoded artifact has the exact binary64 parameters and preparation state that were encoded, and it prepares the same vectors and produces the same scores, bit for bit.

## 3. Training run

A run is a directory holding exactly four files:

| File | Content |
|---|---|
| `model.json` | The model artifact, `pix-risk-model-v2`. |
| `report.json` | The training report ([`pix-training-report-v2`](pix-training-report-v2.md)), whose exact bytes the artifact binds. |
| `config.json` | The canonical form of the configuration, in its own version ([`pix-training-config-v2` §6](pix-training-config-v2.md#6-canonical-form-and-identity), [`pix-training-config-v3` §6](pix-training-config-v3.md#6-canonical-form-and-identity)), whose SHA-256 is `config_sha256`. |
| `manifest.json` | The manifest. |

The manifest is one JSON object, decoded strictly:

| Member | Content |
|---|---|
| `manifest_version` | `pix-risk-run-v1`. |
| `files` | Exactly `model.json`, `report.json`, and `config.json`, in that order, each with `path`, `sha256`, and `bytes`. |

Verification reads the manifest only up to 64 KiB, refuses a model or configuration whose manifest entry exceeds its size limit before reading it, and reads no file past its manifest entry. A run is valid when every file matches its manifest entry, the directory holds no other file, the artifact decodes, the configuration decodes and is canonical, and the files bind each other: the artifact's `report_sha256` is the report's SHA-256; the artifact, the report, and the configuration name the same `config_sha256`; and the artifact and the report name the same dataset, selected epoch, and threshold.

## 4. Publishing

`cmd/train -run-dir DIR` publishes a run:

1. It verifies the bindings of the files in memory.
2. It creates a staging directory next to `DIR`, writes and syncs the model, the report, and the configuration, and writes the manifest last.
3. It verifies the staged run as in [section 3](#3-training-run), reading the files back.
4. It renames the staging directory to `DIR` and syncs the parent directory.

A run therefore exists completely or not at all: a failure at any step removes the staging directory and publishes nothing, and a crash can leave only a staging directory whose name starts with `.` and ends in `.staging-` and a random suffix. `DIR` must not exist; a run is never overwritten.

## 5. Promotion

Training never makes a model available for scoring. `cmd/promote -run DIR -output FILE` verifies the run completely, then writes its `model.json` to a temporary file next to `FILE` and renames it into place, so `FILE` never holds a partial artifact. An existing `FILE` is replaced only with `-replace`. Promotion reports the SHA-256 of the promoted artifact.

## 6. Out of scope

Remote model registries and signatures.
