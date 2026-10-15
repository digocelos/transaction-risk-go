// Package pixinput decodes and validates scoring inputs for payer-side Pix
// analysis, as defined by the pix-payer-input-v2 input contract.
package pixinput

import "time"

// ContractV2 is the contract version this package supports. Callers state
// which versions they accept, so a later version is never accepted
// silently (ADR 0006).
const ContractV2 = "pix-payer-input-v2"

const (
	// MaxDocumentSize is the largest scoring input document, in bytes.
	MaxDocumentSize = 65536
)

// PayerDeviceUsage states whether the payer authorized the Pix payment on a device.
type PayerDeviceUsage string

const (
	PayerDevice   PayerDeviceUsage = "payer_device"
	NoPayerDevice PayerDeviceUsage = "no_payer_device"
)

// RecipientKeyUsage states whether a Pix key identified the recipient.
type RecipientKeyUsage string

const (
	PixKey   RecipientKeyUsage = "pix_key"
	NoPixKey RecipientKeyUsage = "no_pix_key"
)

// PersonType distinguishes natural from legal persons.
type PersonType string

const (
	NaturalPerson PersonType = "NATURAL_PERSON"
	LegalPerson   PersonType = "LEGAL_PERSON"
)

// LimitPeriod is the limit period the payer's PSP applies to the payment.
type LimitPeriod string

const (
	Daytime       LimitPeriod = "daytime"
	Nighttime     LimitPeriod = "nighttime"
	Daily         LimitPeriod = "daily"
	NoPeriodLimit LimitPeriod = "no_period_limit"
)

// RegistrationStatus states whether a registration is in effect.
type RegistrationStatus string

const (
	Registered    RegistrationStatus = "registered"
	NotRegistered RegistrationStatus = "not_registered"
)

// ScoringInput is one valid scoring input document.
type ScoringInput struct {
	ContractVersion string
	DecisionTime    time.Time
	PixPayment      PixPayment
	Observations    Observations
}

// PixPayment holds the facts of the Pix payment being scored.
type PixPayment struct {
	AmountCents         int64
	PayerDeviceUsage    PayerDeviceUsage
	RecipientKeyUsage   RecipientKeyUsage
	RecipientPersonType PersonType

	// ReferenceTimeOffsetMinutes is the UTC offset, in minutes east of
	// UTC, of the time reference the PSP uses for the payer's limit periods.
	ReferenceTimeOffsetMinutes int
	PayerLimitPeriod           LimitPeriod
}

// Observations groups observations by subject. A nil observation is missing.
// PayerDevice and RecipientPixKey are nil when they do not apply.
type Observations struct {
	PayerAccount     PayerAccount
	PayerDevice      *PayerDeviceSubject
	RecipientPerson  RecipientPerson
	RecipientAccount RecipientAccount
	RecipientPixKey  *RecipientPixKey
}

// PayerAccount holds the history of the account the payment debits.
// PeriodLimit is also nil when PayerLimitPeriod is NoPeriodLimit.
type PayerAccount struct {
	SettledOutgoingPix90d   *SettledHistory
	InitiatedOutgoingPix24h *CountObservation
	PeriodLimit             *PeriodLimit
}

// PayerDeviceSubject holds the activity of the device that authorized the payment.
type PayerDeviceSubject struct {
	SettledOutgoingPix90d *CountObservation
	Registration          *Registration
}

// RecipientPerson holds the payer account's relationship with the recipient
// person and the person's DICT statistics.
type RecipientPerson struct {
	SettledPixFromPayerAccount365d *CountObservation
	Spi                            *SpiStatistics
	FraudMarkers                   *FraudMarkers
	InfractionReports              *InfractionReports
	Entries                        *PersonEntries
}

// RecipientAccount holds DICT entry data about the recipient account and
// whether a pre-registration by the payer applies to it.
type RecipientAccount struct {
	Entry           *AccountEntry
	PreRegistration *Registration
}

// RecipientPixKey holds DICT data about the Pix key that identified the recipient.
type RecipientPixKey struct {
	Entry             *KeyEntry
	Spi               *SpiStatistics
	FraudMarkers      *FraudMarkers
	InfractionReports *InfractionReports
	Entries           *KeyEntries
}

// SettledHistory counts settled payments and their lower median amount.
// MedianAmountCents is nil exactly when Count is zero.
type SettledHistory struct {
	Count             int64
	MedianAmountCents *int64
	AsOf              time.Time
}

// PeriodLimit is the limit in effect for the payment's limit period and the
// amount already counted against it, excluding the payment.
type PeriodLimit struct {
	LimitCents    int64
	ConsumedCents int64
	AsOf          time.Time
}

// Registration states whether a registration is in effect at AsOf.
type Registration struct {
	Status RegistrationStatus
	AsOf   time.Time
}

// CountObservation is a count over a contract-defined lookback window.
type CountObservation struct {
	Count int64
	AsOf  time.Time
}

// AccountEntry mirrors the DICT account data of the recipient.
type AccountEntry struct {
	OpeningDate time.Time
	AsOf        time.Time
}

// KeyEntry mirrors the DICT entry data of the recipient Pix key.
type KeyEntry struct {
	KeyType          string
	CreationDate     time.Time
	KeyOwnershipDate time.Time
	AsOf             time.Time
}

// WindowCounter mirrors a DICT WindowedValue. A nil window was omitted by DICT.
type WindowCounter struct {
	D90 *int64
	M12 *int64
	M60 *int64
}

// SpiStatistics mirrors the DICT Spi group.
type SpiStatistics struct {
	Settlements *WindowCounter
	AsOf        time.Time
}

// FraudMarkers mirrors the DICT FraudMarkers group.
type FraudMarkers struct {
	ApplicationFrauds      *WindowCounter
	MuleAccounts           *WindowCounter
	ScammerAccounts        *WindowCounter
	OtherFrauds            *WindowCounter
	UnknownFrauds          *WindowCounter
	DistinctFraudReporters *WindowCounter
	AsOf                   time.Time
}

// InfractionReports mirrors the DICT InfractionReports group.
type InfractionReports struct {
	OpenReports                  *int64
	OpenReportsDistinctReporters *int64
	RejectedReports              *WindowCounter
	AsOf                         time.Time
}

// PersonEntries mirrors the DICT Entries group of a person.
type PersonEntries struct {
	RegisteredAccounts *int64
	AsOf               time.Time
}

// KeyEntries mirrors the DICT Entries group of a Pix key.
type KeyEntries struct {
	DistinctAccounts *WindowCounter
	AsOf             time.Time
}
