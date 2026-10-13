package transaction

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/digocelos/transaction-risk-go/pixinput"
)

// Reasons why a valid scoring input cannot produce the features of this
// feature schema.
const (
	ReasonMissing       = "missing"
	ReasonStale         = "stale"
	ReasonNoHistory     = "no_history"
	ReasonNotApplicable = "not_applicable"
)

// Maximum ages of the observations this feature schema consumes.
const (
	recentCountMaxAge = 5 * time.Minute
	historyMaxAge     = 24 * time.Hour
)

const (
	settledPath          = "/observations/payer_account/settled_outgoing_pix_90d"
	initiatedPath        = "/observations/payer_account/initiated_outgoing_pix_24h"
	recipientPath        = "/observations/recipient_person/settled_pix_from_payer_account_365d"
	devicePath           = "/observations/payer_device/settled_outgoing_pix_90d"
	payerDeviceUsagePath = "/pix_payment/payer_device_usage"
)

// Blocker is one reason why conversion cannot produce features.
// Path is a JSON Pointer into the scoring input.
type Blocker struct {
	Path   string
	Reason string
}

// InsufficientInputError lists every blocker of a valid scoring input,
// sorted by path and then by reason.
type InsufficientInputError struct {
	Blockers []Blocker
}

func (e *InsufficientInputError) Error() string {
	first := e.Blockers[0]
	message := fmt.Sprintf("insufficient input for %s: %s: %s", FeatureSchema, first.Path, first.Reason)

	if len(e.Blockers) > 1 {
		message += fmt.Sprintf(" (and %d more blockers)", len(e.Blockers)-1)
	}

	return message
}

// FromScoringInput converts a valid scoring input to the features of this
// feature schema. It never imputes a value: when information is missing,
// stale, or not applicable, it returns an *InsufficientInputError with
// every blocker and no features.
func FromScoringInput(input pixinput.ScoringInput) ([FeatureCount]float64, error) {
	var inputs [FeatureCount]float64

	b := blockers{decisionTime: input.DecisionTime}
	observations := input.Observations

	settled := observations.PayerAccount.SettledOutgoingPix90d
	if settled == nil {
		b.add(settledPath, ReasonMissing)
	} else {
		b.checkAge(settledPath, settled.AsOf, historyMaxAge)
		if settled.Count == 0 {
			b.add(settledPath+"/median_amount_cents", ReasonNoHistory)
		}
	}

	initiated := observations.PayerAccount.InitiatedOutgoingPix24h
	if initiated == nil {
		b.add(initiatedPath, ReasonMissing)
	} else {
		b.checkAge(initiatedPath, initiated.AsOf, recentCountMaxAge)
	}

	recipient := observations.RecipientPerson.SettledPixFromPayerAccount365d
	if recipient == nil {
		b.add(recipientPath, ReasonMissing)
	} else {
		b.checkAge(recipientPath, recipient.AsOf, historyMaxAge)
	}

	var device *pixinput.CountObservation
	if input.PixPayment.PayerDeviceUsage == pixinput.NoPayerDevice {
		b.add(payerDeviceUsagePath, ReasonNotApplicable)
	} else {
		if observations.PayerDevice != nil {
			device = observations.PayerDevice.SettledOutgoingPix90d
		}

		if device == nil {
			b.add(devicePath, ReasonMissing)
		} else {
			b.checkAge(devicePath, device.AsOf, historyMaxAge)
		}
	}

	if len(b.list) > 0 {
		slices.SortFunc(b.list, func(x, y Blocker) int {
			return cmp.Or(cmp.Compare(x.Path, y.Path), cmp.Compare(x.Reason, y.Reason))
		})
		return inputs, &InsufficientInputError{Blockers: b.list}
	}

	// Reuse the legacy extraction so features match it bit for bit.
	return Features(Transaction{
		AmountCents:        input.PixPayment.AmountCents,
		TypicalAmountCents: *settled.MedianAmountCents,
		RecentCount:        int(initiated.Count),
		NewRecipient:       recipient.Count == 0,
		NewDevice:          device.Count == 0,
	})
}

// blockers collects the reasons why conversion cannot produce features.
type blockers struct {
	decisionTime time.Time
	list         []Blocker
}

func (b *blockers) add(path, reason string) {
	b.list = append(b.list, Blocker{Path: path, Reason: reason})
}

// checkAge reports an observation older than maxAge at decision time.
// The limit is inclusive.
func (b *blockers) checkAge(path string, asOf time.Time, maxAge time.Duration) {
	if b.decisionTime.Sub(asOf) > maxAge {
		b.add(path+"/as_of", ReasonStale)
	}
}
