# Scores are separate from regulatory rules and operational decisions

The model produces a score. It does not enforce regulatory rules, such as Pix transaction limits, and it does not choose an operational decision, such as allowing or blocking a payment. Regulation changes on its own schedule and must be applied deterministically and auditably, and each integrator sets its own risk appetite; a learned score can provide neither guarantee, so both stay outside model scoring.

Refined by [ADR 0012](0012-regulatory-context-can-be-a-feature.md): the context of a regulatory rule can be a model feature, while the rule itself is still enforced outside scoring.
