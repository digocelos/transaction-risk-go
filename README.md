# transaction-risk-go
An open-source Go toolkit for learning and building financial transaction fraud detection models, starting with Pix.

## Requirements

- Go 1.27 or later
- No external dependencies (standard library only)

Run all commands from the repository root. Both programs read and write
`model.json` relative to the current directory.

## Training the model (`cmd/demo`)

```bash
go run ./cmd/demo
```

The demo:

1. Builds 8 fictional labeled transactions (4 legitimate, 4 fraudulent) and
   converts each one into a feature vector with `transaction.Features`.
2. Creates a 4-4-1 network (4 inputs, 4 hidden neurons, 1 output) with
   seed `42`, so every run starts from the same weights.
3. Prints the scores of the untrained network.
4. Trains for 5000 epochs with learning rate `1.0`, printing the loss
   every 500 epochs.
5. Prints the scores after training, then evaluates 4 examples that were
   never used in training. A score `>= 0.5` is predicted as fraud (`1`).
6. Saves the trained network to `model.json`, overwriting the existing file.

Expected output (abridged):

```
Epoch    0 | Loss: 0.279745
Epoch  500 | Loss: 0.005225
...
Epoch 4500 | Loss: 0.000217

Evaluation on unseen examples:
Example 1 | Score: 0.0009 | Prediction: 0 | Target: 0 | Correct: true
Example 2 | Score: 0.1873 | Prediction: 0 | Target: 0 | Correct: true
Example 3 | Score: 0.9962 | Prediction: 1 | Target: 1 | Correct: true
Example 4 | Score: 0.9907 | Prediction: 1 | Target: 1 | Correct: true

Model saved to model.json
```

To experiment, edit `transactions`, `targets`, the hidden layer size, the
number of epochs or the learning rate in `cmd/demo/main.go`, then run the
demo again.

## Scoring a Pix payment (`cmd/predict`)

`predict` reads one scoring input: a JSON document that follows the
[`pix-payer-input-v1` input contract](docs/contracts/pix-payer-input-v1.md).
It describes a Pix payment that the payer's institution is about to send,
together with observations about the payer account, the payer device, the
recipient, and the recipient's Pix key. Your system collects those
observations; the toolkit never queries DICT or any other source.

```bash
go run ./cmd/predict docs/contracts/examples/01-complete.json
```

```
{"status":"scored","contract_version":"pix-payer-input-v1","feature_schema":"transaction-features-v1","score":0.18734535214416656}
```

Pass `-`, or no argument, to read the scoring input from standard input:

```bash
go run ./cmd/predict < docs/contracts/examples/08-no-pix-key.json
```

The score ranges from 0 (legitimate) to 1 (fraud). It informs your
decision but is not one: regulatory rules and operational decisions stay in
your own systems.

`predict` validates the input, converts it to the features of
`transaction-features-v1`, and scores them with `model.json`, which must
have been trained with the same feature schema. The repository already
includes a trained `model.json`, so you can run `predict` without training
first. Standard output always holds exactly one JSON object, followed by a
newline:

| Exit code | `status` | Meaning |
|-----------|----------|---------|
| 0 | `scored` | The payment was scored. |
| 2 | `invalid_input` | The document breaks the contract. `issues` lists each problem with a JSON Pointer `path`, a stable `code`, and a `message`. |
| 3 | `insufficient_input` | The document is valid, but the model cannot score it. `blockers` lists each `path` with a `reason`: `missing`, `stale`, `no_history`, or `not_applicable`. |
| 1 | none | Operational failure, such as an unreadable file or a missing model. Standard output is empty, and the message goes to standard error. |

The model never guesses missing information. For example, a payer account
with no settled Pix payments in the last 90 days has no typical amount, so
the input is insufficient rather than scored with an invented value.

[`docs/contracts/examples/`](docs/contracts/examples/) holds 17 scoring
inputs, each with its expected result: start from `01-complete.json` and
change one observation at a time. The model uses four features:

| Feature | Observation | Value sent to the network |
|---------|-------------|---------------------------|
| Amount ratio | `pix_payment.amount_cents` and `payer_account.settled_outgoing_pix_90d.median_amount_cents` | amount / median amount / 10 |
| Recent count | `payer_account.initiated_outgoing_pix_24h.count` | count / 20 |
| `NewRecipient` | `recipient_person.settled_pix_from_payer_account_365d.count` | `1` if the count is 0, otherwise `0` |
| `NewDevice` | `payer_device.settled_outgoing_pix_90d.count` | `1` if the count is 0, otherwise `0` |

If `model.json` is missing, run the demo first. If you change the feature
order or scaling in `transaction/features.go`, update `FeatureSchema` and
retrain. `predict` rejects a model trained with a different schema.
