package pixinput

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const completeExample = "01-complete.json"

// replace substitutes exactly one occurrence of old in the complete example.
func replace(t *testing.T, old, new string) []byte {
	t.Helper()

	text := string(readExample(t, completeExample))
	if strings.Count(text, old) != 1 {
		t.Fatalf("%q must occur exactly once in the complete example", old)
	}

	return []byte(strings.Replace(text, old, new, 1))
}

// edit applies changes to the complete example. Each key of changes is a
// slash-separated path; a nil value of type deleted removes the member.
func edit(t *testing.T, changes map[string]any) []byte {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(readExample(t, completeExample)))
	decoder.UseNumber()

	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}

	for path, value := range changes {
		segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
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

func raw(text string) json.RawMessage {
	return json.RawMessage(text)
}

func TestDecodeReportsIssues(t *testing.T) {
	const (
		settled   = "/observations/payer_account/settled_outgoing_pix_90d"
		initiated = "/observations/payer_account/initiated_outgoing_pix_24h"
		keyEntry  = "/observations/recipient_pix_key/entry"
		account   = "/observations/recipient_account/entry"
	)

	tooLarge := append(readExample(t, completeExample), bytes.Repeat([]byte(" "), MaxDocumentSize)...)
	tooLarge = tooLarge[:MaxDocumentSize+1]

	tests := []struct {
		name  string
		input []byte
		want  []pathCode
	}{
		// Document.
		{"document too large", tooLarge, []pathCode{{"", "document_too_large"}}},
		{"byte order mark", append([]byte("\xEF\xBB\xBF"), readExample(t, completeExample)...),
			[]pathCode{{"", "bom_not_allowed"}}},
		{"invalid UTF-8", replace(t, `"EVP"`, "\"EV\xff\""), []pathCode{{"", "invalid_utf8"}}},
		{"empty document", []byte(""), []pathCode{{"", "invalid_json"}}},
		{"whitespace only", []byte(" \n\t"), []pathCode{{"", "invalid_json"}}},
		{"truncated JSON", []byte(`{"contract_version":`), []pathCode{{"", "invalid_json"}}},
		{"unpaired high surrogate", replace(t, `"EVP"`, `"\ud800"`), []pathCode{{"", "invalid_json"}}},
		{"unpaired low surrogate", replace(t, `"EVP"`, `"\udc00"`), []pathCode{{"", "invalid_json"}}},
		{"control character in string", replace(t, `"EVP"`, "\"E\tVP\""), []pathCode{{"", "invalid_json"}}},
		{"second value", append(readExample(t, completeExample), []byte("{}")...),
			[]pathCode{{"", "trailing_data"}}},
		{"non-whitespace after value", append(readExample(t, completeExample), 'x'),
			[]pathCode{{"", "trailing_data"}}},
		{"array document", []byte(`[]`), []pathCode{{"", "wrong_type"}}},
		{"string document", []byte(`"pix-payer-input-v1"`), []pathCode{{"", "wrong_type"}}},
		{"duplicate members", replace(t, `"amount_cents": 250000,`,
			`"amount_cents": 250000, "amount_cents": 1, "a/b": 1, "a/b": 2,`),
			[]pathCode{{"/pix_payment/amount_cents", "duplicate_member"}, {"/pix_payment/a~1b", "duplicate_member"}}},

		// Contract version.
		{"version absent", edit(t, map[string]any{"contract_version": deleted{}}),
			[]pathCode{{"/contract_version", "required"}}},
		{"version null", edit(t, map[string]any{"contract_version": nil}),
			[]pathCode{{"/contract_version", "null_not_allowed"}}},
		{"version not a string", edit(t, map[string]any{"contract_version": 1}),
			[]pathCode{{"/contract_version", "wrong_type"}}},
		{"version unsupported hides other issues", edit(t, map[string]any{
			"contract_version":         "pix-payer-input-v1",
			"pix_payment/amount_cents": 0,
			"channel":                  "mobile",
		}), []pathCode{{"/contract_version", "unsupported_contract_version"}}},

		// Structure.
		{"unknown members are escaped", replace(t, `"payer_account": {`,
			`"payer_account": {"a/b~c": null,`), []pathCode{
			{"/observations/payer_account/a~1b~0c", "unknown_member"},
		}},
		{"unknown root member", edit(t, map[string]any{"channel": "mobile"}),
			[]pathCode{{"/channel", "unknown_member"}}},
		{"required members", edit(t, map[string]any{
			"decision_time":                                      deleted{},
			"observations/recipient_account":                     deleted{},
			settled[1:] + "/as_of":                               deleted{},
			"observations/recipient_pix_key/spi/settlements/d90": deleted{},
		}), []pathCode{
			{"/decision_time", "required"},
			{settled + "/as_of", "required"},
			{"/observations/recipient_account", "required"},
			{"/observations/recipient_pix_key/spi/settlements/d90", "required"},
		}},
		{"applicable subjects are required", edit(t, map[string]any{
			"observations/payer_device":      deleted{},
			"observations/recipient_pix_key": deleted{},
		}), []pathCode{
			{"/observations/payer_device", "required"},
			{"/observations/recipient_pix_key", "required"},
		}},
		{"not applicable subjects are not allowed, even as null", edit(t, map[string]any{
			"pix_payment/payer_device_usage":                "no_payer_device",
			"pix_payment/recipient_key_usage":               "no_pix_key",
			"observations/payer_device":                     nil,
			"observations/recipient_pix_key/entry/key_type": "evp",
		}), []pathCode{
			{"/observations/payer_device", "member_not_allowed"},
			{"/observations/recipient_pix_key", "member_not_allowed"},
		}},
		{"invalid discriminator still validates a present subject", edit(t, map[string]any{
			"pix_payment/recipient_key_usage":               "qr_code",
			"observations/recipient_pix_key/entry/key_type": "evp",
		}), []pathCode{
			{"/observations/recipient_pix_key/entry/key_type", "invalid_format"},
			{"/pix_payment/recipient_key_usage", "invalid_enum_value"},
		}},
		{"invalid discriminator leaves an absent subject alone", edit(t, map[string]any{
			"pix_payment/payer_device_usage": "maybe",
			"observations/payer_device":      deleted{},
		}), []pathCode{{"/pix_payment/payer_device_usage", "invalid_enum_value"}}},
		{"null where null is not allowed", edit(t, map[string]any{
			"pix_payment/amount_cents":                      nil,
			"observations/payer_device":                     nil,
			"observations/recipient_pix_key/entry/key_type": nil,
			account[1:] + "/opening_date":                   nil,
			initiated[1:] + "/as_of":                        nil,
			initiated[1:] + "/count":                        nil,
		}), []pathCode{
			{initiated + "/as_of", "null_not_allowed"},
			{initiated + "/count", "null_not_allowed"},
			{"/observations/payer_device", "null_not_allowed"},
			{account + "/opening_date", "null_not_allowed"},
			{keyEntry + "/key_type", "null_not_allowed"},
			{"/pix_payment/amount_cents", "null_not_allowed"},
		}},
		{"wrong member types are not descended", edit(t, map[string]any{
			"pix_payment/amount_cents":                      "250000",
			"observations/payer_account":                    []any{},
			"observations/recipient_person/spi/settlements": 12,
			"decision_time":                                 1,
		}), []pathCode{
			{"/decision_time", "wrong_type"},
			{"/observations/payer_account", "wrong_type"},
			{"/observations/recipient_person/spi/settlements", "wrong_type"},
			{"/pix_payment/amount_cents", "wrong_type"},
		}},

		// Integers.
		{"negative zero", replace(t, `"amount_cents": 250000`, `"amount_cents": -0`),
			[]pathCode{{"/pix_payment/amount_cents", "invalid_number_format"}}},
		{"negative integer", replace(t, `"count": 31`, `"count": -1`),
			[]pathCode{{settled + "/count", "invalid_number_format"}}},
		{"fraction", replace(t, `"count": 31`, `"count": 31.0`),
			[]pathCode{{settled + "/count", "invalid_number_format"}}},
		{"exponent", replace(t, `"count": 31`, `"count": 1e3`),
			[]pathCode{{settled + "/count", "invalid_number_format"}}},
		{"zero amount", edit(t, map[string]any{"pix_payment/amount_cents": 0}),
			[]pathCode{{"/pix_payment/amount_cents", "out_of_range"}}},
		{"zero median", edit(t, map[string]any{settled[1:] + "/median_amount_cents": 0}),
			[]pathCode{{settled + "/median_amount_cents", "out_of_range"}}},
		{"above 2^53-1", replace(t, `"count": 31`, `"count": 9007199254740992`),
			[]pathCode{{settled + "/count", "out_of_range"}}},
		{"beyond int64", replace(t, `"count": 31`, `"count": 99999999999999999999999`),
			[]pathCode{{settled + "/count", "out_of_range"}}},

		// Formats and enumerations.
		{"timestamp without offset", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"lowercase t", edit(t, map[string]any{"decision_time": "2026-10-13t17:03:07Z"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"lowercase z", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07z"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"unknown local offset", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07-00:00"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"leap second", edit(t, map[string]any{"decision_time": "2026-12-31T23:59:60Z"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"ten fractional digits", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07.1234567891Z"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"empty fraction", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07.Z"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"missing seconds", edit(t, map[string]any{"decision_time": "2026-10-13T17:03Z"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"year zero", edit(t, map[string]any{"decision_time": "0000-10-13T17:03:07Z"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"nonexistent date", edit(t, map[string]any{"decision_time": "2026-02-30T17:03:07Z"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"hour 24", edit(t, map[string]any{"decision_time": "2026-10-13T24:00:00Z"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"offset hour 24", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07+24:00"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"offset without colon", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07+0300"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"time zone name", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07 BRT"}),
			[]pathCode{{"/decision_time", "invalid_format"}}},
		{"lowercase key type", edit(t, map[string]any{keyEntry[1:] + "/key_type": "evp"}),
			[]pathCode{{keyEntry + "/key_type", "invalid_format"}}},
		{"empty key type", edit(t, map[string]any{keyEntry[1:] + "/key_type": ""}),
			[]pathCode{{keyEntry + "/key_type", "invalid_format"}}},
		{"key type with spaces", edit(t, map[string]any{keyEntry[1:] + "/key_type": " EVP"}),
			[]pathCode{{keyEntry + "/key_type", "invalid_format"}}},
		{"key type of 33 characters", edit(t, map[string]any{keyEntry[1:] + "/key_type": strings.Repeat("K", 33)}),
			[]pathCode{{keyEntry + "/key_type", "invalid_format"}}},
		{"closed enumerations", edit(t, map[string]any{
			"pix_payment/recipient_key_usage":   "qr_code",
			"pix_payment/recipient_person_type": "natural_person",
			"observations/recipient_pix_key":    deleted{},
		}), []pathCode{
			{"/pix_payment/recipient_key_usage", "invalid_enum_value"},
			{"/pix_payment/recipient_person_type", "invalid_enum_value"},
		}},

		// Consistency.
		{"as_of after decision_time", edit(t, map[string]any{
			settled[1:] + "/as_of": "2026-10-13T14:03:07.121-03:00",
			account[1:] + "/as_of": "2026-10-14T00:00:00Z",
		}), []pathCode{
			{settled + "/as_of", "as_of_after_decision_time"},
			{account + "/as_of", "as_of_after_decision_time"},
		}},
		{"median present with an empty history", edit(t, map[string]any{settled[1:] + "/count": 0}),
			[]pathCode{{settled + "/median_amount_cents", "median_count_mismatch"}}},
		{"median missing with a history", edit(t, map[string]any{settled[1:] + "/median_amount_cents": nil}),
			[]pathCode{{settled + "/median_amount_cents", "median_count_mismatch"}}},
		{"dates after as_of", edit(t, map[string]any{
			keyEntry[1:] + "/creation_date":      "2026-10-13T17:03:06Z",
			keyEntry[1:] + "/key_ownership_date": "2026-10-13T17:03:06.500Z",
			account[1:] + "/opening_date":        "2026-10-13T17:03:06Z",
		}), []pathCode{
			{account + "/opening_date", "date_after_as_of"},
			{keyEntry + "/creation_date", "date_after_as_of"},
			{keyEntry + "/key_ownership_date", "date_after_as_of"},
			{keyEntry + "/key_ownership_date", "key_ownership_after_creation"},
		}},
		{"consistency skips invalid values", edit(t, map[string]any{
			settled[1:] + "/count":               0,
			settled[1:] + "/median_amount_cents": "25000",
			keyEntry[1:] + "/creation_date":      "yesterday",
			keyEntry[1:] + "/key_ownership_date": "2026-10-13T17:03:06Z",
		}), []pathCode{
			{settled + "/median_amount_cents", "wrong_type"},
			{keyEntry + "/creation_date", "invalid_format"},
			{keyEntry + "/key_ownership_date", "date_after_as_of"},
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

func TestDecodeAcceptsBoundaryValues(t *testing.T) {
	const settled = "observations/payer_account/settled_outgoing_pix_90d"

	tests := []struct {
		name  string
		input []byte
	}{
		{"largest integer", edit(t, map[string]any{"pix_payment/amount_cents": raw("9007199254740991")})},
		{"zero count", edit(t, map[string]any{settled + "/count": 0, settled + "/median_amount_cents": nil})},
		{"nine fractional digits", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07.123456789Z"})},
		{"half-hour offset", edit(t, map[string]any{"decision_time": "2026-10-13T22:33:07.120+05:30"})},
		{"positive zero offset", edit(t, map[string]any{"decision_time": "2026-10-13T17:03:07.120+00:00"})},
		{"as_of equal to decision_time in another offset", edit(t, map[string]any{
			settled + "/as_of": "2026-10-13T17:03:07.120Z",
		})},
		{"unknown key type is preserved", edit(t, map[string]any{
			"observations/recipient_pix_key/entry/key_type": "NEW_KEY_TYPE_2027",
		})},
		{"leap day", edit(t, map[string]any{
			"observations/recipient_account/entry/opening_date": "2024-02-29T03:00:00Z",
		})},
		{"exactly the maximum size", padTo(t, MaxDocumentSize)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode(test.input, ContractV2); err != nil {
				t.Errorf("Decode() error = %v", err)
			}
		})
	}
}

func TestDecodePreservesOpenKeyType(t *testing.T) {
	input, err := Decode(edit(t, map[string]any{
		"observations/recipient_pix_key/entry/key_type": "NEW_KEY_TYPE_2027",
	}), ContractV2)
	if err != nil {
		t.Fatal(err)
	}

	if got := input.Observations.RecipientPixKey.Entry.KeyType; got != "NEW_KEY_TYPE_2027" {
		t.Errorf("KeyType = %q, want it preserved", got)
	}
}

// padTo appends whitespace to the complete example until it has size bytes.
func padTo(t *testing.T, size int) []byte {
	t.Helper()

	data := readExample(t, completeExample)
	return append(data, bytes.Repeat([]byte(" "), size-len(data))...)
}
