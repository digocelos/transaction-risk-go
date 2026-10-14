# Model selection uses validation only, and the test evaluation is explicit

Every choice that shapes a trained model is made before the test partition is scored, and only with the train and validation partitions: the preparation state and the network are fitted on train, while the checkpoint, early stopping, and the decision threshold are chosen on validation. The configuration states whether a run scores the test partition at all. A model-selection run (`test_evaluation: skip`) never scores it, and a final run (`after_selection`) scores it exactly once, after everything is frozen, and records that it did.

A validation-selected threshold follows an explicit risk policy from the integrator: the highest validation recall whose false positive rate stays within a configured limit, breaking ties toward the highest threshold. The toolkit never invents a limit. When no threshold meets it, or when the validation partition cannot support the configured selection, the run fails without writing a model or a report instead of falling back to another policy.

## Considered options

- **Fall back to the final epoch or a fixed threshold** when validation cannot support selection: a run would look successful while using a policy nobody configured.
- **Always score the test partition**: repeated tuning against a reported test result turns the test partition into a second validation partition.
- **Choose the threshold that maximizes F1 or accuracy**: a metric is not a risk policy; it hides the trade-off between missed fraud and false alerts that each integrator sets.

## Consequences

- The training report distinguishes requested, actual, and selected epochs, the stop reason, the objective, and the threshold policy, and it records whether the test partition was scored.
- `cmd/train` exits with code 3 when the threshold policy is infeasible, and with code 1 when the validation partition cannot support the selection.
- Evaluating a saved model on other data is a separate operation (#30).
