// Package synthetic generates synthetic pix-training-example-v2 datasets
// for measuring the toolkit: valid records with a skewed class balance,
// fraud that shifts several feature distributions, and the absence
// patterns of real integrations, such as payments without a Pix key or
// payer device, missing DICT data, stale observations, insufficient
// inputs, immature labels, and unknown losses.
//
// The data is made up. It exercises the software at realistic sizes and
// shapes; it says nothing about real fraud, and a model trained on it is
// not a fraud model.
package synthetic

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"time"
)

// Options configure a dataset. Records are generated in decision time
// order, spread evenly from Start to End, and their labels are known as of
// LabelAsOf, so records decided less than 90 days before it are immature.
// Accounts is the number of payer accounts, used as group keys.
type Options struct {
	Records   int
	Seed      uint64
	FraudRate float64
	Start     time.Time
	End       time.Time
	LabelAsOf time.Time
	Accounts  int
}

// Defaults of the generated time range: decision times from 2025-01-01 to
// 2026-06-30, labels known as of 2026-08-31.
var (
	DefaultStart     = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	DefaultEnd       = time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	DefaultLabelAsOf = time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
)

// DefaultFraudRate is the default share of fraud records.
const DefaultFraudRate = 0.01

// DefaultOptions returns the options of a dataset with the default time
// range and fraud rate, and one payer account per 25 records.
func DefaultOptions(records int, seed uint64) Options {
	return Options{
		Records:   records,
		Seed:      seed,
		FraudRate: DefaultFraudRate,
		Start:     DefaultStart,
		End:       DefaultEnd,
		LabelAsOf: DefaultLabelAsOf,
		Accounts:  max(1, records/25),
	}
}

// Validate checks the options Generate needs.
func (o Options) Validate() error {
	switch {
	case o.Records < 1:
		return errors.New("records must be at least 1")
	case !(o.FraudRate > 0 && o.FraudRate < 1):
		return errors.New("the fraud rate must be greater than 0 and less than 1")
	case o.Start.IsZero() || !o.Start.Before(o.End):
		return errors.New("start must be set and earlier than end")
	case o.LabelAsOf.Before(o.End):
		return errors.New("labels must be known as of end or later")
	case o.Accounts < 1:
		return errors.New("accounts must be at least 1")
	}
	return nil
}

// Generate writes a dataset of options.Records JSON Lines records to w.
// The same options always produce the same bytes.
func Generate(w io.Writer, options Options) error {
	if err := options.Validate(); err != nil {
		return fmt.Errorf("synthetic dataset: %w", err)
	}

	buffered := bufio.NewWriter(w)
	encoder := json.NewEncoder(buffered)
	encoder.SetEscapeHTML(false)

	g := &generator{
		random:  rand.New(rand.NewPCG(options.Seed, 0x5eed_0f_9a7a)),
		options: options,
	}
	step := options.End.Sub(options.Start) / time.Duration(options.Records)

	for i := range options.Records {
		decisionTime := options.Start.Add(time.Duration(i) * step).
			Add(time.Duration(g.random.Int64N(int64(max(step, time.Millisecond))))).
			Truncate(time.Millisecond)

		if err := encoder.Encode(g.record(decisionTime)); err != nil {
			return fmt.Errorf("synthetic dataset: %w", err)
		}
	}

	if err := buffered.Flush(); err != nil {
		return fmt.Errorf("synthetic dataset: %w", err)
	}
	return nil
}

// Shares of the absence patterns.
const (
	shareNoPixKey           = 0.20
	shareNoPayerDevice      = 0.10
	shareNoPeriodLimit      = 0.15
	shareLegalRecipient     = 0.20
	shareNewPayerAccount    = 0.05
	shareDICTOutage         = 0.03
	shareMissingGroup       = 0.04
	shareMissingWindow      = 0.02
	shareMissingOptional    = 0.04
	shareStaleOptional      = 0.03
	shareInsufficient       = 0.02
	shareZeroLimit          = 0.02
	shareUnrecognizedKey    = 0.01
	shareOtherOffset        = 0.05
	shareUnknownFraudLoss   = 0.30
	shareRegisteredDevice   = 0.85
	shareFraudRegistered    = 0.40
	sharePreRegistered      = 0.10
	shareFraudPreRegistered = 0.01
)

type generator struct {
	random  *rand.Rand
	options Options
}

func (g *generator) chance(p float64) bool {
	return g.random.Float64() < p
}

// logNormal draws a log-normal integer with the given median, at least 1
// and at most 2^40.
func (g *generator) logNormal(median, sigma float64) int64 {
	value := median * math.Exp(sigma*g.random.NormFloat64())
	return int64(math.Max(1, math.Min(value, 1<<40)))
}

// count draws a non-negative count around mean.
func (g *generator) count(mean float64) int64 {
	if mean <= 0 {
		return 0
	}
	return int64(math.Round(math.Max(0, mean*math.Exp(0.5*g.random.NormFloat64()))))
}

func (g *generator) record(decisionTime time.Time) trainingExample {
	fraud := g.chance(g.options.FraudRate)
	input := g.scoringInput(decisionTime, fraud)

	outcome := outcome{Label: "legitimate", LabelAsOf: g.options.LabelAsOf.Format(time.RFC3339)}
	zero := int64(0)
	outcome.LossAmountCents = &zero

	if fraud {
		outcome.Label = "fraud"
		if g.chance(shareUnknownFraudLoss) {
			outcome.LossAmountCents = nil
		} else {
			loss := int64(float64(input.PixPayment.AmountCents) * (0.3 + 0.7*g.random.Float64()))
			loss = min(max(loss, 1), input.PixPayment.AmountCents)
			outcome.LossAmountCents = &loss
		}
	}

	groupKey := fmt.Sprintf("acct-%07d", g.random.IntN(g.options.Accounts))

	return trainingExample{
		TrainingExampleVersion: "pix-training-example-v2",
		ScoringInput:           input,
		Outcome:                outcome,
		GroupKey:               &groupKey,
	}
}

var brasilia = time.FixedZone("-03:00", -3*60*60)

func (g *generator) scoringInput(decisionTime time.Time, fraud bool) scoringInput {
	offset, zone := "-03:00", brasilia
	if g.chance(shareOtherOffset) {
		offset, zone = "-04:00", time.FixedZone("-04:00", -4*60*60)
	}
	local := decisionTime.In(zone)

	payment := pixPayment{
		AmountCents:         g.logNormal(15_000, 1.2),
		PayerDeviceUsage:    "payer_device",
		RecipientKeyUsage:   "pix_key",
		RecipientPersonType: "NATURAL_PERSON",
		ReferenceTimeOffset: offset,
	}
	if fraud {
		payment.AmountCents = g.logNormal(120_000, 1.0)
	}
	if g.chance(shareNoPayerDevice) {
		payment.PayerDeviceUsage = "no_payer_device"
	}
	if g.chance(shareNoPixKey) {
		payment.RecipientKeyUsage = "no_pix_key"
	}

	legal := g.chance(shareLegalRecipient)
	switch {
	case g.chance(shareNoPeriodLimit):
		payment.PayerLimitPeriod = "no_period_limit"
	case legal:
		payment.RecipientPersonType = "LEGAL_PERSON"
		payment.PayerLimitPeriod = "daily"
	case local.Hour() >= 6 && local.Hour() < 20:
		payment.PayerLimitPeriod = "daytime"
	default:
		payment.PayerLimitPeriod = "nighttime"
	}

	t := times{decision: decisionTime}
	input := scoringInput{
		ContractVersion: "pix-payer-input-v2",
		DecisionTime:    local.Format("2006-01-02T15:04:05.000-07:00"),
		PixPayment:      payment,
	}

	input.Observations.PayerAccount = g.payerAccount(t, payment, fraud)
	if payment.PayerDeviceUsage == "payer_device" {
		device := g.payerDevice(t, fraud)
		input.Observations.PayerDevice = &device
	}

	outage := g.chance(shareDICTOutage)
	input.Observations.RecipientPerson = g.recipientPerson(t, fraud, outage)
	input.Observations.RecipientAccount = g.recipientAccount(t, fraud)
	if payment.RecipientKeyUsage == "pix_key" {
		key := g.recipientPixKey(t, fraud, legal, outage)
		input.Observations.RecipientPixKey = &key
	}

	return input
}

// times derives the as_of instants of observations from the decision time.
type times struct {
	decision time.Time
}

func format(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// daily is the as_of of a daily batch: 06:00 UTC of the decision day, or
// of the day before when the decision is earlier.
func (t times) daily() string {
	batch := time.Date(t.decision.Year(), t.decision.Month(), t.decision.Day(), 6, 0, 0, 0, time.UTC)
	if batch.After(t.decision) {
		batch = batch.AddDate(0, 0, -1)
	}
	return format(batch)
}

// recent is the as_of of a real-time lookup, just before the decision.
func (t times) recent() string { return format(t.decision.Add(-246 * time.Millisecond)) }

// dict is the as_of of a DICT lookup.
func (t times) dict() string { return format(t.decision.Add(-time.Second)) }

// stale24h and stale5m are older than the maximum ages of pix-risk-v1.
func (t times) stale24h() string { return format(t.decision.Add(-49 * time.Hour)) }
func (t times) stale5m() string  { return format(t.decision.Add(-11 * time.Minute)) }

// optional returns nil for a missing observation, or the as_of to use:
// stale or fresh.
func (g *generator) optional(fresh, stale string) *string {
	switch {
	case g.chance(shareMissingOptional):
		return nil
	case g.chance(shareStaleOptional):
		return &stale
	default:
		return &fresh
	}
}

func (g *generator) payerAccount(t times, payment pixPayment, fraud bool) payerAccount {
	account := payerAccount{}

	history := &settledHistory{Count: g.count(30), AsOf: t.daily()}
	if g.chance(shareNewPayerAccount) {
		history.Count = 0
	}
	if history.Count > 0 {
		median := g.logNormal(12_000, 0.8)
		history.MedianAmountCents = &median
	}

	recent := &countObservation{Count: g.count(1.5), AsOf: t.recent()}
	if fraud {
		recent.Count = g.count(6)
	}

	if g.chance(shareInsufficient) {
		if g.chance(0.5) {
			history = nil
		} else {
			recent.AsOf = t.stale5m()
		}
	}
	account.SettledOutgoingPix90d = history
	account.InitiatedOutgoingPix24h = recent

	if payment.PayerLimitPeriod != "no_period_limit" {
		account.PeriodLimit.present = true
		if asOf := g.optional(t.recent(), t.stale5m()); asOf != nil {
			limit := 100_000 * (1 + g.random.Int64N(20))
			if g.chance(shareZeroLimit) {
				limit = 0
			}
			consumed := int64(0)
			if limit > 0 {
				consumed = g.random.Int64N(limit + 1)
			}
			account.PeriodLimit.value = &periodLimit{LimitCents: limit, ConsumedCents: consumed, AsOf: *asOf}
		}
	}

	return account
}

func (g *generator) payerDevice(t times, fraud bool) payerDevice {
	device := payerDevice{}

	if asOf := g.optional(t.daily(), t.stale24h()); asOf != nil {
		count := g.count(20)
		if fraud && g.chance(0.6) {
			count = 0
		}
		device.SettledOutgoingPix90d = &countObservation{Count: count, AsOf: *asOf}
	}

	if asOf := g.optional(t.recent(), t.stale5m()); asOf != nil {
		status := "not_registered"
		if (!fraud && g.chance(shareRegisteredDevice)) || (fraud && g.chance(shareFraudRegistered)) {
			status = "registered"
		}
		device.Registration = &registration{Status: status, AsOf: *asOf}
	}

	return device
}

func (g *generator) recipientPerson(t times, fraud, outage bool) recipientPerson {
	count := g.count(8)
	if g.chance(0.4) || (fraud && g.chance(0.95)) {
		count = 0
	}

	person := recipientPerson{
		SettledPixFromPayerAccount365d: &countObservation{Count: count, AsOf: t.daily()},
	}

	if g.dictGroup(outage) {
		person.Spi = g.spi(t, fraud)
	}
	if g.dictGroup(outage) {
		person.FraudMarkers = g.fraudMarkers(t, fraud)
	}
	if g.dictGroup(outage) {
		person.InfractionReports = g.infractionReports(t, fraud)
	}
	if g.dictGroup(outage) {
		person.Entries = &personEntries{RegisteredAccounts: g.window1(1 + g.random.Int64N(5)), AsOf: g.dictAsOf(t)}
	}

	return person
}

func (g *generator) recipientAccount(t times, fraud bool) recipientAccount {
	account := recipientAccount{}

	if asOf := g.optional(t.dict(), t.stale24h()); asOf != nil {
		age := time.Duration(30+g.random.IntN(3000)) * 24 * time.Hour
		if fraud && g.chance(0.7) {
			age = time.Duration(1+g.random.IntN(20)) * 24 * time.Hour
		}
		opening := t.decision.Add(-49*time.Hour - age).Truncate(24 * time.Hour)
		account.Entry = &accountEntry{OpeningDate: format(opening), AsOf: *asOf}
	}

	if asOf := g.optional(t.recent(), t.stale5m()); asOf != nil {
		status := "not_registered"
		if (!fraud && g.chance(sharePreRegistered)) || (fraud && g.chance(shareFraudPreRegistered)) {
			status = "registered"
		}
		account.PreRegistration = &registration{Status: status, AsOf: *asOf}
	}

	return account
}

func (g *generator) recipientPixKey(t times, fraud, legal, outage bool) recipientPixKey {
	key := recipientPixKey{}

	if asOf := g.optional(t.dict(), t.stale24h()); asOf != nil {
		keyType := g.keyType(legal)
		linkAge := time.Duration(1+g.random.IntN(1500)) * 24 * time.Hour
		if fraud && g.chance(0.7) {
			linkAge = time.Duration(1+g.random.IntN(10)) * 24 * time.Hour
		}
		ownershipAge := linkAge + time.Duration(g.random.IntN(800))*24*time.Hour
		// Both dates precede the stale as_of too, so either as_of is valid.
		created := t.decision.Add(-49*time.Hour - linkAge)
		owned := t.decision.Add(-49*time.Hour - ownershipAge)
		key.Entry = &keyEntry{KeyType: keyType, CreationDate: format(created), KeyOwnershipDate: format(owned), AsOf: *asOf}
	}

	if g.dictGroup(outage) {
		key.Spi = g.spi(t, fraud)
	}
	if g.dictGroup(outage) {
		key.FraudMarkers = g.fraudMarkers(t, fraud)
	}
	if g.dictGroup(outage) {
		key.InfractionReports = g.infractionReports(t, fraud)
	}
	if g.dictGroup(outage) {
		accounts := 1 + g.random.Int64N(2)
		if fraud && g.chance(0.4) {
			accounts += 3 + g.random.Int64N(5)
		}
		key.Entries = &keyEntries{DistinctAccounts: g.windows(accounts, accounts, accounts+g.random.Int64N(2)), AsOf: g.dictAsOf(t)}
	}

	return key
}

func (g *generator) keyType(legal bool) string {
	switch {
	case g.chance(shareUnrecognizedKey):
		return "NEW_KEY_TYPE"
	case legal && g.chance(0.6):
		return "CNPJ"
	}

	switch r := g.random.Float64(); {
	case r < 0.35:
		return "CPF"
	case r < 0.60:
		return "PHONE"
	case r < 0.75:
		return "EMAIL"
	case r < 0.95:
		return "EVP"
	default:
		return "CNPJ"
	}
}

// dictGroup reports whether a DICT group is present: no group is during an
// outage, and each is missing on its own otherwise.
func (g *generator) dictGroup(outage bool) bool {
	return !outage && !g.chance(shareMissingGroup)
}

func (g *generator) dictAsOf(t times) string {
	if g.chance(shareStaleOptional) {
		return t.stale24h()
	}
	return t.dict()
}

// windows returns a window counter, with one window missing now and then.
func (g *generator) windows(d90, m12, m60 int64) window {
	w := window{D90: &d90, M12: &m12, M60: &m60}
	if g.chance(shareMissingWindow) {
		switch g.random.IntN(3) {
		case 0:
			w.D90 = nil
		case 1:
			w.M12 = nil
		default:
			w.M60 = nil
		}
	}
	return w
}

// window1 returns a single count, missing now and then.
func (g *generator) window1(value int64) *int64 {
	if g.chance(shareMissingWindow) {
		return nil
	}
	return &value
}

func (g *generator) spi(t times, fraud bool) *spi {
	d90 := g.count(10)
	if fraud && g.chance(0.5) {
		d90 = g.count(1)
	}
	m12 := d90 + g.count(25)
	return &spi{Settlements: g.windows(d90, m12, m12+g.count(50)), AsOf: g.dictAsOf(t)}
}

func (g *generator) markers(fraud bool) window {
	if !fraud || !g.chance(0.3) {
		return g.windows(0, 0, 0)
	}
	d90 := 1 + g.random.Int64N(3)
	return g.windows(d90, d90+g.random.Int64N(3), d90+g.random.Int64N(6))
}

func (g *generator) fraudMarkers(t times, fraud bool) *fraudMarkers {
	return &fraudMarkers{
		ApplicationFrauds:      g.windows(0, 0, 0),
		MuleAccounts:           g.markers(fraud),
		ScammerAccounts:        g.markers(fraud),
		OtherFrauds:            g.windows(0, 0, 0),
		UnknownFrauds:          g.windows(0, 0, 0),
		DistinctFraudReporters: g.markers(fraud),
		AsOf:                   g.dictAsOf(t),
	}
}

func (g *generator) infractionReports(t times, fraud bool) *infractionReports {
	open := int64(0)
	if fraud && g.chance(0.2) {
		open = 1 + g.random.Int64N(4)
	}
	return &infractionReports{
		OpenReports:                  g.window1(open),
		OpenReportsDistinctReporters: g.window1(min(open, 2)),
		RejectedReports:              g.windows(0, 0, g.random.Int64N(2)),
		AsOf:                         g.dictAsOf(t),
	}
}
