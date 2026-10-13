# Provider-extensible enumerations are open strings

A field that mirrors a list the provider declares extensible, such as DICT's `KeyType` ("novos tipos podem surgir"), is an open string: the specification documents the known values, accepts any value that matches the field's syntax, and preserves it exactly as received, without changing case, trimming, or turning an unrecognized value into missing. A closed enumeration would make every scoring input with a new provider value invalid, and the integrator could only lie by sending a different value or by reporting a known value as missing. Enumerations that the contract itself defines, such as recipient key usage, and stable dichotomies, such as natural versus legal person, stay closed.

This is independent of ADR 0006: unknown JSON fields are still rejected, because a misspelled field name is a structural error, while an unrecognized enumeration value can be a legitimate new fact.

## Consequences

A future feature that consumes an open string must define how it treats values it does not recognize.
