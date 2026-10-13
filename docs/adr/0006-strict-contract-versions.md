# Strict contract versions: unknown fields are rejected and every change is a new version

A scoring input names its contract version, and the toolkit accepts only versions on an explicit supported list; an unknown version is rejected before any conversion to features. Unknown fields are rejected at every level of the document, because a misspelled field would otherwise be read silently as a missing observation. Therefore any change, including a new optional field, produces a new contract version. We chose this over SemVer-style additive compatibility, which depends on ignoring unknown fields. Receiver-side analysis will be a separate contract, not an extension of the payer contract.

The versioned JSON document is the canonical interchange format, and the Go types are its reference implementation. Instead of a JSON Schema, which would add a dependency, a normative specification documents the contract.
