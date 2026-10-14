// Package pixrisk prepares the features of the feature schema pix-risk-v1
// from pix-payer-input-v2 scoring inputs, as specified in
// docs/features/pix-risk-v1.md. Training, evaluation, and scoring share
// this preparation: Prepare turns one input into values before
// standardization, Fit learns the standardization from the train partition
// only, and State.Apply produces the values a network receives.
package pixrisk

// Schema is the feature schema this package prepares.
const Schema = "pix-risk-v1"

// FeatureCount is the number of features of pix-risk-v1.
const FeatureCount = 62

// Kind tells how a feature is transformed.
type Kind string

const (
	// Standardized features are standardized with the fitted state.
	Standardized Kind = "standardized"
	// Cyclic features range from −1 to 1 and are not standardized.
	Cyclic Kind = "cyclic"
	// Indicator features are 0 or 1 and are not standardized.
	Indicator Kind = "indicator"
)

// Always marks a feature that is always available.
const Always = -1

// Feature describes one feature. Indicator is the index of the
// availability indicator the feature depends on, or Always.
type Feature struct {
	Name      string
	Kind      Kind
	Indicator int
}

var descriptor = func() []Feature {
	features := []Feature{
		{"amount", Standardized, Always},
		{"recipient_legal_person", Indicator, Always},
		{"no_pix_key", Indicator, Always},
		{"no_payer_device", Indicator, Always},
		{"local_time_sin", Cyclic, Always},
		{"local_time_cos", Cyclic, Always},
		{"limit_period_nighttime", Indicator, Always},
		{"limit_period_daily", Indicator, Always},
		{"limit_period_none", Indicator, Always},
		{"settled_count_90d", Standardized, Always},
		{"initiated_count_24h", Standardized, Always},
		{"recipient_payments_365d", Standardized, Always},
		{"amount_to_median_90d", Standardized, 13},
		{"amount_to_median_90d_known", Indicator, Always},
		{"limit_utilization", Standardized, 15},
		{"limit_utilization_known", Indicator, Always},
		{"limit_zero", Indicator, Always},
		{"recipient_pre_registered", Indicator, 18},
		{"recipient_pre_registered_known", Indicator, Always},
		{"device_settled_count_90d", Standardized, 20},
		{"device_settled_count_90d_known", Indicator, Always},
		{"device_registered", Indicator, 22},
		{"device_registered_known", Indicator, Always},
		{"recipient_account_age", Standardized, 24},
		{"recipient_account_age_known", Indicator, Always},
		{"key_link_age", Standardized, 33},
		{"key_ownership_age", Standardized, 33},
		{"key_type_cpf", Indicator, 33},
		{"key_type_cnpj", Indicator, 33},
		{"key_type_phone", Indicator, 33},
		{"key_type_email", Indicator, 33},
		{"key_type_evp", Indicator, 33},
		{"key_type_other", Indicator, 33},
		{"key_entry_known", Indicator, Always},
	}

	for _, subject := range []string{"person", "key"} {
		last := "registered_accounts"
		if subject == "key" {
			last = "distinct_accounts_m12"
		}

		for _, name := range []string{
			"settlements_d90", "settlements_m12", "fraud_markers_d90", "fraud_markers_m12",
			"fraud_reporters_m12", "open_report_reporters", last,
		} {
			features = append(features,
				Feature{subject + "_" + name, Standardized, len(features) + 1},
				Feature{subject + "_" + name + "_known", Indicator, Always},
			)
		}
	}

	return features
}()

// Descriptor returns the features of pix-risk-v1, in order.
func Descriptor() []Feature {
	return append([]Feature(nil), descriptor...)
}

// Feature indices used by preparation.
const (
	iAmount = iota
	iRecipientLegalPerson
	iNoPixKey
	iNoPayerDevice
	iLocalTimeSin
	iLocalTimeCos
	iLimitPeriodNighttime
	iLimitPeriodDaily
	iLimitPeriodNone
	iSettledCount90d
	iInitiatedCount24h
	iRecipientPayments365d
	iAmountToMedian90d
	iAmountToMedian90dKnown
	iLimitUtilization
	iLimitUtilizationKnown
	iLimitZero
	iRecipientPreRegistered
	iRecipientPreRegisteredKnown
	iDeviceSettledCount90d
	iDeviceSettledCount90dKnown
	iDeviceRegistered
	iDeviceRegisteredKnown
	iRecipientAccountAge
	iRecipientAccountAgeKnown
	iKeyLinkAge
	iKeyOwnershipAge
	iKeyTypeCPF
	iKeyTypeCNPJ
	iKeyTypePhone
	iKeyTypeEmail
	iKeyTypeEVP
	iKeyTypeOther
	iKeyEntryKnown
	iPersonStatistics
)

// iKeyStatistics is the first DICT statistics feature of the Pix key; each
// subject has 7 features, each followed by its indicator.
const iKeyStatistics = iPersonStatistics + 14

// Offsets of the DICT statistics features within a subject.
const (
	oSettlementsD90 = 2 * iota
	oSettlementsM12
	oFraudMarkersD90
	oFraudMarkersM12
	oFraudReportersM12
	oOpenReportReporters
	oAccounts
)
