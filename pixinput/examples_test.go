package pixinput

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// expectedResult is the part of an example's expected cmd/predict output
// that validation decides.
type expectedResult struct {
	Status string `json:"status"`
	Issues []struct {
		Path string `json:"path"`
		Code string `json:"code"`
	} `json:"issues"`
}

type pathCode struct {
	Path string
	Code string
}

func issuesOf(t *testing.T, err error) []pathCode {
	t.Helper()

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}

	got := []pathCode{}
	for _, issue := range validationErr.Issues {
		got = append(got, pathCode{issue.Path, issue.Code})
		if issue.Message == "" {
			t.Errorf("issue %s %s has no message", issue.Path, issue.Code)
		}
	}

	return got
}

func TestDecodeSpecificationExamples(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join(examplesDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}

	count := 0

	for _, input := range inputs {
		if strings.HasSuffix(input, ".expected.json") {
			continue
		}
		count++

		name := filepath.Base(input)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(strings.TrimSuffix(input, ".json") + ".expected.json")
			if err != nil {
				t.Fatal(err)
			}

			var expected expectedResult
			if err := json.Unmarshal(data, &expected); err != nil {
				t.Fatal(err)
			}

			_, err = Decode(readExample(t, name))

			if expected.Status != "invalid_input" {
				if err != nil {
					t.Fatalf("Decode() error = %v, want a valid scoring input", err)
				}
				return
			}

			want := []pathCode{}
			for _, issue := range expected.Issues {
				want = append(want, pathCode{issue.Path, issue.Code})
			}

			if got := issuesOf(t, err); !reflect.DeepEqual(got, want) {
				t.Errorf("issues = %v, want %v", got, want)
			}
		})
	}

	if count != 17 {
		t.Errorf("found %d examples, want 17", count)
	}
}

func TestDecodeKeepsMissingAndNotApplicableApart(t *testing.T) {
	decode := func(name string) ScoringInput {
		t.Helper()

		input, err := Decode(readExample(t, name))
		if err != nil {
			t.Fatalf("Decode(%s) error = %v", name, err)
		}

		return input
	}

	if key := decode("02-dict-group-missing.json").Observations.RecipientPixKey; key == nil ||
		key.FraudMarkers != nil || key.Spi == nil {
		t.Errorf("02: want only the fraud_markers group missing, got %+v", key)
	}

	if markers := decode("03-dict-member-missing.json").Observations.RecipientPixKey.FraudMarkers; markers == nil ||
		markers.MuleAccounts != nil || markers.ScammerAccounts == nil {
		t.Errorf("03: want only mule_accounts missing, got %+v", markers)
	}

	if mule := decode("04-dict-window-missing.json").Observations.RecipientPixKey.FraudMarkers.MuleAccounts; mule == nil ||
		mule.D90 != nil || mule.M12 == nil || *mule.M12 != 0 {
		t.Errorf("04: want only d90 missing, got %+v", mule)
	}

	if device := decode("05-known-zero.json").Observations.PayerDevice; device == nil ||
		device.SettledOutgoingPix90d == nil || device.SettledOutgoingPix90d.Count != 0 {
		t.Errorf("05: want a known zero device count, got %+v", device)
	}

	if device := decode("06-integrator-observation-missing.json").Observations.PayerDevice; device == nil ||
		device.SettledOutgoingPix90d != nil {
		t.Errorf("06: want the device subject present with a missing observation, got %+v", device)
	}

	if settled := decode("07-median-semantic-null.json").Observations.PayerAccount.SettledOutgoingPix90d; settled == nil ||
		settled.Count != 0 || settled.MedianAmountCents != nil {
		t.Errorf("07: want a known, empty history, got %+v", settled)
	}

	noKey := decode("08-no-pix-key.json")
	if noKey.PixPayment.RecipientKeyUsage != NoPixKey || noKey.Observations.RecipientPixKey != nil ||
		noKey.Observations.RecipientAccount.Entry != nil {
		t.Errorf("08: want no key subject and a missing account entry, got %+v", noKey.Observations)
	}

	noDevice := decode("09-no-payer-device.json")
	if noDevice.PixPayment.PayerDeviceUsage != NoPayerDevice || noDevice.Observations.PayerDevice != nil {
		t.Errorf("09: want no payer device subject, got %+v", noDevice.Observations.PayerDevice)
	}
}
