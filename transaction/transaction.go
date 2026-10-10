package transaction

// Transaction contém informações disponíveis no momento da análise.
type Transaction struct {
	AmountCents        int64
	TypicalAmountCents int64
	RecentCount        int
	NewRecipient       bool
	NewDevice          bool
}
