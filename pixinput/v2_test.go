package pixinput

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const examplesV2Dir = "../docs/contracts/examples-v2"

func readExampleV2(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(examplesV2Dir, name))
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// editV2 applies changes to the complete pix-payer-input-v2 example, as
// edit does for version 1.
func editV2(t *testing.T, changes map[string]any) []byte {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(readExampleV2(t, "01-complete.json")))
	decoder.UseNumber()

	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}

	for path, value := range changes {
		segments := strings.Split(path, "/")
		parent := document

		for _, segment := range segments[:len(segments)-1] {
			child, ok := parent[segment].(map[string]any)
			if !ok {
				t.Fatalf("path %s does not exist in the complete example", path)
			}
			parent = child
		}

		last := segments[len(segments)-1]
		if _, ok := value.(deleted); ok {
			delete(parent, last)
		} else {
			parent[last] = value
		}
	}

	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestDecodeV2SpecificationExamples(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join(examplesV2Dir, "*.json"))
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
			var expected expectedResult
			data := readExampleV2(t, strings.TrimSuffix(name, ".json")+".expected.json")
			if err := json.Unmarshal(data, &expected); err != nil {
				t.Fatal(err)
			}

			_, err := Decode(readExampleV2(t, name), ContractV2)

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
				t.Errorf("issues = %v\nwant     %v", got, want)
			}
		})
	}

	if count != 20 {
		t.Errorf("found %d examples, want 20", count)
	}
}

func TestDecodeV2ReadsNewMembers(t *testing.T) {
	input, err := Decode(readExampleV2(t, "01-complete.json"), ContractV2)
	if err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 10, 13, 17, 3, 6, 874000000, time.UTC)
	payment := input.PixPayment
	observations := input.Observations

	switch {
	case input.ContractVersion != ContractV2:
		t.Errorf("ContractVersion = %q", input.ContractVersion)
	case payment.ReferenceTimeOffsetMinutes != -180:
		t.Errorf("ReferenceTimeOffsetMinutes = %d, want -180", payment.ReferenceTimeOffsetMinutes)
	case payment.PayerLimitPeriod != Daytime:
		t.Errorf("PayerLimitPeriod = %q", payment.PayerLimitPeriod)
	case observations.PayerAccount.PeriodLimit.LimitCents != 500000 ||
		observations.PayerAccount.PeriodLimit.ConsumedCents != 120000 ||
		!observations.PayerAccount.PeriodLimit.AsOf.Equal(at):
		t.Errorf("PeriodLimit = %+v", observations.PayerAccount.PeriodLimit)
	case observations.PayerDevice.Registration.Status != Registered || !observations.PayerDevice.Registration.AsOf.Equal(at):
		t.Errorf("device Registration = %+v", observations.PayerDevice.Registration)
	case observations.RecipientAccount.PreRegistration.Status != NotRegistered:
		t.Errorf("PreRegistration = %+v", observations.RecipientAccount.PreRegistration)
	}
}

func TestDecodeV2ReferenceTimeOffset(t *testing.T) {
	const path = "/pix_payment/reference_time_offset"

	valid := map[string]int{
		"+14:00": 840,
		"-14:00": -840,
		"+00:00": 0,
		"-03:00": -180,
		"+05:45": 345,
		"-09:30": -570,
	}

	for offset, minutes := range valid {
		input, err := Decode(editV2(t, map[string]any{"pix_payment/reference_time_offset": offset}), ContractV2)
		if err != nil {
			t.Errorf("%s: Decode() error = %v", offset, err)
			continue
		}
		if input.PixPayment.ReferenceTimeOffsetMinutes != minutes {
			t.Errorf("%s: minutes = %d, want %d", offset, input.PixPayment.ReferenceTimeOffsetMinutes, minutes)
		}
	}

	invalid := map[string]string{
		"-00:00":  "invalid_format",
		"Z":       "invalid_format",
		"+03:60":  "invalid_format",
		"+3:00":   "invalid_format",
		"+0300":   "invalid_format",
		"03:00":   "invalid_format",
		" -03:00": "invalid_format",
		"−03:00":  "invalid_format",
		"-03:00 ": "invalid_format",
		"+14:01":  "out_of_range",
		"-15:00":  "out_of_range",
		"+99:00":  "out_of_range",
	}

	for offset, code := range invalid {
		_, err := Decode(editV2(t, map[string]any{"pix_payment/reference_time_offset": offset}), ContractV2)
		if got, want := issuesOf(t, err), []pathCode{{path, code}}; !reflect.DeepEqual(got, want) {
			t.Errorf("%q: issues = %v, want %v", offset, got, want)
		}
	}

	_, err := Decode(editV2(t, map[string]any{"pix_payment/reference_time_offset": -180}), ContractV2)
	if got, want := issuesOf(t, err), []pathCode{{path, "wrong_type"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("number: issues = %v, want %v", got, want)
	}
}

func TestDecodeV2ReportsIssues(t *testing.T) {
	const (
		limit        = "/observations/payer_account/period_limit"
		registration = "/observations/payer_device/registration"
		preReg       = "/observations/recipient_account/pre_registration"
	)

	tests := []struct {
		name  string
		input []byte
		want  []pathCode
	}{
		{"missing reference offset", editV2(t, map[string]any{"pix_payment/reference_time_offset": deleted{}}),
			[]pathCode{{"/pix_payment/reference_time_offset", "required"}}},
		{"missing limit period skips the limit presence check", editV2(t, map[string]any{
			"pix_payment/payer_limit_period": deleted{},
		}), []pathCode{{"/pix_payment/payer_limit_period", "required"}}},
		{"unknown limit period", editV2(t, map[string]any{"pix_payment/payer_limit_period": "weekly"}),
			[]pathCode{{"/pix_payment/payer_limit_period", "invalid_enum_value"}}},
		{"applicable period limit absent", editV2(t, map[string]any{
			"observations/payer_account/period_limit": deleted{},
		}), []pathCode{{limit, "required"}}},
		{"not applicable period limit present as null", editV2(t, map[string]any{
			"pix_payment/payer_limit_period":          "no_period_limit",
			"observations/payer_account/period_limit": nil,
		}), []pathCode{{limit, "member_not_allowed"}}},
		{"period limit members", editV2(t, map[string]any{
			"observations/payer_account/period_limit": map[string]any{
				"limit_cents": -1, "consumed_cents": 1.5, "as_of": "2026-10-13T17:03:06Z", "currency": "BRL",
			},
		}), []pathCode{
			{limit + "/consumed_cents", "invalid_number_format"},
			{limit + "/currency", "unknown_member"},
			{limit + "/limit_cents", "invalid_number_format"},
		}},
		{"device registration absent", editV2(t, map[string]any{
			"observations/payer_device/registration": deleted{},
		}), []pathCode{{registration, "required"}}},
		{"device registration status", editV2(t, map[string]any{
			"observations/payer_device/registration/status": "pending",
		}), []pathCode{{registration + "/status", "invalid_enum_value"}}},
		{"pre-registration absent", editV2(t, map[string]any{
			"observations/recipient_account/pre_registration": deleted{},
		}), []pathCode{{preReg, "required"}}},
		{"pre-registration status null", editV2(t, map[string]any{
			"observations/recipient_account/pre_registration/status": nil,
		}), []pathCode{{preReg + "/status", "null_not_allowed"}}},
		{"as_of of new observations", editV2(t, map[string]any{
			"observations/payer_account/period_limit/as_of":         "2026-10-13T17:03:07.121Z",
			"observations/recipient_account/pre_registration/as_of": "2026-10-13T17:03:07.121Z",
		}), []pathCode{
			{limit + "/as_of", "as_of_after_decision_time"},
			{preReg + "/as_of", "as_of_after_decision_time"},
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(test.input, ContractV2)

			if got := issuesOf(t, err); !reflect.DeepEqual(got, test.want) {
				t.Errorf("issues = %v\nwant     %v", got, test.want)
			}
		})
	}
}

func TestDecodeV2AcceptsKnownFactsAndMissingObservations(t *testing.T) {
	tests := map[string]map[string]any{
		"zero limit":               {"observations/payer_account/period_limit/limit_cents": 0},
		"consumption above limit":  {"observations/payer_account/period_limit/consumed_cents": 900000},
		"missing period limit":     {"observations/payer_account/period_limit": nil},
		"missing registration":     {"observations/payer_device/registration": nil},
		"missing pre-registration": {"observations/recipient_account/pre_registration": nil},
		"daily limit":              {"pix_payment/payer_limit_period": "daily"},
		"nighttime limit":          {"pix_payment/payer_limit_period": "nighttime"},
	}

	for name, changes := range tests {
		if _, err := Decode(editV2(t, changes), ContractV2); err != nil {
			t.Errorf("%s: Decode() error = %v", name, err)
		}
	}

	input, err := Decode(editV2(t, tests["missing registration"]), ContractV2)
	if err != nil {
		t.Fatal(err)
	}
	if input.Observations.PayerDevice.Registration != nil {
		t.Error("a null registration must decode as missing")
	}
}

func TestDecodeAcceptsOnlyRequestedVersions(t *testing.T) {
	v1 := readExample(t, completeExample)
	v2 := readExampleV2(t, "01-complete.json")
	unsupported := []pathCode{{"/contract_version", "unsupported_contract_version"}}

	tests := []struct {
		name     string
		input    []byte
		versions []string
		valid    bool
	}{
		{"v1 as v1", v1, []string{ContractV1}, true},
		{"v2 as v2", v2, []string{ContractV2}, true},
		{"v1 when both are accepted", v1, []string{ContractV1, ContractV2}, true},
		{"v2 when both are accepted", v2, []string{ContractV1, ContractV2}, true},
		{"v1 as v2", v1, []string{ContractV2}, false},
		{"v2 as v1", v2, []string{ContractV1}, false},
		{"no version accepted", v1, nil, false},
	}

	for _, test := range tests {
		_, err := Decode(test.input, test.versions...)

		switch {
		case test.valid && err != nil:
			t.Errorf("%s: Decode() error = %v", test.name, err)
		case !test.valid && !reflect.DeepEqual(issuesOf(t, err), unsupported):
			t.Errorf("%s: issues = %v, want %v", test.name, issuesOf(t, err), unsupported)
		}
	}
}

// Version 1 documents keep the version 1 structure: version 2 members are
// unknown to them.
func TestDecodeV1RejectsV2Members(t *testing.T) {
	input := edit(t, map[string]any{
		"pix_payment/reference_time_offset":      "-03:00",
		"observations/payer_device/registration": nil,
	})

	_, err := Decode(input, ContractV1, ContractV2)

	want := []pathCode{
		{"/observations/payer_device/registration", "unknown_member"},
		{"/pix_payment/reference_time_offset", "unknown_member"},
	}
	if got := issuesOf(t, err); !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %v\nwant     %v", got, want)
	}
}
