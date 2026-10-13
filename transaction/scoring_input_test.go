package transaction

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/pixinput"
)

const examplesDir = "../docs/contracts/examples"

// expectedResult is an example's expected cmd/predict output.
type expectedResult struct {
	Status   string    `json:"status"`
	Score    float64   `json:"score"`
	Blockers []Blocker `json:"blockers"`
}

func decodeExample(t *testing.T, name string) (pixinput.ScoringInput, expectedResult) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(examplesDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}

	input, err := pixinput.Decode(data)
	if err != nil {
		t.Fatalf("Decode(%s) error = %v", name, err)
	}

	data, err = os.ReadFile(filepath.Join(examplesDir, name+".expected.json"))
	if err != nil {
		t.Fatal(err)
	}

	var expected expectedResult
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}

	return input, expected
}

func blockersOf(t *testing.T, err error) []Blocker {
	t.Helper()

	var insufficient *InsufficientInputError
	if !errors.As(err, &insufficient) {
		t.Fatalf("error = %v, want *InsufficientInputError", err)
	}

	return insufficient.Blockers
}

func TestFromScoringInputMatchesSpecificationExamples(t *testing.T) {
	model, err := nn.Load("../model.json", FeatureSchema)
	if err != nil {
		t.Fatal(err)
	}

	// Legacy values with the same meaning as each scored example, taken from
	// the README and the demo's evaluation examples.
	legacy := map[string]Transaction{
		"01-complete":            {AmountCents: 250000, TypicalAmountCents: 25000, RecentCount: 2, NewRecipient: true},
		"02-dict-group-missing":  {AmountCents: 250000, TypicalAmountCents: 25000, RecentCount: 2, NewRecipient: true},
		"03-dict-member-missing": {AmountCents: 250000, TypicalAmountCents: 25000, RecentCount: 2, NewRecipient: true},
		"04-dict-window-missing": {AmountCents: 250000, TypicalAmountCents: 25000, RecentCount: 2, NewRecipient: true},
		"05-known-zero":          {AmountCents: 180000, TypicalAmountCents: 20000, RecentCount: 9, NewRecipient: true, NewDevice: true},
		"08-no-pix-key":          {AmountCents: 18000, TypicalAmountCents: 20000, RecentCount: 2},
	}

	names := []string{
		"01-complete", "02-dict-group-missing", "03-dict-member-missing",
		"04-dict-window-missing", "05-known-zero", "06-integrator-observation-missing",
		"07-median-semantic-null", "08-no-pix-key", "09-no-payer-device",
		"10-multiple-blockers",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			input, expected := decodeExample(t, name)

			features, err := FromScoringInput(input)

			if expected.Status == "insufficient_input" {
				if got := blockersOf(t, err); !reflect.DeepEqual(got, expected.Blockers) {
					t.Errorf("blockers = %v, want %v", got, expected.Blockers)
				}
				if features != ([FeatureCount]float64{}) {
					t.Errorf("features = %v, want none for insufficient input", features)
				}
				return
			}

			if err != nil {
				t.Fatalf("FromScoringInput() error = %v", err)
			}

			want, err := Features(legacy[name])
			if err != nil {
				t.Fatal(err)
			}

			if features != want {
				t.Errorf("features = %v, want the legacy features %v bit for bit", features, want)
			}

			outputs, err := model.Predict(features[:])
			if err != nil {
				t.Fatal(err)
			}

			if outputs[0] != expected.Score {
				t.Errorf("score = %v, want %v", outputs[0], expected.Score)
			}
		})
	}
}

func TestFromScoringInputFreshnessIsInclusive(t *testing.T) {
	tests := []struct {
		name   string
		maxAge time.Duration
		asOf   func(*pixinput.ScoringInput) *time.Time
		path   string
	}{
		{"initiated 24h count", 5 * time.Minute, func(in *pixinput.ScoringInput) *time.Time {
			return &in.Observations.PayerAccount.InitiatedOutgoingPix24h.AsOf
		}, "/observations/payer_account/initiated_outgoing_pix_24h/as_of"},
		{"settled 90d history", 24 * time.Hour, func(in *pixinput.ScoringInput) *time.Time {
			return &in.Observations.PayerAccount.SettledOutgoingPix90d.AsOf
		}, "/observations/payer_account/settled_outgoing_pix_90d/as_of"},
		{"recipient 365d relationship", 24 * time.Hour, func(in *pixinput.ScoringInput) *time.Time {
			return &in.Observations.RecipientPerson.SettledPixFromPayerAccount365d.AsOf
		}, "/observations/recipient_person/settled_pix_from_payer_account_365d/as_of"},
		{"payer device activity", 24 * time.Hour, func(in *pixinput.ScoringInput) *time.Time {
			return &in.Observations.PayerDevice.SettledOutgoingPix90d.AsOf
		}, "/observations/payer_device/settled_outgoing_pix_90d/as_of"},
	}

	for _, test := range tests {
		t.Run(test.name+" at maximum age", func(t *testing.T) {
			input, _ := decodeExample(t, "01-complete")
			*test.asOf(&input) = input.DecisionTime.Add(-test.maxAge)

			if _, err := FromScoringInput(input); err != nil {
				t.Errorf("FromScoringInput() error = %v, want fresh", err)
			}
		})

		t.Run(test.name+" one nanosecond older", func(t *testing.T) {
			input, _ := decodeExample(t, "01-complete")
			*test.asOf(&input) = input.DecisionTime.Add(-test.maxAge - time.Nanosecond)

			_, err := FromScoringInput(input)

			want := []Blocker{{Path: test.path, Reason: "stale"}}
			if got := blockersOf(t, err); !reflect.DeepEqual(got, want) {
				t.Errorf("blockers = %v, want %v", got, want)
			}
		})
	}
}

func TestFromScoringInputReportsEveryBlocker(t *testing.T) {
	input, _ := decodeExample(t, "01-complete")

	account := &input.Observations.PayerAccount
	account.SettledOutgoingPix90d.Count = 0
	account.SettledOutgoingPix90d.MedianAmountCents = nil
	account.SettledOutgoingPix90d.AsOf = input.DecisionTime.Add(-25 * time.Hour)
	account.InitiatedOutgoingPix24h = nil
	input.Observations.RecipientPerson.SettledPixFromPayerAccount365d.AsOf =
		input.DecisionTime.Add(-48 * time.Hour)
	input.PixPayment.PayerDeviceUsage = pixinput.NoPayerDevice
	input.Observations.PayerDevice = nil

	features, err := FromScoringInput(input)

	want := []Blocker{
		{Path: "/observations/payer_account/initiated_outgoing_pix_24h", Reason: "missing"},
		{Path: "/observations/payer_account/settled_outgoing_pix_90d/as_of", Reason: "stale"},
		{Path: "/observations/payer_account/settled_outgoing_pix_90d/median_amount_cents", Reason: "no_history"},
		{Path: "/observations/recipient_person/settled_pix_from_payer_account_365d/as_of", Reason: "stale"},
		{Path: "/pix_payment/payer_device_usage", Reason: "not_applicable"},
	}

	if got := blockersOf(t, err); !reflect.DeepEqual(got, want) {
		t.Errorf("blockers = %v\nwant       %v", got, want)
	}

	if features != ([FeatureCount]float64{}) {
		t.Errorf("features = %v, want no partial features", features)
	}
}

func TestFromScoringInputIgnoresDICTObservations(t *testing.T) {
	input, _ := decodeExample(t, "01-complete")

	key := input.Observations.RecipientPixKey
	key.Entry, key.Spi, key.FraudMarkers, key.InfractionReports, key.Entries = nil, nil, nil, nil, nil
	person := &input.Observations.RecipientPerson
	person.Spi, person.FraudMarkers, person.InfractionReports, person.Entries = nil, nil, nil, nil
	input.Observations.RecipientAccount.Entry = nil

	if _, err := FromScoringInput(input); err != nil {
		t.Errorf("FromScoringInput() error = %v, want DICT observations never to block", err)
	}
}
