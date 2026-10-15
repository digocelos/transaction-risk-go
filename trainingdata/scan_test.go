package trainingdata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// baseRecord is the usable legitimate example, without its LF.
func baseRecord(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(examplesDir, "01-usable-legitimate.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSuffix(string(data), "\n")
}

// edit replaces exactly one occurrence of old in the base record.
func edit(t *testing.T, old, new string) string {
	t.Helper()

	record := baseRecord(t)
	if strings.Count(record, old) != 1 {
		t.Fatalf("%q must occur exactly once in the base record", old)
	}

	return strings.Replace(record, old, new, 1)
}

func scanAll(t *testing.T, r io.Reader) ([]Record, Summary) {
	t.Helper()

	var records []Record

	summary, err := Scan(r, PixRiskV1, DefaultMaxRecords, func(record Record) error {
		records = append(records, record)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	return records, summary
}

// outcome is the observable classification of one record.
type outcome struct {
	Line     int
	Category Category
	Issues   []IssueReference
}

func outcomes(records []Record) []outcome {
	got := []outcome{}
	for _, record := range records {
		item := outcome{Line: record.Line, Category: record.Category}
		for _, diagnostic := range record.Diagnostics {
			item.Issues = append(item.Issues, IssueReference{diagnostic.Path, diagnostic.Code})
		}
		got = append(got, item)
	}
	return got
}

func invalid(line int, pathCodes ...string) outcome {
	item := outcome{Line: line, Category: Invalid}
	for i := 0; i < len(pathCodes); i += 2 {
		item.Issues = append(item.Issues, IssueReference{pathCodes[i], pathCodes[i+1]})
	}
	return item
}

func TestScanClassifiesRecords(t *testing.T) {
	base := baseRecord(t)
	usable := outcome{Line: 1, Category: Usable}

	tests := []struct {
		name    string
		dataset string
		want    []outcome
	}{
		// Line rules.
		{"record too large", strings.Repeat("x", MaxRecordSize+1) + "\n",
			[]outcome{invalid(1, "", "record_too_large")}},
		{"record of exactly the maximum size", base + strings.Repeat(" ", MaxRecordSize-len(base)) + "\n",
			[]outcome{usable}},
		{"byte order mark on line 1", "\xEF\xBB\xBF" + base + "\n",
			[]outcome{invalid(1, "", "bom_not_allowed")}},
		{"byte order mark on line 2", base + "\n\xEF\xBB\xBF" + base + "\n",
			[]outcome{usable, invalid(2, "", "invalid_json")}},
		{"CRLF line ending", base + "\r\n",
			[]outcome{invalid(1, "", "carriage_return_not_allowed")}},
		{"invalid UTF-8", edit(t, `"g-7c1e9a"`, "\"g-7c1e9\xff\"") + "\n",
			[]outcome{invalid(1, "", "invalid_utf8")}},
		{"empty and whitespace lines", "\n \t\n" + base + "\n",
			[]outcome{invalid(1, "", "blank_line"), invalid(2, "", "blank_line"), {Line: 3, Category: Usable}}},
		{"truncated JSON", "{\"training_example_version\":\n",
			[]outcome{invalid(1, "", "invalid_json")}},
		{"unpaired surrogate", edit(t, `"g-7c1e9a"`, `"\ud800"`) + "\n",
			[]outcome{invalid(1, "", "invalid_json")}},
		{"second value on the line", base + " {}\n",
			[]outcome{invalid(1, "", "trailing_data")}},
		{"trailing spaces and tabs", base + " \t\n", []outcome{usable}},
		{"array record", "[]\n", []outcome{invalid(1, "", "wrong_type")}},
		{"duplicate members anywhere stop the line", edit(t, `"label":"legitimate",`,
			`"label":"legitimate","label":"fraud","extra":1,`) + "\n",
			[]outcome{invalid(1, "/outcome/label", "duplicate_member")}},
		{"duplicate inside the scoring input", edit(t, `"amount_cents":250000`,
			`"amount_cents":250000,"amount_cents":1`) + "\n",
			[]outcome{invalid(1, "/scoring_input/pix_payment/amount_cents", "duplicate_member")}},

		// Version.
		{"version absent", edit(t, `"training_example_version":"pix-training-example-v2",`, "") + "\n",
			[]outcome{invalid(1, "/training_example_version", "required")}},
		{"version null", edit(t, `"pix-training-example-v2"`, "null") + "\n",
			[]outcome{invalid(1, "/training_example_version", "null_not_allowed")}},
		{"version not a string", edit(t, `"pix-training-example-v2"`, "1") + "\n",
			[]outcome{invalid(1, "/training_example_version", "wrong_type")}},
		{"unsupported version hides other issues", edit(t, `"pix-training-example-v2",`,
			`"pix-training-example-v1","extra":1,`) + "\n",
			[]outcome{invalid(1, "/training_example_version", "unsupported_training_example_version")}},

		// Structure and values.
		{"unknown members", edit(t, `"loss_amount_cents":0}`, `"loss_amount_cents":0,"a/b":1},"source":"x"`) + "\n",
			[]outcome{invalid(1, "/outcome/a~1b", "unknown_member", "/source", "unknown_member")}},
		{"required members", edit(t, `,"group_key":"g-7c1e9a"`, "") + "\n",
			[]outcome{invalid(1, "/group_key", "required")}},
		{"required outcome members", edit(t, `"label_as_of":"2026-10-01T03:00:00Z",`, "") + "\n",
			[]outcome{invalid(1, "/outcome/label_as_of", "required")}},
		{"null scoring input", replaceScoringInput(t, "null") + "\n",
			[]outcome{invalid(1, "/scoring_input", "null_not_allowed")}},
		{"scoring input of the wrong type", replaceScoringInput(t, `"x"`) + "\n",
			[]outcome{invalid(1, "/scoring_input", "wrong_type")}},
		{"wrong outcome and group key types", edit(t, `"group_key":"g-7c1e9a"`, `"group_key":7`) + "\n",
			[]outcome{invalid(1, "/group_key", "wrong_type")}},
		{"null label", edit(t, `"label":"legitimate"`, `"label":null`) + "\n",
			[]outcome{invalid(1, "/outcome/label", "null_not_allowed")}},
		{"invalid label", edit(t, `"label":"legitimate"`, `"label":"chargeback"`) + "\n",
			[]outcome{invalid(1, "/outcome/label", "invalid_enum_value")}},
		{"label_as_of without offset", edit(t, `"2026-10-01T03:00:00Z"`, `"2026-10-01T03:00:00"`) + "\n",
			[]outcome{invalid(1, "/outcome/label_as_of", "invalid_format")}},
		{"group key with a slash", edit(t, `"g-7c1e9a"`, `"g/7c1e9a"`) + "\n",
			[]outcome{invalid(1, "/group_key", "invalid_format")}},
		{"group key of 65 characters", edit(t, `"g-7c1e9a"`, `"`+strings.Repeat("k", 65)+`"`) + "\n",
			[]outcome{invalid(1, "/group_key", "invalid_format")}},
		{"loss of negative zero", edit(t, `"loss_amount_cents":0`, `"loss_amount_cents":-0`) + "\n",
			[]outcome{invalid(1, "/outcome/loss_amount_cents", "invalid_number_format")}},
		{"loss above 2^53-1", edit(t, `"label":"legitimate","label_as_of":"2026-10-01T03:00:00Z","loss_amount_cents":0`,
			`"label":"fraud","label_as_of":"2026-10-01T03:00:00Z","loss_amount_cents":9007199254740992`) + "\n",
			[]outcome{invalid(1, "/outcome/loss_amount_cents", "out_of_range")}},
		{"embedded issues are prefixed", edit(t, `"contract_version":"pix-payer-input-v2",`, "") + "\n",
			[]outcome{invalid(1, "/scoring_input/contract_version", "required")}},
		{"embedded document too large", edit(t, `"scoring_input":{`,
			`"scoring_input":{`+strings.Repeat(" ", 70000)) + "\n",
			[]outcome{invalid(1, "/scoring_input", "document_too_large")}},

		// Consistency.
		{"label before decision time", edit(t, `"2026-10-01T03:00:00Z"`, `"2026-05-14T17:03:07.119Z"`) + "\n",
			[]outcome{invalid(1, "/outcome/label_as_of", "label_before_decision_time")}},
		{"legitimate with a loss", edit(t, `"loss_amount_cents":0`, `"loss_amount_cents":5`) + "\n",
			[]outcome{invalid(1, "/outcome/loss_amount_cents", "loss_amount_mismatch")}},
		{"legitimate with an unknown loss", edit(t, `"loss_amount_cents":0`, `"loss_amount_cents":null`) + "\n",
			[]outcome{invalid(1, "/outcome/loss_amount_cents", "loss_amount_mismatch")}},
		{"fraud loss above the amount", edit(t, `"label":"legitimate","label_as_of":"2026-10-01T03:00:00Z","loss_amount_cents":0`,
			`"label":"fraud","label_as_of":"2026-10-01T03:00:00Z","loss_amount_cents":250001`) + "\n",
			[]outcome{invalid(1, "/outcome/loss_amount_cents", "loss_exceeds_amount")}},
		{"rules reading the scoring input wait for a valid one", strings.Replace(
			edit(t, `"amount_cents":250000`, `"amount_cents":-0`),
			`"2026-10-01T03:00:00Z"`, `"2020-01-01T00:00:00Z"`, 1) + "\n",
			[]outcome{invalid(1, "/scoring_input/pix_payment/amount_cents", "invalid_number_format")}},
		{"loss mismatch does not need the scoring input", strings.Replace(
			edit(t, `"amount_cents":250000`, `"amount_cents":-0`),
			`"loss_amount_cents":0`, `"loss_amount_cents":5`, 1) + "\n",
			[]outcome{invalid(1,
				"/outcome/loss_amount_cents", "loss_amount_mismatch",
				"/scoring_input/pix_payment/amount_cents", "invalid_number_format")}},

		// Classification.
		{"label exactly 90 days old", edit(t, `"2026-10-01T03:00:00Z"`, `"2026-08-12T17:03:07.120Z"`) + "\n",
			[]outcome{usable}},
		{"label one nanosecond younger than 90 days", edit(t, `"2026-10-01T03:00:00Z"`, `"2026-08-12T17:03:07.119999999Z"`) + "\n",
			[]outcome{{Line: 1, Category: Immature}}},
		{"insufficient wins over immature", strings.NewReplacer(
			`"initiated_outgoing_pix_24h":{"count":2,"as_of":"2026-05-14T17:03:06.874Z"}`,
			`"initiated_outgoing_pix_24h":{"count":2,"as_of":"2026-05-14T16:00:00Z"}`,
			`"2026-10-01T03:00:00Z"`, `"2026-06-01T00:00:00Z"`,
		).Replace(base),
			[]outcome{{Line: 1, Category: Insufficient}}},
		{"immature but sufficient", edit(t, `"2026-10-01T03:00:00Z"`, `"2026-06-01T00:00:00Z"`),
			[]outcome{{Line: 1, Category: Immature}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records, _ := scanAll(t, strings.NewReader(test.dataset))

			if got := outcomes(records); !reflect.DeepEqual(got, test.want) {
				t.Errorf("records = %+v\nwant      %+v", got, test.want)
			}

			for _, record := range records {
				for _, diagnostic := range record.Diagnostics {
					if diagnostic.Message == "" || diagnostic.Line != record.Line {
						t.Errorf("diagnostic %+v needs a message and the record's line", diagnostic)
					}
				}
			}
		})
	}
}

// replaceScoringInput replaces the whole embedded scoring input.
func replaceScoringInput(t *testing.T, value string) string {
	t.Helper()

	record := baseRecord(t)
	start := strings.Index(record, `"scoring_input":`) + len(`"scoring_input":`)
	end := strings.Index(record, `,"outcome":`)

	return record[:start] + value + record[end:]
}

func TestScanDecodesTypedExamples(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(examplesDir, "dataset.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	records, _ := scanAll(t, bytes.NewReader(data))

	fraud := records[1].Example
	if fraud.Outcome.Label != Fraud || fraud.Outcome.LossAmountCents == nil ||
		*fraud.Outcome.LossAmountCents != 120000 || fraud.GroupKey == nil || *fraud.GroupKey != "g-41d0b2" {
		t.Errorf("line 2 = %+v", fraud)
	}

	if !fraud.Outcome.LabelAsOf.Equal(time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("LabelAsOf = %s", fraud.Outcome.LabelAsOf)
	}

	if fraud.ScoringInput.PixPayment.AmountCents != 180000 {
		t.Errorf("embedded amount = %d", fraud.ScoringInput.PixPayment.AmountCents)
	}

	unknown := records[4].Example
	if unknown.Outcome.LossAmountCents != nil || unknown.GroupKey != nil {
		t.Errorf("line 5 = %+v, want unknown loss and no group key", unknown)
	}

	if blockers := records[8].Blockers; len(blockers) != 1 || blockers[0] != (Blocker{
		Path: "/scoring_input/observations/payer_account/initiated_outgoing_pix_24h/as_of", Reason: "stale"}) {
		t.Errorf("line 9 blockers = %+v", blockers)
	}
}

func TestScanLineBoundaries(t *testing.T) {
	base := baseRecord(t)

	tests := []struct {
		name    string
		dataset string
		lines   int
	}{
		{"empty dataset", "", 0},
		{"single LF", "\n", 1},
		{"last line without LF", base + "\n" + base, 2},
		{"last line with LF", base + "\n" + base + "\n", 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records, summary := scanAll(t, strings.NewReader(test.dataset))

			if len(records) != test.lines || summary.Records.Total != test.lines {
				t.Errorf("records = %d, total = %d, want %d", len(records), summary.Records.Total, test.lines)
			}

			want := sha256.Sum256([]byte(test.dataset))
			if summary.DatasetSHA256 != hex.EncodeToString(want[:]) || summary.DatasetBytes != int64(len(test.dataset)) {
				t.Errorf("fingerprint = %s (%d bytes), want the SHA-256 of the exact bytes", summary.DatasetSHA256, summary.DatasetBytes)
			}
		})
	}
}

func TestScanIsIndependentOfReadSizes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(examplesDir, "dataset.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	whole, wholeSummary := scanAll(t, bytes.NewReader(data))
	oneByte, oneByteSummary := scanAll(t, iotest.OneByteReader(bytes.NewReader(data)))

	if !reflect.DeepEqual(outcomes(whole), outcomes(oneByte)) || wholeSummary != oneByteSummary {
		t.Errorf("one-byte reads changed the result:\n%+v\n%+v", wholeSummary, oneByteSummary)
	}

	if wholeSummary.DatasetSHA256 != "293b11bd762e865402c934328e41827b45bee10ba4a6d947c125ef0af3df7cc8" {
		t.Errorf("DatasetSHA256 = %s", wholeSummary.DatasetSHA256)
	}
}

// repeatReader streams one line count times without holding the dataset.
type repeatReader struct {
	line    []byte
	count   int
	current []byte
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if len(r.current) == 0 {
		if r.count == 0 {
			return 0, io.EOF
		}
		r.count--
		r.current = r.line
	}

	n := copy(p, r.current)
	r.current = r.current[n:]
	return n, nil
}

func heapInUse() uint64 {
	runtime.GC()

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func TestScanUsesBoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("streams tens of megabytes")
	}

	const records = 20000
	line := []byte(baseRecord(t) + "\n")
	baseline := heapInUse()
	var peak uint64

	summary, err := Scan(&repeatReader{line: line, count: records}, PixRiskV1, DefaultMaxRecords, func(record Record) error {
		if record.Line%5000 == 0 {
			peak = max(peak, heapInUse())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if summary.Records.Usable != records {
		t.Fatalf("usable = %d, want %d", summary.Records.Usable, records)
	}

	datasetBytes := uint64(len(line) * records)
	if growth := peak - min(peak, baseline); growth > 8<<20 {
		t.Errorf("heap grew by %d bytes while streaming %d bytes", growth, datasetBytes)
	}
}

func TestScanDiscardsOversizedLinesWithoutBuffering(t *testing.T) {
	const size = 32 << 20
	line := append(bytes.Repeat([]byte("x"), size), '\n')

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	records, _ := scanAll(t, &repeatReader{line: line, count: 1})

	runtime.ReadMemStats(&after)

	if len(records) != 1 || records[0].Diagnostics[0].Code != "record_too_large" {
		t.Fatalf("records = %+v", records)
	}

	// The test's own 32 MiB line is allocated before the measurement.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
		t.Errorf("scan allocated %d bytes for a %d-byte line", allocated, size)
	}
}

func TestScanStopsOnErrors(t *testing.T) {
	errVisit := errors.New("stop")
	base := baseRecord(t)

	visits := 0
	_, err := Scan(strings.NewReader(base+"\n"+base+"\n"), PixRiskV1, DefaultMaxRecords, func(Record) error {
		visits++
		return errVisit
	})
	if !errors.Is(err, errVisit) || visits != 1 {
		t.Errorf("visit error: err = %v after %d visits, want it returned after 1", err, visits)
	}

	errDisk := errors.New("disk failure")
	_, err = Scan(io.MultiReader(strings.NewReader(base+"\n"), iotest.ErrReader(errDisk)),
		PixRiskV1, DefaultMaxRecords, func(Record) error { return nil })
	if !errors.Is(err, errDisk) {
		t.Errorf("reader error: err = %v, want it to wrap %v", err, errDisk)
	}
}

// Scan reads at most the configured number of records: the next line
// fails with a *RecordLimitError before it is decoded or visited, however
// long the stream is.
func TestScanStopsAtTheRecordLimit(t *testing.T) {
	line := []byte(baseRecord(t) + "\n")

	visits := 0
	summary, err := Scan(&repeatReader{line: line, count: 1 << 30}, PixRiskV1, 25, func(Record) error {
		visits++
		return nil
	})

	var limit *RecordLimitError
	if !errors.As(err, &limit) || limit.Limit != 25 {
		t.Fatalf("Scan() error = %v, want a *RecordLimitError with limit 25", err)
	}
	if visits != 25 || summary.Records.Total != 25 {
		t.Errorf("visited %d records and counted %d, want 25", visits, summary.Records.Total)
	}
	if !strings.Contains(err.Error(), "more than 25 records") {
		t.Errorf("message = %q", err)
	}

	// A dataset with exactly the limit is read completely.
	summary, err = Scan(&repeatReader{line: line, count: 25}, PixRiskV1, 25, func(Record) error { return nil })
	if err != nil || summary.Records.Total != 25 || summary.DatasetSHA256 == "" {
		t.Errorf("at the limit: summary %+v, error %v", summary.Records, err)
	}

	for _, invalid := range []int{0, -1} {
		if _, err := Scan(strings.NewReader(""), PixRiskV1, invalid, func(Record) error { return nil }); err == nil {
			t.Errorf("limit %d: Scan() error = nil", invalid)
		}
	}
}
