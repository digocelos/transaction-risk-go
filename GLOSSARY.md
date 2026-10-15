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
The outcome when a required fact that a feature schema needs is missing or stale in a valid scoring input; no score is produced.
_Avoid_: Invalid input

**Required fact**:
Information a feature schema cannot do without, such as the payer account histories; when it is missing or stale, the input is insufficient.

**Optional observation**:
An observation a feature schema can do without; when it is missing, stale, or not applicable, the features it feeds are unavailable instead of blocking the score.

**Availability indicator**:
A feature that is 1 when another feature holds a fact and 0 when that feature is unavailable and holds the neutral value 0.
_Avoid_: Default flag, imputation flag

**Stale**:
The state of a known observation whose age at decision time exceeds the maximum age a feature schema accepts; a stale required fact makes the input insufficient, and a stale optional observation makes its features unavailable. It never makes the input invalid.

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

**Reference time offset**:
The UTC offset of the time reference the payer's PSP uses for the payer's limit periods at decision time, either the payer's registered address or Brasília time; the toolkit uses it only to compute the local time of day.
_Avoid_: Time zone, local offset

**Contract-defined lookback window**:
A rolling period of fixed duration, defined by the input contract, that ends at an observation's as-of time and does not depend on any time zone.
_Avoid_: Calendar day

**Provider-defined window**:
A period whose boundaries an external provider defines and whose result arrives already aggregated, such as DICT's `d90`, `m12`, and `m60`; it is used exactly as the provider defines it.

## Devices

**Payer device**:
The device on which the payer authorized this specific Pix payment, as identified by the integrator.

**Registered device**:
An access device registered in advance under the Pix regulatory rules; its registration status can be a model feature, while the limits that apply to unregistered devices are enforced outside the model. Registration is not usage.
_Avoid_: Known device, trusted device

**Device activity observation**:
The settled Pix payments that one payer account initiated on one device within a contract-defined lookback window.

## Limits and registrations

**Limit period**:
The kind of value limit the payer's PSP applies to a Pix payment: the daytime or nighttime period when the recipient is a natural person, a daily limit, or no limit per period; the integrator determines it.

**Period limit**:
The value limit in effect for the payer account in the limit period that applies to a Pix payment; a known zero is a fact, not a missing value.
_Avoid_: Transaction limit, cap

**Limit consumption**:
The amount already counted against the period limit in the current period, excluding the Pix payment being scored.

**Projected limit utilization**:
The limit consumption plus the amount of the Pix payment being scored, divided by the period limit; undefined when the limit is zero.

**Pre-registered recipient**:
A recipient covered by a registration the payer made for specific limits, in effect at decision time and applicable to the recipient account of this payment; it is context, not a guarantee of legitimacy.
_Avoid_: Trusted recipient, whitelisted recipient

## Scoring

**Score**:
The model's estimate, between 0 and 1, of how likely a payment is to be fraudulent; it informs a decision but is not one.
_Avoid_: Decision, verdict

**Feature schema**:
The versioned definition of the ordered numeric features a model consumes, including their meaning, scaling, availability rules, and the layout of the preparation state.

**Feature preparation**:
The conversion of a valid scoring input into the features of a feature schema, shared by training, evaluation, and scoring.
_Avoid_: Feature extraction, conversion

**Preparation state**:
The values fitted on the train partition, such as standardization means and scales, that feature preparation applies to every input of one model.

**Preparation note**:
A report that one optional source of a feature is not known, with its path and its status: missing, stale, not applicable, no history, zero limit, or unrecognized.

**Scorer**:
A model artifact loaded once to score any number of scoring inputs; it never changes after loading.

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

**Training data template**:
The CSV file, one column per value of a training example, in which training data can be prepared before it is converted to a training dataset; it is never read by training itself.

**Dataset fingerprint**:
The SHA-256 digest of the exact bytes of a training dataset.

## Evaluation

**Independent evaluation**:
Measuring a saved model artifact on a dataset without training, refitting, selecting, or tuning anything, and without changing the model.
_Avoid_: Validation, testing (when the protocol roles are not meant)

**Evaluation scope**:
The records an independent evaluation measures, chosen by decision time: the validation or test partition of the model's boundaries, or a window that starts at or after the model's train_end.

**Held-out data**:
Records that were not used to fit, select, or tune a model: the test partition, or records decided after the model's validation_end. Changing a model after looking at its results on held-out data turns them into validation data.

**Threshold override**:
A decision threshold given for one evaluation instead of the model's own; the evaluation report labels it, and the model keeps its threshold.

**Reference dataset**:
The training dataset of a model, supplied to measure group overlap when another dataset is evaluated; its dataset fingerprint must be the one the model records.

**Incomplete evaluation**:
An evaluation whose scope cannot support some metrics, because it has no usable examples, no fraud, no legitimate payments, or frauds with an unknown loss; it is reported, never treated as a quality pass.
_Avoid_: Failed evaluation

## Operations

**Software readiness**:
Evidence that the toolkit behaves as specified, from contracts to scoring, on synthetic data; it says nothing about whether a model detects fraud.

**Model readiness**:
The acceptance of one trained model for use, after validation on real, representative data against an agreed risk policy; it is outside the toolkit.
_Avoid_: Production-ready model (for software checks)

**Supported envelope**:
The dataset sizes, training work, and file sizes the toolkit accepts by default, set from measurements on reference hardware; work beyond it is refused, not attempted.

**Training work**:
The number of network parameters times the samples a run passes through the network: train records times the maximum epochs, plus validation records times the validation evaluations.

**Synthetic dataset**:
A generated dataset with realistic shapes, class imbalance, and absence patterns, used to measure and try the toolkit; it is not payment data, and a model trained on it detects nothing.
_Avoid_: Sample data, test data (when real data is meant)

## Versions

**Contract version**:
Identifies the shape and meaning of an input contract.

**Artifact version**:
Identifies the layout of a model artifact, such as `pix-risk-model-v1`; it says nothing about how good the model is or how its features are prepared, which the feature schema identifies.
_Avoid_: Model version, model format version

**Model artifact**:
A saved trained model with everything needed to score without training: its parameters, feature descriptor, preparation state, selection, and the identities of what produced it.
_Avoid_: Model file, weights

**Training run**:
A published directory holding a model artifact, its training report, its configuration, and a manifest that binds them; it exists completely or not at all.

**Promotion**:
The explicit step that verifies a training run and makes its model artifact available for scoring; training alone never promotes.
_Avoid_: Deployment, release
