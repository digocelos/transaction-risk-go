# Regulatory context can be a model feature; enforcement stays outside

Facts about the regulatory context of a Pix payment can be model features: the limit period that applies, the applicable limit and its consumption, whether the payer device is a registered access device, whether the recipient is pre-registered, and the local time of day in the time reference the payer's PSP uses. The integrator determines each fact, including which rule or period applies and which time reference its PSP chose, and supplies it in the scoring input. The toolkit only turns those facts into features. It never decides whether a rule applies, computes a regulatory calendar, or blocks, limits, or approves a payment.

This refines ADR 0002: scores remain separate from regulatory rules and operational decisions, but a rule's context is information like any other. It also refines ADR 0005: local-calendar rules, such as the nighttime period, still belong to the integrator's rules layer; the toolkit receives their result as a fact and computes the local time of day from `decision_time` and an explicit `reference_time_offset`, without a time zone database.

## Consequences

- A registered device, a pre-registered recipient, or a high limit is context, not a guarantee of legitimacy. A fraudster can induce a payer to register a recipient or raise a limit.
- A rule changes on its own schedule; when its meaning changes, the scoring input contract or the feature schema gets a new version, and the model is retrained.
- `reference_time_offset` can reveal the coarse time zone of the payer's registered address when the PSP uses that reference. It identifies no person and is not geolocation in the sense of ADR 0007, which still excludes coordinates, addresses, and every identifier.
