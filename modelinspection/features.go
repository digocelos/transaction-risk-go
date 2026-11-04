package modelinspection

import (
	"math"
	"slices"

	"github.com/digocelos/transaction-risk-go/pixrisk"
)

// Expected directions of the score as a feature grows, declared in
// docs/features/pix-risk-v1.md §20. They are expectations about Pix fraud,
// not properties of the model: the report measures whether a model follows
// them.
const (
	RiskIncreasing = "risk_increasing"
	RiskDecreasing = "risk_decreasing"
	NoDirection    = "none"
)

// Units of the partial dependence grids: the real units of a standardized
// feature before its fixed transformation.
const (
	UnitCentavos = "centavos"
	UnitCount    = "count"
	UnitRatio    = "ratio"
	UnitDays     = "days"
)

// directions is the expected direction of every feature, by name.
var directions = map[string]string{
	"amount":                             RiskIncreasing,
	"recipient_legal_person":             NoDirection,
	"no_pix_key":                         NoDirection,
	"no_payer_device":                    NoDirection,
	"local_time_sin":                     NoDirection,
	"local_time_cos":                     NoDirection,
	"limit_period_nighttime":             NoDirection,
	"limit_period_daily":                 NoDirection,
	"limit_period_none":                  NoDirection,
	"settled_count_90d":                  RiskDecreasing,
	"initiated_count_24h":                RiskIncreasing,
	"recipient_payments_365d":            RiskDecreasing,
	"amount_to_median_90d":               RiskIncreasing,
	"amount_to_median_90d_known":         NoDirection,
	"limit_utilization":                  RiskIncreasing,
	"limit_utilization_known":            NoDirection,
	"limit_zero":                         NoDirection,
	"recipient_pre_registered":           RiskDecreasing,
	"recipient_pre_registered_known":     NoDirection,
	"device_settled_count_90d":           RiskDecreasing,
	"device_settled_count_90d_known":     NoDirection,
	"device_registered":                  RiskDecreasing,
	"device_registered_known":            NoDirection,
	"recipient_account_age":              RiskDecreasing,
	"recipient_account_age_known":        NoDirection,
	"key_link_age":                       RiskDecreasing,
	"key_ownership_age":                  RiskDecreasing,
	"key_type_cpf":                       NoDirection,
	"key_type_cnpj":                      NoDirection,
	"key_type_phone":                     NoDirection,
	"key_type_email":                     NoDirection,
	"key_type_evp":                       NoDirection,
	"key_type_other":                     NoDirection,
	"key_entry_known":                    NoDirection,
	"person_settlements_d90":             NoDirection,
	"person_settlements_d90_known":       NoDirection,
	"person_settlements_m12":             NoDirection,
	"person_settlements_m12_known":       NoDirection,
	"person_fraud_markers_d90":           RiskIncreasing,
	"person_fraud_markers_d90_known":     NoDirection,
	"person_fraud_markers_m12":           RiskIncreasing,
	"person_fraud_markers_m12_known":     NoDirection,
	"person_fraud_reporters_m12":         RiskIncreasing,
	"person_fraud_reporters_m12_known":   NoDirection,
	"person_open_report_reporters":       RiskIncreasing,
	"person_open_report_reporters_known": NoDirection,
	"person_registered_accounts":         NoDirection,
	"person_registered_accounts_known":   NoDirection,
	"key_settlements_d90":                NoDirection,
	"key_settlements_d90_known":          NoDirection,
	"key_settlements_m12":                NoDirection,
	"key_settlements_m12_known":          NoDirection,
	"key_fraud_markers_d90":              RiskIncreasing,
	"key_fraud_markers_d90_known":        NoDirection,
	"key_fraud_markers_m12":              RiskIncreasing,
	"key_fraud_markers_m12_known":        NoDirection,
	"key_fraud_reporters_m12":            RiskIncreasing,
	"key_fraud_reporters_m12_known":      NoDirection,
	"key_open_report_reporters":          RiskIncreasing,
	"key_open_report_reporters_known":    NoDirection,
	"key_distinct_accounts_m12":          RiskIncreasing,
	"key_distinct_accounts_m12_known":    NoDirection,
}

// Direction returns the expected direction of a feature, by name, and
// whether the feature exists.
func Direction(name string) (string, bool) {
	direction, ok := directions[name]
	return direction, ok
}

// scale describes the real units of a standardized feature and its fixed
// transformation of pix-risk-v1 §9: log1p of the raw value, or its natural
// logarithm for the ratio of the amount to the median.
type scale struct {
	unit string
	ln   bool
	grid []float64
}

var (
	centavosGrid    = []float64{100, 500, 2000, 5000, 10000, 20000, 50000, 100000, 200000, 500000, 1000000, 5000000}
	countGrid       = []float64{0, 1, 2, 3, 5, 10, 20, 50, 100, 300, 1000, 10000}
	daysGrid        = []float64{0, 1, 3, 7, 14, 30, 60, 90, 180, 365, 730, 1825}
	medianRatioGrid = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5, 10, 20, 50, 100}
	utilizationGrid = []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9, 1, 1.25, 1.5, 2, 5, 10}
)

// scales gives the unit, transformation, and grid of every standardized
// feature, by name.
var scales = map[string]scale{
	"amount":                   {UnitCentavos, false, centavosGrid},
	"settled_count_90d":        {UnitCount, false, countGrid},
	"initiated_count_24h":      {UnitCount, false, countGrid},
	"recipient_payments_365d":  {UnitCount, false, countGrid},
	"amount_to_median_90d":     {UnitRatio, true, medianRatioGrid},
	"limit_utilization":        {UnitRatio, false, utilizationGrid},
	"device_settled_count_90d": {UnitCount, false, countGrid},
	"recipient_account_age":    {UnitDays, false, daysGrid},
	"key_link_age":             {UnitDays, false, daysGrid},
	"key_ownership_age":        {UnitDays, false, daysGrid},
}

func scaleOf(name string) scale {
	if s, ok := scales[name]; ok {
		return s
	}
	// Every other standardized feature is a log1p count: DICT settlements,
	// markers, reporters, and accounts.
	return scale{UnitCount, false, countGrid}
}

// transform applies the fixed transformation to a raw value.
func (s scale) transform(raw float64) float64 {
	if s.ln {
		return math.Log(raw)
	}
	return math.Log1p(raw)
}

// raw inverts the fixed transformation, rounded to six decimals so that
// integers come back as integers.
func (s scale) raw(value float64) float64 {
	raw := math.Expm1(value)
	if s.ln {
		raw = math.Exp(value)
	}
	return math.Round(raw*1e6) / 1e6
}

// sourceFeatures lists the features each optional source feeds, by index,
// as pix-risk-v1 §9 assigns them. Masking a source sets them all to 0,
// which is exactly how an unavailable source is represented.
var sourceFeatures = map[string][]int{
	pixrisk.SourcePeriodLimit:              {14, 15, 16},
	pixrisk.SourcePayerDeviceHistory:       {19, 20},
	pixrisk.SourcePayerDeviceRegistration:  {21, 22},
	pixrisk.SourceRecipientPreRegistration: {17, 18},
	pixrisk.SourceRecipientAccountEntry:    {23, 24},
	pixrisk.SourceRecipientPixKeyEntry:     span(25, 33),
	pixrisk.SourcePersonSpi:                span(34, 37),
	pixrisk.SourcePersonFraudMarkers:       span(38, 43),
	pixrisk.SourcePersonInfractionReports:  {44, 45},
	pixrisk.SourcePersonEntries:            {46, 47},
	pixrisk.SourceKeySpi:                   span(48, 51),
	pixrisk.SourceKeyFraudMarkers:          span(52, 57),
	pixrisk.SourceKeyInfractionReports:     {58, 59},
	pixrisk.SourceKeyEntries:               {60, 61},
}

// Indices of the discriminators a population sets.
const (
	indexNoPixKey      = 2
	indexNoPayerDevice = 3
)

// Names of the probes beyond the single sources.
const (
	ProbeDictStatistics = "dict_statistics"
)

// probe is one masking experiment: the indicators it sets to 1 and the
// features it sets to 0.
type probe struct {
	name   string
	kind   string
	set    []int
	masked []int
}

// probes returns every probe, in report order: each optional source, every
// DICT statistics group together, and the populations without a Pix key
// and without a payer device.
func probes() []probe {
	var list []probe
	for _, name := range pixrisk.Sources() {
		list = append(list, probe{name: name, kind: ProbeSource, masked: slices.Clone(sourceFeatures[name])})
	}

	var dict []int
	for _, name := range []string{
		pixrisk.SourcePersonSpi, pixrisk.SourcePersonFraudMarkers, pixrisk.SourcePersonInfractionReports, pixrisk.SourcePersonEntries,
		pixrisk.SourceKeySpi, pixrisk.SourceKeyFraudMarkers, pixrisk.SourceKeyInfractionReports, pixrisk.SourceKeyEntries,
	} {
		dict = append(dict, sourceFeatures[name]...)
	}
	list = append(list, probe{name: ProbeDictStatistics, kind: ProbeSources, masked: dict})

	var keyless []int
	for _, name := range []string{pixrisk.SourceRecipientPixKeyEntry, pixrisk.SourceKeySpi, pixrisk.SourceKeyFraudMarkers, pixrisk.SourceKeyInfractionReports, pixrisk.SourceKeyEntries} {
		keyless = append(keyless, sourceFeatures[name]...)
	}
	list = append(list, probe{name: pixrisk.PopulationNoPixKey, kind: ProbePopulation, set: []int{indexNoPixKey}, masked: keyless})

	var deviceless []int
	for _, name := range []string{pixrisk.SourcePayerDeviceHistory, pixrisk.SourcePayerDeviceRegistration} {
		deviceless = append(deviceless, sourceFeatures[name]...)
	}
	list = append(list, probe{name: pixrisk.PopulationNoPayerDevice, kind: ProbePopulation, set: []int{indexNoPayerDevice}, masked: deviceless})

	return list
}

// apply returns a copy of values with the probe applied.
func (p probe) apply(values []float64) []float64 {
	applied := slices.Clone(values)
	for _, i := range p.set {
		applied[i] = 1
	}
	for _, i := range p.masked {
		applied[i] = 0
	}
	return applied
}

func span(from, to int) []int {
	indices := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		indices = append(indices, i)
	}
	return indices
}
