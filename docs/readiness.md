# Software readiness

This checklist states what the toolkit guarantees as software, the evidence for each guarantee, and its limits, at the commit that contains it. It is the gate of epic #24. It reflects demonstrated software behavior on synthetic data: no metric in this repository is evidence that any model detects fraud.

## 1. Verdict

- **Ready as software.** The supported workflow, configure → train and select → package → evaluate → load and score, runs from a clean checkout on linux/amd64 and darwin/arm64, without real data or a bundled model, and every guarantee below has automated evidence that CI enforces.
- **Not established.** Fraud detection quality, calibrated probabilities, regulatory compliance, and production operation. They need the maintainer's real data and the validation of [section 5](#5-deferred-validation-with-real-data), which is deliberately outside this gate.

## 2. CI gates

CI runs every step on linux/amd64 (`ubuntu-24.04`) and darwin/arm64 (`macos-15`), and fails on any of them:

| Step | Fails when |
|---|---|
| Formatting | A file is not formatted with `gofmt`. |
| Vet | `go vet` reports a problem. |
| Unit tests | Any package test fails: contracts, preparation, training, selection, artifacts, runs, evaluation, scoring, limits, and the 32-bit build guard. |
| Integration tests | The workflow fails with the built commands (`internal/e2e`): the quick start, the Go API scenario, outcome isolation, every specification example through `predict`, failures that must write nothing, and an interrupted training run. |
| Race detector | Shared scoring has a data race: `scoring` and `predict`, the only code that shares state across goroutines. Training, evaluation, and the commands run on one goroutine. |
| Benchmarks | A benchmark stops compiling or running at its smallest size. No time is asserted. |
| Build | `go build ./...` fails. |

In particular, CI fails on:

- **Incompatible schemas and versions:** version 1 inputs and datasets (`TestDecodeAcceptsOnlyRequestedVersions`, `TestScanRejectsOtherVersions`, `TestFailuresWriteNothing`), another feature schema in a configuration or artifact (`TestRunRejectsInvalidConfigurationsBeforeReading`, `TestDecodeRejectsTamperedOrInvalidArtifacts`), and a descriptor other than `pix-risk-v1` (`TestDescriptorMatchesTheSpecification`).
- **Leakage into features or selection:** test outcomes that change the preparation state, the parameters, the checkpoint, or the threshold, and outcomes that change any prepared feature (`TestOutcomesNeverReachTheModel`, `TestRunSelectsWithoutTheTestPartition`, `TestPrepareDatasetFitsOnTheTrainPartitionOnly`, `TestRunTrainsOnTheTrainPartitionOnly`); training metadata in a scoring input (`TestScoreRejectsOtherDocuments`).
- **Corrupted artifacts:** changed weights, preparation, versions, descriptor, or sizes, truncated or oversized files (`TestDecodeRejectsTamperedOrInvalidArtifacts`, `TestArtifactsAboveTheSizeLimitAreRefused`, `TestNewRejectsInvalidArtifacts`, `TestFailuresWriteNothing`).
- **Inconsistent publication:** runs whose files do not match their manifest or each other, extra or missing files, failures at any publishing step, and promotion of a tampered run (`TestVerifyRejectsTamperedRuns`, `TestPublishRefusesInconsistentBundles`, `TestPublishLeavesNothingBehindOnFailure`, `TestPromoteLeavesNothingBehindOnFailure`, `TestFailuresWriteNothing`).

Deliberate mutations of the code, recorded in each issue's pull request, checked that these tests fail when the behavior they guard breaks.

## 3. Guarantees

| # | Guarantee | Evidence | Limitation |
|---|---|---|---|
| G1 | Scoring inputs follow the strict [`pix-payer-input-v2`](contracts/pix-payer-input-v2.md) contract: unknown members, other versions, and invalid values are rejected with stable codes and JSON Pointers. | `pixinput` tests; the 20 [examples](contracts/examples-v2/) with expected results; `TestPredictSpecificationExamples` (e2e). | Any change, even an optional member, needs a new contract version (ADR 0006). |
| G2 | Missing information is never imputed: required facts block scoring, and unavailable optional observations become explicit indicators and preparation notes. | `TestPrepareSpecificationExamples`, `TestPrepareBlocksOnEveryMissingRequiredFact`, `TestPreparePartialDictResponses`, `TestPrepareDistinguishesKnownFalseFromMissing`; ADR 0004, ADR 0011. | Missing and stale are the same to the model; notes keep them apart. |
| G3 | Features come only from the point-in-time scoring input: no outcome, group key, or personal identifier is a feature. | `TestDescriptorMatchesTheSpecification`, `TestOutcomesNeverReachTheModel`, `TestScoreRejectsOtherDocuments`; [`pix-risk-v1` §18](features/pix-risk-v1.md#18-leakage-and-privacy); ADR 0005, ADR 0007, ADR 0009. | The integrator guarantees that observations existed at decision time; the toolkit checks `as_of` but cannot detect a backfilled value. |
| G4 | The temporal protocol holds: preparation and parameters are fitted on train, the checkpoint and threshold are chosen on validation, and the test partition is scored only after everything is frozen. | `TestPrepareDatasetFitsOnTheTrainPartitionOnly`, `TestRunTrainsOnTheTrainPartitionOnly`, `TestRunSelectsWithoutTheTestPartition`, `TestRunRestoresTheBestValidationCheckpoint`, `TestOutcomesNeverReachTheModel`; ADR 0015. | Software cannot stop a person from looking at test results repeatedly (ADR 0017). Group overlap is disclosed, not removed. |
| G5 | Training is deterministic on one platform, and scores agree within 10^−9 across architectures. | `TestRunIsDeterministic`, `TestTrainIsReproducible`, `TestRunMatchesReferenceScoresAcrossArchitectures` on both CI platforms. | Bit-for-bit only on one platform ([operating limits §1](operations/operating-limits.md#1-supported-platforms)). |
| G6 | Numerical failures stop instead of producing non-finite values. | `TestTrainBatchFollowsTheNumericalGradient`, `TestTrainBatchRejectsNonFiniteTrainingWithoutChangingParameters`, `TestPrepareStaysFiniteAtTheLargestValues`, `TestApplyRejectsStandardizedValuesThatOverflow`, `TestEvaluateRejectsInvalidInput`. | One model family: a dense sigmoid network trained with full-batch gradient descent on mean squared error. |
| G7 | A model is published as a verified run, completely or not at all, and promoted only explicitly. | `TestRoundTripPreservesFeaturesAndScores`, `TestPublishLeavesNothingBehindOnFailure`, `TestPromoteIsExplicitAndAtomic`, `TestFailuresWriteNothing`; ADR 0016. | SHA-256 values detect changes; they are not signatures. A crash can leave a staging directory. |
| G8 | Training and scoring prepare features identically and give the same scores. | `TestPrepareDatasetMatchesScoringPreparation`, `TestScoreMatchesTheTrainingPath`, `TestFrozenModelThroughTheGoAPI`. | Across architectures, within the tolerance of G5. |
| G9 | A saved model evaluated independently reproduces its training report, reads only, and labels incomplete evaluations. | `TestEvaluateReproducesTheFinalEvaluation`, `TestEvaluateOnlyReads`, `TestEvaluateMarksIncompleteEvaluations`, `TestFrozenModelThroughTheGoAPI`; ADR 0017. | A complete evaluation is not a quality pass. |
| G10 | Dataset, configuration, report, and model identities are recoverable from every output: a score names its artifact, the artifact binds its configuration, dataset, and report, and an evaluation names all of them. | `TestFrozenModelThroughTheGoAPI`, `TestArtifactRecordsItsIdentities`, `TestEvaluateRecordsIdentities`, `TestTrainWritesTraceableArtifacts`, `TestScoreSpecificationExamples`. | The software build is identified from Go build information; a build outside a VCS checkout records `unknown`. |
| G11 | The scoring API loads an artifact once and serves concurrent callers without changing it, and calls no training code and no network. | `TestScorerLoadsOnce`, `TestScorerIsSafeForConcurrentUse` with the race detector, `TestScorerResultsAreCopies`, `TestScoringDependsOnNoTrainingOrNetworkPackage`; ADR 0018. | One call is not cancelable; it is bounded work. |
| G12 | Resource limits refuse work beyond the supported envelope before uncontrolled allocation or publication, and cancellation stops within a documented bound. | `TestScanStopsAtTheRecordLimit`, `TestRunRefusesTrainingWorkAboveTheLimit`, `TestArtifactsAboveTheSizeLimitAreRefused`, `TestVerifyBoundsWhatItReads`, `TestRunChecksCancellationWithinEpochs`, `TestInterruptedTrainingPublishesNothing`; ADR 0019. | Full batch: about 1,000,000 records ([operating limits](operations/operating-limits.md)); larger data needs #43. |
| G13 | Only 64-bit platforms build, and both supported architectures are tested. | `TestThirtyTwoBitPlatformsDoNotBuild`; the CI matrix. | windows/amd64 builds but is not tested. |
| G14 | A clean checkout runs the whole workflow without real data or a bundled model. | `TestDocumentedWorkflow`, the README quick start, `Example` in `scoring`. | The data is synthetic. |
| G15 | Failures write nothing: invalid or version 1 datasets, other schemas, damaged artifacts, tampered runs, existing or unwritable outputs, and interrupts. | `TestFailuresWriteNothing`, `TestInterruptedTrainingPublishesNothing`, and the command tests. | — |

## 4. Operating envelope

[Operating limits](operations/operating-limits.md) documents the supported platforms, the floating-point tolerances, the enforced limits, the measured memory and time, the cancellation bounds, and where full-batch training ends, with [measurements](operations/measurements/2026-10-14-darwin-arm64.md) and the commands that reproduce them.

## 5. Deferred: validation with real data

No real dataset was available, so nothing in this checklist validates a model. When the maintainer supplies real data, these steps remain, and they are a model's validation, not software defects:

1. **Mapping.** Build `pix-payer-input-v2` inputs and `pix-training-example-v2` records from the source systems, with every observation as it was at decision time and every `as_of` true. Backfilled observations leak the future, and the toolkit cannot detect them.
2. **Data audit.** Read the dataset report and coverage: exclusions by reason, label maturity, unknown losses, populations, availability states never seen in train (`not_exercised_in_train`), and group overlap. Decide what the gaps mean before training.
3. **Boundaries and size.** Choose `train_end` and `validation_end` so every partition has both classes and enough mature labels, within the [envelope](operations/operating-limits.md#5-where-full-batch-training-ends) or with mini-batch training (#43).
4. **Configuration search.** Compare architectures and settings on validation only, and record every run.
5. **Final evaluation.** Evaluate the chosen model once on held-out data, including later periods with window scopes, and report precision, recall, PR-AUC, false positives, and missed losses per population.
6. **Baseline.** Compare against the integrator's current rules on the same held-out data.
7. **Risk policy.** Agree the threshold policy and its false positive limit with the risk owners; the threshold is metadata, and the decision stays outside the toolkit (ADR 0002).
8. **Calibration.** If a probability is needed, calibrate and validate it; scores are not probabilities.
9. **Governance.** Review privacy, pseudonymization of group keys, regulatory obligations, and model risk with the responsible teams.
10. **Operation.** Deployment, monitoring, drift detection, and retraining policy are outside this toolkit.

## 6. Remaining software limitations

- Training is full batch, bounded to the [supported envelope](operations/operating-limits.md); mini-batch training is #43.
- One model family: a dense sigmoid network, full-batch gradient descent, and mean squared error, with the threshold policies of [`pix-training-config-v2`](contracts/pix-training-config-v2.md).
- Artifacts and runs are protected against change by SHA-256, not authenticated by signatures.
- `predict` scores one input per process; batch scoring uses the Go API.
- windows is not tested.

## 7. Traceability of epic #24

Each acceptance criterion of the epic's issues, and its evidence:

| Criterion | Evidence |
|---|---|
| **#25** Every feature has a formula and a field mapping; excluded candidates are listed. | [`pix-risk-v1` §9](features/pix-risk-v1.md#9-features), [§17](features/pix-risk-v1.md#17-excluded-and-deferred-candidates). |
| **#25** Complete, partial, new-account, no-key, no-device, stale, and invalid examples have expected outcomes. | [§16](features/pix-risk-v1.md#16-examples) and [`examples-v2/`](contracts/examples-v2/), checked by `TestPrepareSpecificationExamples` and `TestDecodeSpecificationExamples`. |
| **#25** No label, group key, post-decision observation, or identifier becomes a feature. | [§18](features/pix-risk-v1.md#18-leakage-and-privacy); G3. |
| **#25** Unresolved choices were resolved before implementation. | ADR 0011, ADR 0012, ADR 0013, and the decisions recorded in [`pix-risk-v1`](features/pix-risk-v1.md), all before #26. |
| **#25** Glossary, ADRs, and the breaking migration are updated. | [`GLOSSARY.md`](../GLOSSARY.md); [§19](features/pix-risk-v1.md#19-breaking-migration), completed by #33. |
| **#26** Golden tests cover formulas, units, boundaries, availability encodings, and partial DICT responses. | `pixrisk` tests against values computed by an independent implementation. |
| **#26** Training and inference produce the same vector. | G8. |
| **#26** Validation and test values cannot change the fitted state. | `TestPrepareDatasetFitsOnTheTrainPartitionOnly`. |
| **#26** Names, count, and order match the descriptor; invalid or non-finite values cannot enter the network. | `TestDescriptorMatchesTheSpecification`, `TestApplyRejectsValuesThatMustNotReachTheNetwork`, `TestPredictRejectsNonFiniteInputs`. |
| **#26** Normative examples and documentation status are current. | [`examples-v2/`](contracts/examples-v2/); the status lines of the specifications. |
| **#27** Unsupported versions, options, dimensions, and ranges fail without output. | `trainingconfig` tests, `TestTrainRejectsInvalidConfigurations`, `TestFailuresWriteNothing`. |
| **#27** Numerical gradients match finite differences. | `TestTrainBatchFollowsTheNumericalGradient`. |
| **#27** Invalid samples and divergence change no parameter and promote nothing. | `TestTrainBatchRejectsInvalidSamplesWithoutChangingParameters`, `TestTrainBatchRejectsNonFiniteTrainingWithoutChangingParameters`. |
| **#27** Results are reproducible on one platform; cross-architecture tolerance is documented. | G5. |
| **#27** Configuration examples, usage, cancellation, and output are tested. | [`training-config-examples/`](contracts/training-config-examples/), `TestTrainStopsWhenCanceled`, `TestTrainHelp`. |
| **#28** An earlier best checkpoint is restored. | `TestRunRestoresTheBestValidationCheckpoint`. |
| **#28** Test labels and features cannot affect transforms, checkpoint, stopping, or threshold. | `TestRunSelectsWithoutTheTestPartition`, `TestOutcomesNeverReachTheModel`. |
| **#28** Threshold ties and infeasible policies are deterministic. | `TestSelectThresholdBreaksTiesTowardTheHighestThreshold`, `TestRunReportsAnInfeasibleThresholdPolicy`. |
| **#28** Empty or single-class validation follows an explicit policy. | `TestRunRequiresWhatValidationSelectionNeeds`, `TestTrainRejectsAValidationPartitionTheSelectionCannotUse`. |
| **#28** Reports distinguish requested, actual, and selected epochs, policy, and metrics. | [`pix-training-report-v2` §5](contracts/pix-training-report-v2.md#5-selection); `TestMarshalRecordsEveryPart`. |
| **#29** Round trips preserve feature vectors and scores. | `TestRoundTripPreservesFeaturesAndScores`. |
| **#29** Tampered, mismatched, truncated, incomplete, and unsupported artifacts are rejected. | `TestDecodeRejectsTamperedOrInvalidArtifacts`. |
| **#29** Fault injection leaves no mixed bundle. | `TestPublishLeavesNothingBehindOnFailure`. |
| **#29** Manifest and artifact bind configuration, dataset, and report. | `TestPublishWritesAVerifiableRun`, `TestArtifactRecordsItsIdentities`. |
| **#29** No bundled model or real dataset is needed. | G14. |
| **#30** An independently loaded model reproduces the final evaluation. | `TestEvaluateReproducesTheFinalEvaluation`, `TestFrozenModelThroughTheGoAPI`. |
| **#30** Evaluation mutates nothing. | `TestEvaluateOnlyReads`. |
| **#30** Invalid datasets fail with diagnostics and no report. | `TestEvaluateStopsOnInvalidDatasets`, `TestEvaluateStopsOnInvalidDataset`. |
| **#30** Empty, single-class, unknown-loss, and override cases are unambiguous. | `TestEvaluateMarksIncompleteEvaluations`, `TestEvaluateLabelsAThresholdOverride`. |
| **#30** Output, exit codes, and documentation are tested. | `cmd/evaluate` tests; [`pix-evaluation-report-v1`](contracts/pix-evaluation-report-v1.md). |
| **#31** Repeated and concurrent calls are stable; race checks cover shared scoring. | `TestScorerIsSafeForConcurrentUse`; the race detector step. |
| **#31** File and stdin inputs, versions, malformed artifacts, and missing or required data are tested. | `scoring` and `cmd/predict` tests. |
| **#31** Loading happens once; no training or external provider is called. | `TestScorerLoadsOnce`, `TestScoringDependsOnNoTrainingOrNetworkPackage`. |
| **#31** Scoring outputs identify the loaded artifact. | `TestScoreSpecificationExamples`, `TestPredictIgnoresModelFilesInTheWorkingDirectory`. |
| **#31** Go and CLI examples run from a clean checkout. | `Example` in `scoring`, `TestDocumentedWorkflow`. |
| **#32** Benchmarks are reproducible and documented. | [Operating limits §6](operations/operating-limits.md#6-reproducing-the-measurements), [measurements](operations/measurements/2026-10-14-darwin-arm64.md). |
| **#32** Limits fail before allocation or publication; cancellation is bounded. | G12. |
| **#32** Skewed labels and missing data exercise the pipeline. | `TestGenerateSkewsLabelsAndFeatures`, `TestGenerateCoversAbsencePatterns`, `TestFrozenModelThroughTheGoAPI`. |
| **#32** Platforms and tolerances are explicit. | [Operating limits §1](operations/operating-limits.md#1-supported-platforms); G13. |
| **#32** Where full batch ends and the follow-up are documented. | [Operating limits §5](operations/operating-limits.md#5-where-full-batch-training-ends); #43. |
| **#33** Nothing depends on `cmd/demo` or a root `model.json`. | Both are removed; G14. |
| **#33** Smoke examples generate, evaluate, and score a temporary artifact. | `TestDocumentedWorkflow`. |
| **#33** Software readiness is documented apart from model readiness. | [README](../README.md#software-readiness-is-not-model-readiness); this document. |
| **#33** Version references are precise. | [README, Versions](../README.md#versions). |
| **#33** No real data, credentials, or production artifacts are committed. | Repository contents: synthetic fixtures and specification examples only. |
| **#34** A clean checkout completes train, evaluate, and score without real data or a bundled model. | G14. |
| **#34** CI fails on incompatible schemas, leakage, corrupted artifacts, and inconsistent publication. | [Section 2](#2-ci-gates). |
| **#34** Identities are recoverable from reports and scoring outputs. | G10. |
| **#34** This checklist names each guarantee, evidence, and limitation, and real-data validation is explicit. | [Sections 3](#3-guarantees) and [5](#5-deferred-validation-with-real-data). |
| **#34** The epic closes only when every criterion has evidence. | This table; no required item is deferred. |
