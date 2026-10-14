package synthetic

import "encoding/json"

// The types below encode pix-training-example-v2 records. A pointer is
// null when it is nil; a conditional subject is omitted when nil.

type trainingExample struct {
	TrainingExampleVersion string       `json:"training_example_version"`
	ScoringInput           scoringInput `json:"scoring_input"`
	Outcome                outcome      `json:"outcome"`
	GroupKey               *string      `json:"group_key"`
}

type outcome struct {
	Label           string `json:"label"`
	LabelAsOf       string `json:"label_as_of"`
	LossAmountCents *int64 `json:"loss_amount_cents"`
}

type scoringInput struct {
	ContractVersion string       `json:"contract_version"`
	DecisionTime    string       `json:"decision_time"`
	PixPayment      pixPayment   `json:"pix_payment"`
	Observations    observations `json:"observations"`
}

type pixPayment struct {
	AmountCents         int64  `json:"amount_cents"`
	PayerDeviceUsage    string `json:"payer_device_usage"`
	RecipientKeyUsage   string `json:"recipient_key_usage"`
	RecipientPersonType string `json:"recipient_person_type"`
	ReferenceTimeOffset string `json:"reference_time_offset"`
	PayerLimitPeriod    string `json:"payer_limit_period"`
}

type observations struct {
	PayerAccount     payerAccount     `json:"payer_account"`
	PayerDevice      *payerDevice     `json:"payer_device,omitempty"`
	RecipientPerson  recipientPerson  `json:"recipient_person"`
	RecipientAccount recipientAccount `json:"recipient_account"`
	RecipientPixKey  *recipientPixKey `json:"recipient_pix_key,omitempty"`
}

type payerAccount struct {
	SettledOutgoingPix90d   *settledHistory          `json:"settled_outgoing_pix_90d"`
	InitiatedOutgoingPix24h *countObservation        `json:"initiated_outgoing_pix_24h"`
	PeriodLimit             conditional[periodLimit] `json:"period_limit,omitzero"`
}

// conditional is an observation that is omitted when not present, null
// when present without a value, and the value otherwise.
type conditional[T any] struct {
	present bool
	value   *T
}

func (c conditional[T]) IsZero() bool { return !c.present }

func (c conditional[T]) MarshalJSON() ([]byte, error) { return json.Marshal(c.value) }

type settledHistory struct {
	Count             int64  `json:"count"`
	MedianAmountCents *int64 `json:"median_amount_cents"`
	AsOf              string `json:"as_of"`
}

type countObservation struct {
	Count int64  `json:"count"`
	AsOf  string `json:"as_of"`
}

type periodLimit struct {
	LimitCents    int64  `json:"limit_cents"`
	ConsumedCents int64  `json:"consumed_cents"`
	AsOf          string `json:"as_of"`
}

type payerDevice struct {
	SettledOutgoingPix90d *countObservation `json:"settled_outgoing_pix_90d"`
	Registration          *registration     `json:"registration"`
}

type registration struct {
	Status string `json:"status"`
	AsOf   string `json:"as_of"`
}

type recipientPerson struct {
	SettledPixFromPayerAccount365d *countObservation  `json:"settled_pix_from_payer_account_365d"`
	Spi                            *spi               `json:"spi"`
	FraudMarkers                   *fraudMarkers      `json:"fraud_markers"`
	InfractionReports              *infractionReports `json:"infraction_reports"`
	Entries                        *personEntries     `json:"entries"`
}

type recipientAccount struct {
	Entry           *accountEntry `json:"entry"`
	PreRegistration *registration `json:"pre_registration"`
}

type accountEntry struct {
	OpeningDate string `json:"opening_date"`
	AsOf        string `json:"as_of"`
}

type recipientPixKey struct {
	Entry             *keyEntry          `json:"entry"`
	Spi               *spi               `json:"spi"`
	FraudMarkers      *fraudMarkers      `json:"fraud_markers"`
	InfractionReports *infractionReports `json:"infraction_reports"`
	Entries           *keyEntries        `json:"entries"`
}

type keyEntry struct {
	KeyType          string `json:"key_type"`
	CreationDate     string `json:"creation_date"`
	KeyOwnershipDate string `json:"key_ownership_date"`
	AsOf             string `json:"as_of"`
}

type window struct {
	D90 *int64 `json:"d90"`
	M12 *int64 `json:"m12"`
	M60 *int64 `json:"m60"`
}

type spi struct {
	Settlements window `json:"settlements"`
	AsOf        string `json:"as_of"`
}

type fraudMarkers struct {
	ApplicationFrauds      window `json:"application_frauds"`
	MuleAccounts           window `json:"mule_accounts"`
	ScammerAccounts        window `json:"scammer_accounts"`
	OtherFrauds            window `json:"other_frauds"`
	UnknownFrauds          window `json:"unknown_frauds"`
	DistinctFraudReporters window `json:"distinct_fraud_reporters"`
	AsOf                   string `json:"as_of"`
}

type infractionReports struct {
	OpenReports                  *int64 `json:"open_reports"`
	OpenReportsDistinctReporters *int64 `json:"open_reports_distinct_reporters"`
	RejectedReports              window `json:"rejected_reports"`
	AsOf                         string `json:"as_of"`
}

type personEntries struct {
	RegisteredAccounts *int64 `json:"registered_accounts"`
	AsOf               string `json:"as_of"`
}

type keyEntries struct {
	DistinctAccounts window `json:"distinct_accounts"`
	AsOf             string `json:"as_of"`
}
