# No personal identifiers in scoring inputs

A scoring input carries derived facts and enumerated values only. It contains no personal data or identifiers: no CPF or CNPJ, names, Pix key values, branch or account numbers, institution codes, device identifiers, IP addresses, or geolocation. It also contains no hashes of identifiers and no correlation ID. The integrator already computes every relationship the toolkit needs (ADR 0001), so the toolkit has no use for identity, and keeping identity out means scoring inputs and training datasets can be stored and shared without carrying personal data under the LGPD.

## Consequences

Identification, correlation, deduplication, identity-based auditing, and persistence of scoring inputs are the integrator's responsibility. The toolkit cannot link two scoring inputs to the same person, account, or device. Personal data that enters a dataset is hard to remove, so adding any identifier later requires a new contract version and a new ADR.
