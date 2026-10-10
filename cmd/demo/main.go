package main

import (
	"fmt"
	"log"

	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/transaction"
)

func main() {
	transactions := []transaction.Transaction{
		// Legitimate: familiar recipient and device.
		{
			AmountCents:        10000,
			TypicalAmountCents: 20000,
			RecentCount:        1,
			NewRecipient:       false,
			NewDevice:          false,
		},
		// Fraudulent: unusual amount, activity, recipient, and device.
		{
			AmountCents:        200000,
			TypicalAmountCents: 20000,
			RecentCount:        8,
			NewRecipient:       true,
			NewDevice:          true,
		},
		// Fraudulent: another combination of unusual characteristics.
		{
			AmountCents:        150000,
			TypicalAmountCents: 10000,
			RecentCount:        12,
			NewRecipient:       true,
			NewDevice:          true,
		},
		// Legitimate: large amount on a familiar device.
		{
			AmountCents:        200000,
			TypicalAmountCents: 20000,
			RecentCount:        1,
			NewRecipient:       false,
			NewDevice:          false,
		},
		// Legitimate: first payment to a new recipient.
		{
			AmountCents:        30000,
			TypicalAmountCents: 20000,
			RecentCount:        2,
			NewRecipient:       true,
			NewDevice:          false,
		},
		// Legitimate: a new device with otherwise familiar behavior.
		{
			AmountCents:        15000,
			TypicalAmountCents: 20000,
			RecentCount:        1,
			NewRecipient:       false,
			NewDevice:          true,
		},
		// Fraudulent: small amount with intense activity on a new device.
		{
			AmountCents:        5000,
			TypicalAmountCents: 20000,
			RecentCount:        15,
			NewRecipient:       true,
			NewDevice:          true,
		},
		// Fraudulent: familiar recipient with unusual activity.
		{
			AmountCents:        100000,
			TypicalAmountCents: 20000,
			RecentCount:        10,
			NewRecipient:       false,
			NewDevice:          true,
		},
	}

	// Fictional labels, in the same order as the transactions.
	targets := []float64{0, 1, 1, 0, 0, 0, 1, 1}

	samples := make([]nn.Sample, len(transactions))

	for i, tx := range transactions {
		inputs, err := transaction.Features(tx)
		if err != nil {
			log.Fatal(err)
		}

		samples[i] = nn.Sample{
			Inputs:  inputs[:],
			Targets: []float64{targets[i]},
		}
	}

	model, err := nn.NewNetwork(
		42,
		transaction.FeatureCount,
		4,
		1,
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Before training:")

	for i, sample := range samples {
		outputs, err := model.Predict(sample.Inputs)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf(
			"Sample %d | Score: %.4f | Target: %.0f\n",
			i+1,
			outputs[0],
			sample.Targets[0],
		)
	}

	for epoch := 0; epoch < 5000; epoch++ {
		loss, err := model.TrainBatch(samples, 1.0)
		if err != nil {
			log.Fatal(err)
		}

		if epoch%500 == 0 {
			fmt.Printf("Epoch %4d | Loss: %.6f\n", epoch, loss)
		}
	}

	fmt.Println("\nAfter training:")

	for i, sample := range samples {
		outputs, err := model.Predict(sample.Inputs)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf(
			"Sample %d | Score: %.4f | Target: %.0f\n",
			i+1,
			outputs[0],
			sample.Targets[0],
		)
	}

	// These examples are never passed to TrainBatch.
	evaluationExamples := []struct {
		transaction transaction.Transaction
		target      float64
	}{
		{
			transaction: transaction.Transaction{
				AmountCents:        18000,
				TypicalAmountCents: 20000,
				RecentCount:        2,
				NewRecipient:       false,
				NewDevice:          false,
			},
			target: 0,
		},
		{
			transaction: transaction.Transaction{
				AmountCents:        250000,
				TypicalAmountCents: 25000,
				RecentCount:        2,
				NewRecipient:       true,
				NewDevice:          false,
			},
			target: 0,
		},
		{
			transaction: transaction.Transaction{
				AmountCents:        180000,
				TypicalAmountCents: 20000,
				RecentCount:        9,
				NewRecipient:       true,
				NewDevice:          true,
			},
			target: 1,
		},
		{
			transaction: transaction.Transaction{
				AmountCents:        8000,
				TypicalAmountCents: 20000,
				RecentCount:        13,
				NewRecipient:       true,
				NewDevice:          true,
			},
			target: 1,
		},
	}

	fmt.Println("\nEvaluation on unseen examples:")

	for i, example := range evaluationExamples {
		inputs, err := transaction.Features(example.transaction)
		if err != nil {
			log.Fatal(err)
		}

		outputs, err := model.Predict(inputs[:])
		if err != nil {
			log.Fatal(err)
		}

		prediction := 0.0
		if outputs[0] >= 0.5 {
			prediction = 1
		}

		fmt.Printf(
			"Example %d | Score: %.4f | Prediction: %.0f | Target: %.0f | Correct: %t\n",
			i+1,
			outputs[0],
			prediction,
			example.target,
			prediction == example.target,
		)
	}

	if err := model.Save(
		"model.json",
		transaction.FeatureSchema,
	); err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nModel saved to model.json")
}
