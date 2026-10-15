package pixrisk

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/pixinput"
)

const examplesDir = "../docs/contracts/examples-v2"

// tolerance bounds the difference between the expected values, computed by
// an independent implementation, and the prepared values of features that
// use logarithms or trigonometry.
const tolerance = 1e-12

type expectedOutcome struct {
	Status   string `json:"status"`
	Features []struct {
		Name  string  `json:"name"`
		Value float64 `json:"value"`
	} `json:"features"`
	Notes    []Note    `json:"notes"`
	Blockers []Blocker `json:"blockers"`
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func decode(t *testing.T, data []byte) pixinput.ScoringInput {
	t.Helper()

	input, err := pixinput.Decode(data, pixinput.ContractV2)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	return input
}

func prepare(t *testing.T, data []byte) Prepared {
	t.Helper()

	prepared, err := Prepare(decode(t, data))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	return prepared
}

// edit applies changes to the complete example; each key is a
// slash-separated path, and a deleted value removes the member.
func edit(t *testing.T, changes map[string]any) []byte {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(readFile(t, filepath.Join(examplesDir, "01-complete.json"))))
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

type deleted struct{}

// index returns the position of a feature in the descriptor.
func index(t *testing.T, name string) int {
	t.Helper()

	for i, feature := range Descriptor() {
		if feature.Name == name {
			return i
		}
	}

	t.Fatalf("no feature named %s", name)
	return -1
}

func TestPrepareSpecificationExamples(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join(examplesDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, input := range inputs {
		if strings.HasSuffix(input, ".expected.json") {
			continue
		}

		name := filepath.Base(input)
		t.Run(name, func(t *testing.T) {
			var expected expectedOutcome
			if err := json.Unmarshal(readFile(t, strings.TrimSuffix(input, ".json")+".expected.json"), &expected); err != nil {
				t.Fatal(err)
			}

			if expected.Status == "invalid_input" {
				return
			}

			prepared, err := Prepare(decode(t, readFile(t, input)))

			if expected.Status == "insufficient_input" {
				var insufficient *InsufficientInputError
				if !errors.As(err, &insufficient) {
					t.Fatalf("Prepare() error = %v, want *InsufficientInputError", err)
				}
				if !reflect.DeepEqual(insufficient.Blockers, expected.Blockers) {
					t.Errorf("blockers = %v\nwant       %v", insufficient.Blockers, expected.Blockers)
				}
				return
			}

			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}

			descriptor := Descriptor()
			if len(expected.Features) != len(descriptor) {
				t.Fatalf("expected %d features, descriptor has %d", len(expected.Features), len(descriptor))
			}

			for i, want := range expected.Features {
				got := prepared.Values[i]

				switch {
				case descriptor[i].Name != want.Name:
					t.Errorf("feature %d = %s, want %s", i, descriptor[i].Name, want.Name)
				case descriptor[i].Kind == Indicator && got != want.Value:
					t.Errorf("%s = %v, want exactly %v", want.Name, got, want.Value)
				case math.Abs(got-want.Value) > tolerance:
					t.Errorf("%s = %v, want %v", want.Name, got, want.Value)
				}
			}

			wantNotes := expected.Notes
			if wantNotes == nil {
				wantNotes = []Note{}
			}
			if !reflect.DeepEqual(prepared.Notes, wantNotes) {
				t.Errorf("notes = %v\nwant    %v", prepared.Notes, wantNotes)
			}
		})
	}
}

// The offset in which decision_time is written never changes the features.
func TestPrepareIgnoresTheDecisionTimeRepresentation(t *testing.T) {
	complete := prepare(t, readFile(t, filepath.Join(examplesDir, "01-complete.json")))
	utc := prepare(t, readFile(t, filepath.Join(examplesDir, "09-utc-decision-time.json")))
	tokyo := prepare(t, edit(t, map[string]any{"decision_time": "2026-10-14T02:03:07.120+09:00"}))

	if complete.Values != utc.Values || complete.Values != tokyo.Values {
		t.Error("the same instant written with different offsets produced different features")
	}
}

func TestPrepareLocalTimeOfDay(t *testing.T) {
	tests := []struct {
		decisionTime, offset string
		seconds              int
	}{
		{"2026-10-13T23:59:59.999Z", "+00:00", 86399},
		{"2026-10-13T00:00:00Z", "+00:00", 0},
		{"2026-10-13T23:59:59.999Z", "+14:00", 50399},
		{"2026-10-13T00:00:00Z", "-14:00", 36000},
		{"2026-10-13T00:00:00Z", "-00:01", 86340},
		{"2026-10-13T17:03:07.120Z", "-03:00", 50587},
		{"1969-12-31T23:59:59.5Z", "+00:00", 86399},
	}

	sin, cos := index(t, "local_time_sin"), index(t, "local_time_cos")

	for _, test := range tests {
		input := timeOnlyInput(t, test.decisionTime, test.offset)

		prepared, err := Prepare(input)
		if err != nil {
			t.Fatalf("%s %s: Prepare() error = %v", test.decisionTime, test.offset, err)
		}

		theta := 2 * math.Pi * float64(test.seconds) / 86400
		if math.Abs(prepared.Values[sin]-math.Sin(theta)) > tolerance ||
			math.Abs(prepared.Values[cos]-math.Cos(theta)) > tolerance {
			t.Errorf("%s %s: (sin, cos) = (%v, %v), want the angle of %d seconds",
				test.decisionTime, test.offset, prepared.Values[sin], prepared.Values[cos], test.seconds)
		}
	}
}

// timeOnlyInput builds a minimal valid input at decisionTime: required
// facts known and fresh, everything optional missing or not applicable.
func timeOnlyInput(t *testing.T, decisionTime, offset string) pixinput.ScoringInput {
	t.Helper()

	document := `{
	  "contract_version": "pix-payer-input-v2",
	  "decision_time": "` + decisionTime + `",
	  "pix_payment": {"amount_cents": 1000, "payer_device_usage": "no_payer_device",
	    "recipient_key_usage": "no_pix_key", "recipient_person_type": "NATURAL_PERSON",
	    "reference_time_offset": "` + offset + `", "payer_limit_period": "no_period_limit"},
	  "observations": {
	    "payer_account": {
	      "settled_outgoing_pix_90d": {"count": 0, "median_amount_cents": null, "as_of": "` + decisionTime + `"},
	      "initiated_outgoing_pix_24h": {"count": 0, "as_of": "` + decisionTime + `"}},
	    "recipient_person": {
	      "settled_pix_from_payer_account_365d": {"count": 0, "as_of": "` + decisionTime + `"},
	      "spi": null, "fraud_markers": null, "infraction_reports": null, "entries": null},
	    "recipient_account": {"entry": null, "pre_registration": null}}}`

	return decode(t, []byte(document))
}

// decision_time of the complete example is 2026-10-13T17:03:07.120Z.
func TestPrepareFreshnessLimitsAreInclusive(t *testing.T) {
	tests := []struct {
		name, path, atLimit, beyond string
		known                       string
	}{
		{"period limit", "observations/payer_account/period_limit/as_of",
			"2026-10-13T16:58:07.120Z", "2026-10-13T16:58:07.119999999Z", "limit_utilization_known"},
		{"device registration", "observations/payer_device/registration/as_of",
			"2026-10-13T16:58:07.120Z", "2026-10-13T16:58:07.119999999Z", "device_registered_known"},
		{"pre-registration", "observations/recipient_account/pre_registration/as_of",
			"2026-10-13T16:58:07.120Z", "2026-10-13T16:58:07.119999999Z", "recipient_pre_registered_known"},
		{"device history", "observations/payer_device/settled_outgoing_pix_90d/as_of",
			"2026-10-12T17:03:07.120Z", "2026-10-12T17:03:07.119999999Z", "device_settled_count_90d_known"},
		{"key entry", "observations/recipient_pix_key/entry/as_of",
			"2026-10-12T17:03:07.120Z", "2026-10-12T17:03:07.119999999Z", "key_entry_known"},
		{"person statistics", "observations/recipient_person/spi/as_of",
			"2026-10-12T17:03:07.120Z", "2026-10-12T17:03:07.119999999Z", "person_settlements_d90_known"},
	}

	for _, test := range tests {
		known := index(t, test.known)

		if got := prepare(t, edit(t, map[string]any{test.path: test.atLimit})).Values[known]; got != 1 {
			t.Errorf("%s at the limit: %s = %v, want 1", test.name, test.known, got)
		}

		prepared := prepare(t, edit(t, map[string]any{test.path: test.beyond}))
		if got := prepared.Values[known]; got != 0 {
			t.Errorf("%s beyond the limit: %s = %v, want 0", test.name, test.known, got)
		}

		want := Note{Path: "/" + test.path, Status: StatusStale}
		if !containsNote(prepared.Notes, want) {
			t.Errorf("%s beyond the limit: notes = %v, want %v", test.name, prepared.Notes, want)
		}
	}

	required := map[string]string{
		"observations/payer_account/initiated_outgoing_pix_24h/as_of":             "2026-10-13T16:58:07.120Z",
		"observations/payer_account/settled_outgoing_pix_90d/as_of":               "2026-10-12T17:03:07.120Z",
		"observations/recipient_person/settled_pix_from_payer_account_365d/as_of": "2026-10-12T17:03:07.120Z",
	}

	for path, atLimit := range required {
		if _, err := Prepare(decode(t, edit(t, map[string]any{path: atLimit}))); err != nil {
			t.Errorf("%s at the limit: Prepare() error = %v", path, err)
		}

		beyond := strings.Replace(atLimit, ".120Z", ".119999999Z", 1)
		_, err := Prepare(decode(t, edit(t, map[string]any{path: beyond})))

		var insufficient *InsufficientInputError
		want := []Blocker{{Path: "/" + path, Reason: ReasonStale}}
		if !errors.As(err, &insufficient) || !reflect.DeepEqual(insufficient.Blockers, want) {
			t.Errorf("%s beyond the limit: error = %v, want blockers %v", path, err, want)
		}
	}

}

func containsNote(notes []Note, want Note) bool {
	for _, note := range notes {
		if note == want {
			return true
		}
	}
	return false
}

func TestPrepareBlocksOnEveryMissingRequiredFact(t *testing.T) {
	_, err := Prepare(decode(t, edit(t, map[string]any{
		"observations/payer_account/settled_outgoing_pix_90d":               nil,
		"observations/payer_account/initiated_outgoing_pix_24h":             nil,
		"observations/recipient_person/settled_pix_from_payer_account_365d": nil,
	})))

	var insufficient *InsufficientInputError
	if !errors.As(err, &insufficient) {
		t.Fatalf("Prepare() error = %v, want *InsufficientInputError", err)
	}

	want := []Blocker{
		{Path: "/observations/payer_account/initiated_outgoing_pix_24h", Reason: ReasonMissing},
		{Path: "/observations/payer_account/settled_outgoing_pix_90d", Reason: ReasonMissing},
		{Path: "/observations/recipient_person/settled_pix_from_payer_account_365d", Reason: ReasonMissing},
	}
	if !reflect.DeepEqual(insufficient.Blockers, want) {
		t.Errorf("blockers = %v\nwant       %v", insufficient.Blockers, want)
	}
}

func TestPrepareAgesCountCompleteDays(t *testing.T) {
	const path = "observations/recipient_account/entry/opening_date"
	age := index(t, "recipient_account_age")

	tests := map[string]float64{
		"2026-10-12T17:03:07.120Z":       1,
		"2026-10-12T17:03:07.120000001Z": 0,
		"2026-10-13T17:03:05.912Z":       0,
		"2026-10-10T17:03:07.121-03:00":  2, // 2026-10-10T20:03:07.121Z
		"0001-01-01T00:00:00Z":           739901,
		"2024-02-29T03:00:00Z":           957,
	}

	for date, days := range tests {
		got := prepare(t, edit(t, map[string]any{path: date})).Values[age]
		if want := math.Log1p(days); got != want {
			t.Errorf("opening_date %s: age = %v, want log1p(%v) = %v", date, got, days, want)
		}
	}
}

func TestPrepareStaysFiniteAtTheLargestValues(t *testing.T) {
	const max = 9007199254740991

	window := map[string]any{"d90": max, "m12": max, "m60": max}
	data := edit(t, map[string]any{
		"pix_payment/amount_cents":                                       max,
		"observations/payer_account/settled_outgoing_pix_90d/count":      max,
		"observations/payer_account/period_limit/limit_cents":            1,
		"observations/payer_account/period_limit/consumed_cents":         max,
		"observations/recipient_person/fraud_markers/application_frauds": window,
		"observations/recipient_person/fraud_markers/mule_accounts":      window,
		"observations/recipient_person/fraud_markers/scammer_accounts":   window,
		"observations/recipient_person/fraud_markers/other_frauds":       window,
		"observations/recipient_person/fraud_markers/unknown_frauds":     window,
	})

	prepared := prepare(t, data)

	for i, value := range prepared.Values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Errorf("feature %d is not finite: %v", i, value)
		}
	}

	// The sum of five counters is exact before the conversion.
	sum := float64(5 * int64(max))
	if got, want := prepared.Values[index(t, "person_fraud_markers_d90")], math.Log1p(sum); got != want {
		t.Errorf("person_fraud_markers_d90 = %v, want %v", got, want)
	}

	utilization := (float64(max) + float64(max)) / 1
	if got, want := prepared.Values[index(t, "limit_utilization")], math.Log1p(utilization); got != want {
		t.Errorf("limit_utilization = %v, want %v", got, want)
	}
}

func TestPrepareDistinguishesKnownFalseFromMissing(t *testing.T) {
	registered, known := index(t, "device_registered"), index(t, "device_registered_known")

	tests := map[string]struct {
		change      any
		value, flag float64
	}{
		"registered":     {"registered", 1, 1},
		"not registered": {"not_registered", 0, 1},
	}

	for name, test := range tests {
		prepared := prepare(t, edit(t, map[string]any{"observations/payer_device/registration/status": test.change}))
		if prepared.Values[registered] != test.value || prepared.Values[known] != test.flag {
			t.Errorf("%s: (value, known) = (%v, %v), want (%v, %v)", name,
				prepared.Values[registered], prepared.Values[known], test.value, test.flag)
		}
	}

	missing := prepare(t, edit(t, map[string]any{"observations/payer_device/registration": nil}))
	if missing.Values[registered] != 0 || missing.Values[known] != 0 {
		t.Errorf("missing: (value, known) = (%v, %v), want (0, 0)", missing.Values[registered], missing.Values[known])
	}
	if !containsNote(missing.Notes, Note{Path: "/observations/payer_device/registration", Status: StatusMissing}) {
		t.Errorf("missing: notes = %v", missing.Notes)
	}
}

// Each window and member of a partial DICT response is kept or dropped on
// its own.
func TestPreparePartialDictResponses(t *testing.T) {
	const person = "observations/recipient_person"

	tests := []struct {
		name    string
		changes map[string]any
		unknown []string
		note    string
	}{
		{"one settlements window", map[string]any{person + "/spi/settlements/m12": nil},
			[]string{"person_settlements_m12"}, "/" + person + "/spi/settlements/m12"},
		{"the settlements counter", map[string]any{person + "/spi/settlements": nil},
			[]string{"person_settlements_d90", "person_settlements_m12"}, "/" + person + "/spi/settlements"},
		{"one fraud marker counter", map[string]any{person + "/fraud_markers/other_frauds": nil},
			[]string{"person_fraud_markers_d90", "person_fraud_markers_m12"}, "/" + person + "/fraud_markers/other_frauds"},
		{"distinct reporters window", map[string]any{person + "/fraud_markers/distinct_fraud_reporters/m12": nil},
			[]string{"person_fraud_reporters_m12"}, "/" + person + "/fraud_markers/distinct_fraud_reporters/m12"},
		{"open report reporters", map[string]any{person + "/infraction_reports/open_reports_distinct_reporters": nil},
			[]string{"person_open_report_reporters"}, "/" + person + "/infraction_reports/open_reports_distinct_reporters"},
		{"registered accounts", map[string]any{person + "/entries/registered_accounts": nil},
			[]string{"person_registered_accounts"}, "/" + person + "/entries/registered_accounts"},
		{"key distinct accounts window", map[string]any{"observations/recipient_pix_key/entries/distinct_accounts/m12": nil},
			[]string{"key_distinct_accounts_m12"}, "/observations/recipient_pix_key/entries/distinct_accounts/m12"},
	}

	complete := prepare(t, readFile(t, filepath.Join(examplesDir, "01-complete.json")))

	for _, test := range tests {
		prepared := prepare(t, edit(t, test.changes))

		for i, feature := range Descriptor() {
			unknown := false
			for _, name := range test.unknown {
				if feature.Name == name || feature.Name == name+"_known" {
					unknown = true
				}
			}

			if unknown && prepared.Values[i] != 0 {
				t.Errorf("%s: %s = %v, want 0", test.name, feature.Name, prepared.Values[i])
			}
			if !unknown && prepared.Values[i] != complete.Values[i] {
				t.Errorf("%s: %s = %v, want its complete value %v", test.name, feature.Name, prepared.Values[i], complete.Values[i])
			}
		}

		if want := []Note{{Path: test.note, Status: StatusMissing}}; !reflect.DeepEqual(prepared.Notes, want) {
			t.Errorf("%s: notes = %v, want %v", test.name, prepared.Notes, want)
		}
	}

	// Windows a feature does not read produce no note.
	if prepared := prepare(t, edit(t, map[string]any{person + "/spi/settlements/m60": nil})); len(prepared.Notes) != 0 {
		t.Errorf("unread m60 window: notes = %v, want none", prepared.Notes)
	}
}

func TestPrepareRequiresVersion2(t *testing.T) {
	for _, version := range []string{"pix-payer-input-v1", ""} {
		input := decode(t, readFile(t, filepath.Join(examplesDir, "01-complete.json")))
		input.ContractVersion = version

		var insufficient *InsufficientInputError
		if _, err := Prepare(input); err == nil || errors.As(err, &insufficient) {
			t.Errorf("Prepare() of a %q input: error = %v, want an operational error", version, err)
		}
	}
}
