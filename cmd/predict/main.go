package main

import (
	"fmt"
	"log"

	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/transaction"
)

func main() {
	model, err := nn.Load(
		"model.json",
		transaction.FeatureSchema,
	)
	if err != nil {
		log.Fatal(err)
	}

	// Same transaction as the second evaluation example.
	tx := transaction.Transaction{
		AmountCents:        250000,
		TypicalAmountCents: 25000,
		RecentCount:        2,
		NewRecipient:       true,
		NewDevice:          false,
	}

	inputs, err := transaction.Features(tx)
	if err != nil {
		log.Fatal(err)
	}

	outputs, err := model.Predict(inputs[:])
	if err != nil {
		log.Fatal(err)
	}

	if len(outputs) != 1 {
		log.Fatal("transaction risk model must have exactly one output")
	}

	fmt.Printf("Score: %.6f\n", outputs[0])
}
