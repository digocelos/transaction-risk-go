package pixrisk

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/digocelos/transaction-risk-go/pixinput"
)

// Reasons of a blocker: a required fact that is missing or stale.
const (
	ReasonMissing = "missing"
	ReasonStale   = "stale"
)

// Statuses of a note: why an optional source is not known.
const (
	StatusMissing       = "missing"
	StatusStale         = "stale"
	StatusNotApplicable = "not_applicable"
	StatusNoHistory     = "no_history"
	StatusZeroLimit     = "zero_limit"
	StatusUnrecognized  = "unrecognized"
)

// Maximum ages, which are engineering choices of pix-risk-v1.
const (
	recentMaxAge = 5 * time.Minute
	dailyMaxAge  = 24 * time.Hour
)

// Blocker is a required fact that is missing or stale. Path is a JSON
// Pointer into the scoring input.
type Blocker struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Note reports an optional source that is not known. Path is a JSON
// Pointer into the scoring input.
type Note struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// InsufficientInputError lists every blocker of a valid scoring input,
// sorted by path and then by reason.
type InsufficientInputError struct {
	Blockers []Blocker
}

func (e *InsufficientInputError) Error() string {
	first := e.Blockers[0]
	message := fmt.Sprintf("insufficient input for %s: %s: %s", Schema, first.Path, first.Reason)

	if len(e.Blockers) > 1 {
		message += fmt.Sprintf(" (and %d more blockers)", len(e.Blockers)-1)
	}

	return message
}

// Prepared holds the values of one scoring input before standardization,
// in descriptor order. An unavailable feature is 0 and so is its
// indicator. Notes lists every optional source that is not known, sorted by
// path and then by status; it is never nil.
type Prepared struct {
	Values [FeatureCount]float64
	Notes  []Note
}

// Prepare computes the features of a valid pix-payer-input-v2 scoring
// input. It reads no clock: every age is relative to the input's decision
// time. A missing or stale required fact yields an *InsufficientInputError
// and no values.
func Prepare(input pixinput.ScoringInput) (Prepared, error) {
	if input.ContractVersion != pixinput.ContractV2 {
		return Prepared{}, fmt.Errorf("%s requires a %s scoring input, got %q",
			Schema, pixinput.ContractV2, input.ContractVersion)
	}

	p := &preparer{input: input, decisionTime: input.DecisionTime}

	p.required()
	if len(p.blockers) > 0 {
		slices.SortFunc(p.blockers, func(a, b Blocker) int {
			return cmp.Or(strings.Compare(a.Path, b.Path), strings.Compare(a.Reason, b.Reason))
		})
		return Prepared{}, &InsufficientInputError{Blockers: p.blockers}
	}

	p.payment()
	p.payerHistory()
	p.periodLimit()
	p.preRegistration()
	p.payerDevice()
	p.recipientAccount()
	p.recipientPixKey()

	observations := input.Observations
	p.statistics(iPersonStatistics, "/observations/recipient_person",
		observations.RecipientPerson.Spi, observations.RecipientPerson.FraudMarkers,
		observations.RecipientPerson.InfractionReports, p.personAccounts())

	if key := observations.RecipientPixKey; key != nil {
		p.statistics(iKeyStatistics, "/observations/recipient_pix_key",
			key.Spi, key.FraudMarkers, key.InfractionReports, p.keyAccounts())
	}

	slices.SortFunc(p.notes, func(a, b Note) int {
		return cmp.Or(strings.Compare(a.Path, b.Path), strings.Compare(a.Status, b.Status))
	})
	p.prepared.Notes = slices.Compact(p.notes)
	if p.prepared.Notes == nil {
		p.prepared.Notes = []Note{}
	}

	return p.prepared, nil
}

// preparer collects the values, notes, and blockers of one input.
type preparer struct {
	input        pixinput.ScoringInput
	decisionTime time.Time
	prepared     Prepared
	notes        []Note
	blockers     []Blocker
}

func (p *preparer) set(i int, value float64) {
	p.prepared.Values[i] = value
}

func (p *preparer) note(path, status string) {
	p.notes = append(p.notes, Note{Path: path, Status: status})
}

// fresh reports whether an observation is no older than maxAge at decision
// time. The limit is inclusive.
func (p *preparer) fresh(asOf time.Time, maxAge time.Duration) bool {
	return p.decisionTime.Sub(asOf) <= maxAge
}

// known reports whether an optional observation is present and fresh,
// noting it otherwise.
func (p *preparer) known(present bool, path string, asOf func() time.Time, maxAge time.Duration) bool {
	switch {
	case !present:
		p.note(path, StatusMissing)
		return false
	case !p.fresh(asOf(), maxAge):
		p.note(path+"/as_of", StatusStale)
		return false
	}
	return true
}

// required checks the required facts.
func (p *preparer) required() {
	account := p.input.Observations.PayerAccount
	recipient := p.input.Observations.RecipientPerson.SettledPixFromPayerAccount365d

	check := func(path string, present bool, asOf func() time.Time, maxAge time.Duration) {
		switch {
		case !present:
			p.blockers = append(p.blockers, Blocker{Path: path, Reason: ReasonMissing})
		case !p.fresh(asOf(), maxAge):
			p.blockers = append(p.blockers, Blocker{Path: path + "/as_of", Reason: ReasonStale})
		}
	}

	check("/observations/payer_account/settled_outgoing_pix_90d", account.SettledOutgoingPix90d != nil,
		func() time.Time { return account.SettledOutgoingPix90d.AsOf }, dailyMaxAge)
	check("/observations/payer_account/initiated_outgoing_pix_24h", account.InitiatedOutgoingPix24h != nil,
		func() time.Time { return account.InitiatedOutgoingPix24h.AsOf }, recentMaxAge)
	check("/observations/recipient_person/settled_pix_from_payer_account_365d", recipient != nil,
		func() time.Time { return recipient.AsOf }, dailyMaxAge)
}

func indicator(condition bool) float64 {
	if condition {
		return 1
	}
	return 0
}

// payment prepares the features of the Pix payment and its time context.
func (p *preparer) payment() {
	payment := p.input.PixPayment

	p.set(iAmount, math.Log1p(float64(payment.AmountCents)))
	p.set(iRecipientLegalPerson, indicator(payment.RecipientPersonType == pixinput.LegalPerson))
	p.set(iNoPixKey, indicator(payment.RecipientKeyUsage == pixinput.NoPixKey))
	p.set(iNoPayerDevice, indicator(payment.PayerDeviceUsage == pixinput.NoPayerDevice))

	// Seconds since local midnight; Unix rounds down, dropping fractions.
	seconds := (p.decisionTime.Unix() + int64(payment.ReferenceTimeOffsetMinutes)*60) % 86400
	if seconds < 0 {
		seconds += 86400
	}
	theta := 2 * math.Pi * float64(seconds) / 86400
	p.set(iLocalTimeSin, math.Sin(theta))
	p.set(iLocalTimeCos, math.Cos(theta))

	p.set(iLimitPeriodNighttime, indicator(payment.PayerLimitPeriod == pixinput.Nighttime))
	p.set(iLimitPeriodDaily, indicator(payment.PayerLimitPeriod == pixinput.Daily))
	p.set(iLimitPeriodNone, indicator(payment.PayerLimitPeriod == pixinput.NoPeriodLimit))
}

// payerHistory prepares the required payer account histories.
func (p *preparer) payerHistory() {
	account := p.input.Observations.PayerAccount
	settled := account.SettledOutgoingPix90d

	p.set(iSettledCount90d, math.Log1p(float64(settled.Count)))
	p.set(iInitiatedCount24h, math.Log1p(float64(account.InitiatedOutgoingPix24h.Count)))
	p.set(iRecipientPayments365d,
		math.Log1p(float64(p.input.Observations.RecipientPerson.SettledPixFromPayerAccount365d.Count)))

	if settled.Count == 0 {
		p.note("/observations/payer_account/settled_outgoing_pix_90d/median_amount_cents", StatusNoHistory)
		return
	}

	p.set(iAmountToMedian90d,
		math.Log(float64(p.input.PixPayment.AmountCents))-math.Log(float64(*settled.MedianAmountCents)))
	p.set(iAmountToMedian90dKnown, 1)
}

func (p *preparer) periodLimit() {
	const path = "/observations/payer_account/period_limit"

	if p.input.PixPayment.PayerLimitPeriod == pixinput.NoPeriodLimit {
		p.note("/pix_payment/payer_limit_period", StatusNotApplicable)
		return
	}

	limit := p.input.Observations.PayerAccount.PeriodLimit
	if !p.known(limit != nil, path, func() time.Time { return limit.AsOf }, recentMaxAge) {
		return
	}

	if limit.LimitCents == 0 {
		p.set(iLimitZero, 1)
		p.note(path+"/limit_cents", StatusZeroLimit)
		return
	}

	utilization := (float64(limit.ConsumedCents) + float64(p.input.PixPayment.AmountCents)) / float64(limit.LimitCents)
	p.set(iLimitUtilization, math.Log1p(utilization))
	p.set(iLimitUtilizationKnown, 1)
}

func (p *preparer) preRegistration() {
	registration := p.input.Observations.RecipientAccount.PreRegistration
	if p.known(registration != nil, "/observations/recipient_account/pre_registration",
		func() time.Time { return registration.AsOf }, recentMaxAge) {
		p.set(iRecipientPreRegistered, indicator(registration.Status == pixinput.Registered))
		p.set(iRecipientPreRegisteredKnown, 1)
	}
}

func (p *preparer) payerDevice() {
	if p.input.PixPayment.PayerDeviceUsage == pixinput.NoPayerDevice {
		p.note("/pix_payment/payer_device_usage", StatusNotApplicable)
		return
	}

	device := p.input.Observations.PayerDevice

	history := device.SettledOutgoingPix90d
	if p.known(history != nil, "/observations/payer_device/settled_outgoing_pix_90d",
		func() time.Time { return history.AsOf }, dailyMaxAge) {
		p.set(iDeviceSettledCount90d, math.Log1p(float64(history.Count)))
		p.set(iDeviceSettledCount90dKnown, 1)
	}

	registration := device.Registration
	if p.known(registration != nil, "/observations/payer_device/registration",
		func() time.Time { return registration.AsOf }, recentMaxAge) {
		p.set(iDeviceRegistered, indicator(registration.Status == pixinput.Registered))
		p.set(iDeviceRegisteredKnown, 1)
	}
}

// ageInDays counts the complete blocks of 24 hours from date to decision
// time, from exact instants and without a saturating duration. Validation
// guarantees that date is not later than decision time.
func (p *preparer) ageInDays(date time.Time) float64 {
	seconds := p.decisionTime.Unix() - date.Unix()
	if p.decisionTime.Nanosecond() < date.Nanosecond() {
		seconds--
	}
	return float64(seconds / 86400)
}

func (p *preparer) recipientAccount() {
	entry := p.input.Observations.RecipientAccount.Entry
	if p.known(entry != nil, "/observations/recipient_account/entry",
		func() time.Time { return entry.AsOf }, dailyMaxAge) {
		p.set(iRecipientAccountAge, math.Log1p(p.ageInDays(entry.OpeningDate)))
		p.set(iRecipientAccountAgeKnown, 1)
	}
}

// keyTypes maps the recognized key types to their features.
var keyTypes = map[string]int{
	"CPF":   iKeyTypeCPF,
	"CNPJ":  iKeyTypeCNPJ,
	"PHONE": iKeyTypePhone,
	"EMAIL": iKeyTypeEmail,
	"EVP":   iKeyTypeEVP,
}

func (p *preparer) recipientPixKey() {
	const path = "/observations/recipient_pix_key/entry"

	if p.input.PixPayment.RecipientKeyUsage == pixinput.NoPixKey {
		p.note("/pix_payment/recipient_key_usage", StatusNotApplicable)
		return
	}

	entry := p.input.Observations.RecipientPixKey.Entry
	if !p.known(entry != nil, path, func() time.Time { return entry.AsOf }, dailyMaxAge) {
		return
	}

	p.set(iKeyLinkAge, math.Log1p(p.ageInDays(entry.CreationDate)))
	p.set(iKeyOwnershipAge, math.Log1p(p.ageInDays(entry.KeyOwnershipDate)))

	if i, ok := keyTypes[entry.KeyType]; ok {
		p.set(i, 1)
	} else {
		p.set(iKeyTypeOther, 1)
		p.note(path+"/key_type", StatusUnrecognized)
	}

	p.set(iKeyEntryKnown, 1)
}

// accounts is the last DICT statistics feature of a subject: its entries
// group and how to read the counter it uses.
type accounts struct {
	present bool
	asOf    func() time.Time
	value   func() (*int64, string)
}

func (p *preparer) personAccounts() accounts {
	entries := p.input.Observations.RecipientPerson.Entries
	return accounts{
		present: entries != nil,
		asOf:    func() time.Time { return entries.AsOf },
		value: func() (*int64, string) {
			return entries.RegisteredAccounts, "/registered_accounts"
		},
	}
}

func (p *preparer) keyAccounts() accounts {
	entries := p.input.Observations.RecipientPixKey.Entries
	return accounts{
		present: entries != nil,
		asOf:    func() time.Time { return entries.AsOf },
		value: func() (*int64, string) {
			if entries.DistinctAccounts == nil {
				return nil, "/distinct_accounts"
			}
			return entries.DistinctAccounts.M12, "/distinct_accounts/m12"
		},
	}
}

// count sets a standardized feature and its indicator from a counter
// value, or notes the counter at path as missing.
func (p *preparer) count(i int, value *int64, path string) {
	if value == nil {
		p.note(path, StatusMissing)
		return
	}

	p.set(i, math.Log1p(float64(*value)))
	p.set(i+1, 1)
}

// windowCount sets a standardized feature from one window of a counter.
func (p *preparer) windowCount(i int, counter *pixinput.WindowCounter, path string,
	pick func(*pixinput.WindowCounter) *int64, name string) {
	value, valuePath := window(counter, path, pick, name)
	p.count(i, value, valuePath)
}

// window returns one window of a counter and its path, or the counter's
// path when the whole counter is missing.
func window(counter *pixinput.WindowCounter, path string, pick func(*pixinput.WindowCounter) *int64, name string) (*int64, string) {
	if counter == nil {
		return nil, path
	}
	return pick(counter), path + "/" + name
}

func d90(c *pixinput.WindowCounter) *int64 { return c.D90 }
func m12(c *pixinput.WindowCounter) *int64 { return c.M12 }

// statistics prepares the DICT statistics features of a subject starting
// at first. Each feature is available only when its group is known and
// every value it reads is present.
func (p *preparer) statistics(
	first int,
	base string,
	spi *pixinput.SpiStatistics,
	markers *pixinput.FraudMarkers,
	reports *pixinput.InfractionReports,
	entries accounts,
) {
	if p.known(spi != nil, base+"/spi", func() time.Time { return spi.AsOf }, dailyMaxAge) {
		p.windowCount(first+oSettlementsD90, spi.Settlements, base+"/spi/settlements", d90, "d90")
		p.windowCount(first+oSettlementsM12, spi.Settlements, base+"/spi/settlements", m12, "m12")
	}

	if p.known(markers != nil, base+"/fraud_markers", func() time.Time { return markers.AsOf }, dailyMaxAge) {
		p.markerSum(first+oFraudMarkersD90, base+"/fraud_markers", markers, d90, "d90")
		p.markerSum(first+oFraudMarkersM12, base+"/fraud_markers", markers, m12, "m12")
		p.windowCount(first+oFraudReportersM12, markers.DistinctFraudReporters,
			base+"/fraud_markers/distinct_fraud_reporters", m12, "m12")
	}

	if p.known(reports != nil, base+"/infraction_reports", func() time.Time { return reports.AsOf }, dailyMaxAge) {
		p.count(first+oOpenReportReporters, reports.OpenReportsDistinctReporters,
			base+"/infraction_reports/open_reports_distinct_reporters")
	}

	if p.known(entries.present, base+"/entries", entries.asOf, dailyMaxAge) {
		value, path := entries.value()
		p.count(first+oAccounts, value, base+"/entries"+path)
	}
}

// markerSum sets the sum of the five fraud marker types for one window. It
// is available only when every addend is present, and is computed exactly
// before the conversion.
func (p *preparer) markerSum(i int, path string, markers *pixinput.FraudMarkers,
	pick func(*pixinput.WindowCounter) *int64, name string) {
	counters := []struct {
		name    string
		counter *pixinput.WindowCounter
	}{
		{"application_frauds", markers.ApplicationFrauds},
		{"mule_accounts", markers.MuleAccounts},
		{"scammer_accounts", markers.ScammerAccounts},
		{"other_frauds", markers.OtherFrauds},
		{"unknown_frauds", markers.UnknownFrauds},
	}

	var sum int64
	complete := true

	for _, c := range counters {
		value, valuePath := window(c.counter, path+"/"+c.name, pick, name)
		if value == nil {
			p.note(valuePath, StatusMissing)
			complete = false
			continue
		}
		sum += *value
	}

	if complete {
		p.set(i, math.Log1p(float64(sum)))
		p.set(i+1, 1)
	}
}
