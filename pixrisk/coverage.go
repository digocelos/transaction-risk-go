package pixrisk

import "slices"

// StateKnown is the availability state of an optional source that is
// present and fresh. The other states are the note statuses.
const StateKnown = "known"

// Optional sources of pix-risk-v1, as the training report names them.
const (
	SourcePeriodLimit              = "period_limit"
	SourcePayerDeviceHistory       = "payer_device_history"
	SourcePayerDeviceRegistration  = "payer_device_registration"
	SourceRecipientPreRegistration = "recipient_pre_registration"
	SourceRecipientAccountEntry    = "recipient_account_entry"
	SourceRecipientPixKeyEntry     = "recipient_pix_key_entry"
	SourcePersonSpi                = "person_spi"
	SourcePersonFraudMarkers       = "person_fraud_markers"
	SourcePersonInfractionReports  = "person_infraction_reports"
	SourcePersonEntries            = "person_entries"
	SourceKeySpi                   = "key_spi"
	SourceKeyFraudMarkers          = "key_fraud_markers"
	SourceKeyInfractionReports     = "key_infraction_reports"
	SourceKeyEntries               = "key_entries"
)

// Supported populations of pix-risk-v1.
const (
	PopulationNewPayerAccount         = "new_payer_account"
	PopulationNoPayerDevice           = "no_payer_device"
	PopulationNoPeriodLimit           = "no_period_limit"
	PopulationNoPixKey                = "no_pix_key"
	PopulationNoRecipientAccountEntry = "no_recipient_account_entry"
)

// source locates an optional source in a scoring input: its path, and the
// discriminator that can make it not applicable.
type source struct {
	name          string
	path          string
	discriminator string
}

var sources = []source{
	{SourcePeriodLimit, "/observations/payer_account/period_limit", "/pix_payment/payer_limit_period"},
	{SourcePayerDeviceHistory, "/observations/payer_device/settled_outgoing_pix_90d", "/pix_payment/payer_device_usage"},
	{SourcePayerDeviceRegistration, "/observations/payer_device/registration", "/pix_payment/payer_device_usage"},
	{SourceRecipientPreRegistration, "/observations/recipient_account/pre_registration", ""},
	{SourceRecipientAccountEntry, "/observations/recipient_account/entry", ""},
	{SourceRecipientPixKeyEntry, "/observations/recipient_pix_key/entry", "/pix_payment/recipient_key_usage"},
	{SourcePersonSpi, "/observations/recipient_person/spi", ""},
	{SourcePersonFraudMarkers, "/observations/recipient_person/fraud_markers", ""},
	{SourcePersonInfractionReports, "/observations/recipient_person/infraction_reports", ""},
	{SourcePersonEntries, "/observations/recipient_person/entries", ""},
	{SourceKeySpi, "/observations/recipient_pix_key/spi", "/pix_payment/recipient_key_usage"},
	{SourceKeyFraudMarkers, "/observations/recipient_pix_key/fraud_markers", "/pix_payment/recipient_key_usage"},
	{SourceKeyInfractionReports, "/observations/recipient_pix_key/infraction_reports", "/pix_payment/recipient_key_usage"},
	{SourceKeyEntries, "/observations/recipient_pix_key/entries", "/pix_payment/recipient_key_usage"},
}

// Sources returns the names of the optional sources, in report order.
func Sources() []string {
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = s.name
	}
	return names
}

// PossibleStates returns the availability states a source can be in. A
// member or window missing inside a known group leaves the group known;
// the features it feeds report it.
func PossibleStates(name string) []string {
	for _, s := range sources {
		if s.name != name {
			continue
		}

		states := []string{StateKnown, StatusMissing, StatusStale}
		if s.discriminator != "" {
			states = append(states, StatusNotApplicable)
		}
		if name == SourcePeriodLimit {
			states = append(states, StatusZeroLimit)
		}
		return states
	}
	return nil
}

// SourceStates returns the availability state of every optional source of
// a prepared input.
func (p Prepared) SourceStates() map[string]string {
	states := make(map[string]string, len(sources))

	for _, s := range sources {
		state := StateKnown

		switch {
		case s.discriminator != "" && slices.Contains(p.Notes, Note{Path: s.discriminator, Status: StatusNotApplicable}):
			state = StatusNotApplicable
		case slices.Contains(p.Notes, Note{Path: s.path, Status: StatusMissing}):
			state = StatusMissing
		case slices.Contains(p.Notes, Note{Path: s.path + "/as_of", Status: StatusStale}):
			state = StatusStale
		case s.name == SourcePeriodLimit && p.Values[iLimitZero] == 1:
			state = StatusZeroLimit
		}

		states[s.name] = state
	}

	return states
}

// Populations returns the supported populations a prepared input belongs
// to, sorted by name.
func (p Prepared) Populations() []string {
	populations := []string{}

	if p.Values[iAmountToMedian90dKnown] == 0 {
		populations = append(populations, PopulationNewPayerAccount)
	}
	if p.Values[iNoPayerDevice] == 1 {
		populations = append(populations, PopulationNoPayerDevice)
	}
	if p.Values[iLimitPeriodNone] == 1 {
		populations = append(populations, PopulationNoPeriodLimit)
	}
	if p.Values[iNoPixKey] == 1 {
		populations = append(populations, PopulationNoPixKey)
	}
	if p.Values[iRecipientAccountAgeKnown] == 0 {
		populations = append(populations, PopulationNoRecipientAccountEntry)
	}

	return populations
}

// Populations returns every supported population, sorted by name.
func Populations() []string {
	return []string{
		PopulationNewPayerAccount, PopulationNoPayerDevice, PopulationNoPeriodLimit,
		PopulationNoPixKey, PopulationNoRecipientAccountEntry,
	}
}

// LimitPeriods returns the payer limit periods, in report order.
func LimitPeriods() []string {
	return []string{"daytime", "nighttime", "daily", "no_period_limit"}
}

// LimitPeriod returns the payer limit period of a prepared input.
func (p Prepared) LimitPeriod() string {
	switch {
	case p.Values[iLimitPeriodNighttime] == 1:
		return "nighttime"
	case p.Values[iLimitPeriodDaily] == 1:
		return "daily"
	case p.Values[iLimitPeriodNone] == 1:
		return "no_period_limit"
	}
	return "daytime"
}
