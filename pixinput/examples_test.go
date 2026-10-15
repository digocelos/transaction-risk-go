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

// expectedResult is the part of an example's expected preparation result
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

			_, err = Decode(readExample(t, name), ContractV2)

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

	if count != 20 {
		t.Errorf("found %d examples, want 20", count)
	}
}

func TestDecodeKeepsMissingAndNotApplicableApart(t *testing.T) {
	decode := func(name string, data []byte) ScoringInput {
		t.Helper()

		input, err := Decode(data, ContractV2)
		if err != nil {
			t.Fatalf("Decode(%s) error = %v", name, err)
		}

		return input
	}
	const key = "observations/recipient_pix_key"

	partial := decode("02", readExample(t, "02-partial-dict.json"))
	if markers := partial.Observations.RecipientPixKey; markers == nil || markers.FraudMarkers != nil || markers.Spi == nil {
		t.Errorf("02: want only the key's fraud_markers group missing, got %+v", markers)
	}
	if mule := partial.Observations.RecipientPerson.FraudMarkers.MuleAccounts; mule == nil ||
		mule.D90 != nil || mule.M12 == nil || *mule.M12 != 0 {
		t.Errorf("02: want only the person's mule_accounts d90 missing, got %+v", mule)
	}

	member := decode("member", edit(t, map[string]any{key + "/fraud_markers/mule_accounts": nil}))
	if markers := member.Observations.RecipientPixKey.FraudMarkers; markers == nil ||
		markers.MuleAccounts != nil || markers.ScammerAccounts == nil {
		t.Errorf("member: want only mule_accounts missing, got %+v", markers)
	}

	zero := decode("known zero", edit(t, map[string]any{"observations/payer_device/settled_outgoing_pix_90d/count": 0}))
	if device := zero.Observations.PayerDevice; device == nil ||
		device.SettledOutgoingPix90d == nil || device.SettledOutgoingPix90d.Count != 0 {
		t.Errorf("known zero: want a known zero device count, got %+v", device)
	}

	missing := decode("missing", edit(t, map[string]any{"observations/payer_device/settled_outgoing_pix_90d": nil}))
	if device := missing.Observations.PayerDevice; device == nil || device.SettledOutgoingPix90d != nil {
		t.Errorf("missing: want the device subject present with a missing observation, got %+v", device)
	}

	if settled := decode("03", readExample(t, "03-new-account.json")).Observations.PayerAccount.SettledOutgoingPix90d; settled == nil ||
		settled.Count != 0 || settled.MedianAmountCents != nil {
		t.Errorf("03: want a known, empty history, got %+v", settled)
	}

	noKey := decode("04", readExample(t, "04-no-pix-key.json"))
	if noKey.PixPayment.RecipientKeyUsage != NoPixKey || noKey.Observations.RecipientPixKey != nil ||
		noKey.Observations.RecipientAccount.Entry != nil {
		t.Errorf("04: want no key subject and a missing account entry, got %+v", noKey.Observations)
	}

	noDevice := decode("05", readExample(t, "05-no-payer-device.json"))
	if noDevice.PixPayment.PayerDeviceUsage != NoPayerDevice || noDevice.Observations.PayerDevice != nil {
		t.Errorf("05: want no payer device subject, got %+v", noDevice.Observations.PayerDevice)
	}
}
