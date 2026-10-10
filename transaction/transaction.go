package transaction

// Transaction contains the information available at analysis time.
type Transaction struct {
	AmountCents        int64
	TypicalAmountCents int64
	RecentCount        int
	NewRecipient       bool
	NewDevice          bool
}
