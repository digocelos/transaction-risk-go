# The preparation state bounds what the network receives, and `pix-risk-v2` replaces `pix-risk-v1`

A trained network has no defined behavior on inputs outside the range its train partition covered: the inspection of the model trained on the first scenario dataset showed the score falling as fraud evidence grew past the largest value seen in training, and random responses to features no train record exercised. The feature schema `pix-risk-v2` keeps the 62 features and the preparation of `pix-risk-v1` and adds to the fitted state the train range of every standardized feature and whether each feature was exercised. Applying the state clips every available standardized value to its train range before standardizing it, so a network only ever sees values it was trained on, and scoring reports as model notes, separately from the preparation notes, the features it clipped and the unexercised features an input activates. The state is a property of one model, so the model notes are too: the specification examples, whose expected results describe inputs alone, are unchanged.

`pix-risk-v2` replaces `pix-risk-v1` without coexistence, as ADR 0013 did for the contracts, and the artifact that carries its state is `pix-risk-model-v2`, replacing `pix-risk-model-v1`: a build implements one feature schema and loads one artifact version. A version 1 artifact is rejected on load, and its model is retrained from the run's configuration and dataset, which the run records.

## Considered options

- **Clip in the scoring input contract:** the contract would then depend on a model's training data, against ADR 0001 and ADR 0006.
- **Keep extrapolating and only report it:** the inspection report already measures the response outside the train range; without clipping, a scoring input beyond it still gets a score the model never learned.
- **Coexistence of the two schemas and artifact versions:** every package that prepares, trains, inspects, evaluates, or scores would carry two code paths for a change that only retraining needs.
- **Monotonicity constraints in the network:** a different model family; out of scope for this schema.

## Consequences

- Train records are never clipped: they define the range. Validation and test records, and every scoring input, are.
- A scoring input with a `clipped` or `unexercised` model note is scored, with a score that rests on the edge, or outside, of what the model learned; the integrator's rules can route it (ADR 0002).
- The inspection report reads the train range and the exercised flags from the state, and its partial dependence grid shows the clipped response beyond the range.
- Training configurations name `pix-risk-v2`; the committed examples and the benchmark configuration do, and their canonical identities changed with the name.
