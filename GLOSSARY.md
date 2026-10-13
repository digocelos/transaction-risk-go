# Transaction Risk

Vocabulary for scoring financial transactions for fraud risk, starting with payer-side analysis of Pix payments before settlement.

## Pix

**Pix**:
Brazil's instant payment scheme, operated by the Central Bank of Brazil.
_Avoid_: PIX, instant transfer

**Payer**:
The person or company whose account sends a Pix payment.
_Avoid_: Sender, originator, customer

**Recipient**:
The person or company whose account receives a Pix payment.
_Avoid_: Receiver, payee, beneficiary

**Pix key**:
An alias (CPF, CNPJ, phone number, email address, or random key) that DICT resolves to a recipient account.
_Avoid_: Chave, alias

**DICT**:
The Central Bank's directory that resolves Pix keys to accounts and publishes security information about keys and their owners.

**Settlement**:
The point at which a Pix payment becomes final and funds reach the recipient.

**Payer-side analysis**:
Fraud scoring performed for the payer's institution after the payer initiates a Pix payment and before settlement.
_Avoid_: Outbound screening

## Integration boundary

**Integrator**:
The system that calls the toolkit and supplies all information a score needs; the toolkit never fetches data itself.
_Avoid_: Client, caller, consumer

**Input contract**:
The versioned definition of the information an integrator supplies to score one Pix payment.

**Pix payment**:
The Pix payment being scored.
_Avoid_: Transaction, transfer

**Observation**:
A fact about one subject that the integrator supplies for scoring; it is either known or missing.
_Avoid_: Signal, attribute

**Subject**:
What an observation describes: the payer account, the payer device, the recipient person, the recipient account, or the recipient Pix key.

**Payer account**:
The account debited by the Pix payment; all payer history is computed for this account alone, never for other accounts of the same person.
_Avoid_: Customer, payer (when the account is meant)

**Recipient key usage**:
Whether a Pix key was actually used to identify or resolve the recipient, regardless of how the Pix payment was initiated.
_Avoid_: Initiation method

**Payer device usage**:
Whether the payer authorized this specific Pix payment on a device.
_Avoid_: Channel

**Not applicable**:
The known fact that a subject or a feature does not apply to a Pix payment, such as a recipient Pix key when no key was used; it is stated explicitly and is never missing.

**Initiated Pix payment**:
A Pix payment instruction that the payer account started, whatever its outcome; technical retries, retransmissions, and reprocessing of the same instruction are not separate initiated payments.

**Empty history**:
The known fact that no qualifying payments exist in a window; it is never missing.

**Scoring input**:
One instance of the input contract: a Pix payment, its observations, and the decision time.
_Avoid_: Transaction, request, payload

**Missing**:
The state of an observation, or of a provider value the source allows to be omitted, that the integrator did not supply; a known zero or a known false is not missing, and missing never means not applicable.
_Avoid_: Null, empty, default, unknown

**Insufficient input**:
The outcome when a valid scoring input lacks information that a feature schema needs; no score is produced.
_Avoid_: Invalid input

**Stale**:
The state of a known observation whose age at decision time exceeds the maximum age a feature schema accepts; it makes the input insufficient, not invalid.

## Time

**Decision time**:
The instant at which a score is requested; every observation used for that score must have been available at that instant.
_Avoid_: Request time, now

**As-of time**:
The instant up to which the integrator asserts that an observation is a known, usable snapshot.
_Avoid_: Timestamp, collected at, watermark

**Provider watermark**:
A marker whose meaning a data provider defines; it is never presumed to be the limit up to which the provider's data is complete.

**DICT group watermark**:
The instant of the last event DICT considered in one statistics group; it is neither the query instant nor a measure of freshness.

**Contract-defined lookback window**:
A rolling period of fixed duration, defined by the input contract, that ends at an observation's as-of time and does not depend on any time zone.
_Avoid_: Calendar day

**Provider-defined window**:
A period whose boundaries an external provider defines and whose result arrives already aggregated, such as DICT's `d90`, `m12`, and `m60`; it is used exactly as the provider defines it.

## Devices

**Payer device**:
The device on which the payer authorized this specific Pix payment, as identified by the integrator.

**Registered device**:
A device registered under the Pix regulatory rules; it has no 90-day limit and is used only by rules outside the model.
_Avoid_: Known device, trusted device

**Device activity observation**:
The settled Pix payments that one payer account initiated on one device within a contract-defined lookback window.

**NewDevice**:
Legacy name of the `transaction-features-v1` feature that marks the absence of settled Pix payments from a device in the last 90 days; it does not mean an unregistered device.
_Avoid_: Unregistered device

## Scoring

**Score**:
The model's estimate, between 0 and 1, of how likely a payment is to be fraudulent; it informs a decision but is not one.
_Avoid_: Decision, verdict

**Feature schema**:
The versioned definition of the ordered numeric features a model consumes, including their meaning and scaling.

**Regulatory rule**:
A mandatory constraint from Pix regulation, such as a transaction limit, evaluated separately from scoring.

**Operational decision**:
The action an integrator takes on a payment, such as allowing or blocking it, evaluated separately from scoring.

## Training

**Fraud label**:
The known outcome, fraudulent or legitimate, of a past payment; it is never part of the information supplied for scoring.
_Avoid_: Fraud flag

**Training example**:
The scoring input that existed for a past Pix payment at its decision time, paired with the outcome observed later for that payment.

**Training dataset**:
A file of training examples, one per line, identified by the exact bytes it contains.
_Avoid_: Training data, corpus

**Outcome**:
The training-only information about what happened to a past Pix payment: its fraud label, the instant up to which that label is known, and the loss.
_Avoid_: Result, target

**Label as-of time**:
The instant up to which the dataset owner asserts that an outcome is known; it is never earlier than the payment's decision time.

**Label maturity**:
The state of a label whose label as-of time is at least 90 days after the payment's decision time, so most fraud reports for the payment already exist.

**Usable example**:
A training example that is valid, convertible by the feature schema being trained, and mature; only usable examples are used to train and evaluate.

**Loss amount**:
The part of a fraudulent Pix payment that the payer did not recover, as known at the label as-of time; zero for a legitimate payment and unknown when not established.

**Group key**:
An opaque, dataset-local key that groups related training examples for partitioning and leakage analysis; it is pseudonymous data and never a model input.
_Avoid_: Customer ID, user ID

**Partition**:
The train, validation, or test subset of a training dataset, chosen only by decision time and two fixed boundaries.
_Avoid_: Fold, random split

**Decision threshold**:
The score at or above which a Pix payment is predicted fraudulent, fixed before the test partition is evaluated.

**Dataset fingerprint**:
The SHA-256 digest of the exact bytes of a training dataset.

## Versions

**Contract version**:
Identifies the shape and meaning of an input contract.

**Model format version**:
Identifies the layout of a saved model file, independent of the feature schema the model was trained on.
