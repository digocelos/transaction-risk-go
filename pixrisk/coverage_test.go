package pixrisk

import (
	"github.com/digocelos/transaction-risk-go/pixinput"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func preparedExample(t *testing.T, name string) Prepared {
	t.Helper()
	return prepare(t, readFile(t, filepath.Join(examplesDir, name)))
}

func TestSourceStatesOfTheExamples(t *testing.T) {
	allKnown := map[string]string{}
	for _, source := range Sources() {
		allKnown[source] = StateKnown
	}

	with := func(changes map[string]string) map[string]string {
		states := map[string]string{}
		for source, state := range allKnown {
			states[source] = state
		}
		for source, state := range changes {
			states[source] = state
		}
		return states
	}

	notApplicableKey := map[string]string{
		SourceRecipientPixKeyEntry: StatusNotApplicable, SourceKeySpi: StatusNotApplicable,
		SourceKeyFraudMarkers: StatusNotApplicable, SourceKeyInfractionReports: StatusNotApplicable,
		SourceKeyEntries: StatusNotApplicable, SourceRecipientAccountEntry: StatusMissing,
	}

	tests := map[string]map[string]string{
		"01-complete.json":                 allKnown,
		"02-partial-dict.json":             with(map[string]string{SourceKeyFraudMarkers: StatusMissing}),
		"04-no-pix-key.json":               with(notApplicableKey),
		"05-no-payer-device.json":          with(map[string]string{SourcePayerDeviceHistory: StatusNotApplicable, SourcePayerDeviceRegistration: StatusNotApplicable}),
		"06-optional-stale.json":           with(map[string]string{SourcePeriodLimit: StatusStale, SourcePayerDeviceRegistration: StatusStale}),
		"07-zero-limit.json":               with(map[string]string{SourcePeriodLimit: StatusZeroLimit}),
		"08-no-period-limit.json":          with(map[string]string{SourcePeriodLimit: StatusNotApplicable}),
		"03-new-account.json":              allKnown,
		"12-pre-registered-recipient.json": allKnown,
	}

	for name, want := range tests {
		if got := preparedExample(t, name).SourceStates(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: states = %v\nwant           %v", name, got, want)
		}
	}
}

func TestPopulationsAndLimitPeriodOfTheExamples(t *testing.T) {
	tests := map[string]struct {
		populations []string
		period      string
	}{
		"01-complete.json":        {[]string{}, "daytime"},
		"03-new-account.json":     {[]string{PopulationNewPayerAccount}, "daytime"},
		"04-no-pix-key.json":      {[]string{PopulationNoPixKey, PopulationNoRecipientAccountEntry}, "daytime"},
		"05-no-payer-device.json": {[]string{PopulationNoPayerDevice}, "daytime"},
		"08-no-period-limit.json": {[]string{PopulationNoPeriodLimit}, "no_period_limit"},
	}

	for name, test := range tests {
		prepared := preparedExample(t, name)
		if got := prepared.Populations(); !reflect.DeepEqual(got, test.populations) {
			t.Errorf("%s: populations = %v, want %v", name, got, test.populations)
		}
		if got := prepared.LimitPeriod(); got != test.period {
			t.Errorf("%s: limit period = %q, want %q", name, got, test.period)
		}
	}

	nighttime := prepare(t, edit(t, map[string]any{"pix_payment/payer_limit_period": "nighttime"}))
	daily := prepare(t, edit(t, map[string]any{"pix_payment/payer_limit_period": "daily"}))
	if nighttime.LimitPeriod() != "nighttime" || daily.LimitPeriod() != "daily" {
		t.Errorf("limit periods = %q, %q", nighttime.LimitPeriod(), daily.LimitPeriod())
	}
}

func TestPossibleStates(t *testing.T) {
	want := map[string][]string{
		SourcePeriodLimit:              {StateKnown, StatusMissing, StatusStale, StatusNotApplicable, StatusZeroLimit},
		SourceRecipientPreRegistration: {StateKnown, StatusMissing, StatusStale},
		SourceKeySpi:                   {StateKnown, StatusMissing, StatusStale, StatusNotApplicable},
	}

	for source, states := range want {
		if got := PossibleStates(source); !reflect.DeepEqual(got, states) {
			t.Errorf("%s: possible states = %v, want %v", source, got, states)
		}
	}

	if len(Sources()) != 14 {
		t.Errorf("Sources() has %d sources, want 14", len(Sources()))
	}
}

// Masking the features of a source sets exactly the features that
// preparation sets to 0 when the source is missing from the scoring input.
func TestSourceFeaturesMatchPreparation(t *testing.T) {
	decodeComplete := func() pixinput.ScoringInput {
		data, err := os.ReadFile("../docs/contracts/examples-v3/01-complete.json")
		if err != nil {
			t.Fatal(err)
		}
		input, err := pixinput.Decode(data, pixinput.ContractV3)
		if err != nil {
			t.Fatal(err)
		}
		return input
	}
	base, err := Prepare(decodeComplete())
	if err != nil {
		t.Fatal(err)
	}

	remove := map[string]func(*pixinput.ScoringInput){
		SourcePeriodLimit:              func(in *pixinput.ScoringInput) { in.Observations.PayerAccount.PeriodLimit = nil },
		SourcePayerDeviceHistory:       func(in *pixinput.ScoringInput) { in.Observations.PayerDevice.SettledOutgoingPix90d = nil },
		SourcePayerDeviceRegistration:  func(in *pixinput.ScoringInput) { in.Observations.PayerDevice.Registration = nil },
		SourceRecipientPreRegistration: func(in *pixinput.ScoringInput) { in.Observations.RecipientAccount.PreRegistration = nil },
		SourceRecipientAccountEntry:    func(in *pixinput.ScoringInput) { in.Observations.RecipientAccount.Entry = nil },
		SourceRecipientPixKeyEntry:     func(in *pixinput.ScoringInput) { in.Observations.RecipientPixKey.Entry = nil },
		SourcePersonSpi:                func(in *pixinput.ScoringInput) { in.Observations.RecipientPerson.Spi = nil },
		SourcePersonFraudMarkers:       func(in *pixinput.ScoringInput) { in.Observations.RecipientPerson.FraudMarkers = nil },
		SourcePersonInfractionReports:  func(in *pixinput.ScoringInput) { in.Observations.RecipientPerson.InfractionReports = nil },
		SourcePersonEntries:            func(in *pixinput.ScoringInput) { in.Observations.RecipientPerson.Entries = nil },
		SourceKeySpi:                   func(in *pixinput.ScoringInput) { in.Observations.RecipientPixKey.Spi = nil },
		SourceKeyFraudMarkers:          func(in *pixinput.ScoringInput) { in.Observations.RecipientPixKey.FraudMarkers = nil },
		SourceKeyInfractionReports:     func(in *pixinput.ScoringInput) { in.Observations.RecipientPixKey.InfractionReports = nil },
		SourceKeyEntries:               func(in *pixinput.ScoringInput) { in.Observations.RecipientPixKey.Entries = nil },
	}

	covered := map[int]bool{}
	for _, name := range Sources() {
		input := decodeComplete()
		remove[name](&input)
		without, err := Prepare(input)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		masked := SourceFeatures(name)
		if len(masked) == 0 {
			t.Fatalf("%s: no features", name)
		}
		expected := base.Values
		for _, i := range masked {
			expected[i] = 0
			covered[i] = true
		}
		if without.Values != expected {
			t.Errorf("%s: preparation without the source gives %v, masking its features gives %v", name, without.Values, expected)
		}
	}

	// The payment, context, and required payer history features (0 to 13,
	// and 62 to 64) come from no optional source; every other feature comes
	// from exactly one.
	counts := map[int]int{}
	for _, name := range Sources() {
		for _, i := range SourceFeatures(name) {
			counts[i]++
		}
	}
	for i, feature := range Descriptor() {
		if want := i >= iLimitUtilization && i < iInitiatedAmount24h; covered[i] != want || counts[i] > 1 {
			t.Errorf("feature %d %s: fed by %d sources, want optional %v", i, feature.Name, counts[i], want)
		}
	}
	if SourceFeatures("other") != nil {
		t.Error("an unknown source has features")
	}
}
