package transaction

import "fmt"

const (
	// FeatureSchema identifies feature order, meaning, and scaling.
	FeatureSchema = "transaction-features-v1"
	FeatureCount  = 4

	amountRatioScale = 10.0
	recentCountScale = 20.0
)

// Features converts a transaction into an ordered feature vector.
func Features(tx Transaction) ([FeatureCount]float64, error) {
	var inputs [FeatureCount]float64

	if tx.AmountCents < 0 {
		return inputs, fmt.Errorf("transaction amount cannot be negative")
	}

	if tx.TypicalAmountCents <= 0 {
		return inputs, fmt.Errorf("typical amount must be positive")
	}

	if tx.RecentCount < 0 {
		return inputs, fmt.Errorf("recent transaction count cannot be negative")
	}

	amountRatio := float64(tx.AmountCents) /
		float64(tx.TypicalAmountCents)

	inputs[0] = amountRatio / amountRatioScale
	inputs[1] = float64(tx.RecentCount) / recentCountScale
	inputs[2] = boolToFloat(tx.NewRecipient)
	inputs[3] = boolToFloat(tx.NewDevice)

	return inputs, nil
}

func boolToFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
