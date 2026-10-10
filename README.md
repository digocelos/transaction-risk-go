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

## Using the model (`cmd/predict`)

```bash
go run ./cmd/predict
```

```
Score: 0.187345
```

The program loads `model.json`, checks that it was trained with the same
feature schema (`transaction-features-v1`), and prints the fraud score of
one transaction. The score ranges from 0 (legitimate) to 1 (fraud). The
repository already includes a trained `model.json`, so you can run
`predict` without training first.

To simulate a different transaction, edit the `tx` value in
`cmd/predict/main.go`:

```go
tx := transaction.Transaction{
	AmountCents:        180000, // R$ 1,800.00
	TypicalAmountCents: 20000,  // R$ 200.00
	RecentCount:        9,
	NewRecipient:       true,
	NewDevice:          true,
}
```

This is evaluation example 3 from the demo, so `predict` prints a score
close to `0.9962`.

| Field | Meaning | Feature sent to the network |
|-------|---------|-----------------------------|
| `AmountCents` | Transaction amount, in cents | `AmountCents / TypicalAmountCents / 10` |
| `TypicalAmountCents` | Customer's usual amount, in cents (must be positive) | used in the ratio above |
| `RecentCount` | Number of recent transactions | `RecentCount / 20` |
| `NewRecipient` | First payment to this recipient | `1` or `0` |
| `NewDevice` | Transaction from a new device | `1` or `0` |

If `model.json` is missing, run the demo first. If you change the feature
order or scaling in `transaction/features.go`, update `FeatureSchema` and
retrain. `predict` rejects a model trained with a different schema.
