package trainingcsv

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/internal/synthetic"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

var update = flag.Bool("update", false, "rewrite the template and the column table of its guide")

const (
	templatePath  = "../templates/training-data/pix-training-example-v2.csv"
	guidePath     = "../templates/training-data/README.md"
	examplesDir   = "../docs/contracts/training-examples-v2"
	baselineCheck = "../docs/contracts/training-config-examples/01-baseline.json"
)

// templateExamples are the specification examples the template rows hold.
var templateExamples = []string{
	"01-usable-legitimate.jsonl",
	"02-usable-fraud.jsonl",
	"05-unknown-loss-no-pix-key.jsonl",
	"06-legitimate-no-device-no-period-limit.jsonl",
}

func read(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// toCSV writes JSON Lines records as CSV rows of the layout, with the
// columns in template order: a missing or null value is an empty cell.
func toCSV(t *testing.T, jsonl []byte, separator rune) []byte {
	t.Helper()

	var out bytes.Buffer
	writer := csv.NewWriter(&out)
	writer.Comma = separator

	columns := Columns()
	header := make([]string, len(columns))
	for i, column := range columns {
		header[i] = column.Name
	}
	writer.Write(header)

	for _, line := range bytes.Split(bytes.TrimSuffix(jsonl, []byte("\n")), []byte("\n")) {
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}

		row := make([]string, len(columns))
		for i, column := range columns {
			row[i] = cellOf(record, column.Name)
		}
		writer.Write(row)
	}

	writer.Flush()
	return out.Bytes()
}

func cellOf(record map[string]any, column string) string {
	path := strings.Split(column, ".")
	if path[0] != "outcome" && path[0] != "group_key" {
		path = append([]string{"scoring_input"}, path...)
	}

	var value any = record
	for _, name := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = object[name]
	}

	switch v := value.(type) {
	case json.Number:
		return v.String()
	case string:
		return v
	default:
		return ""
	}
}

func convert(t *testing.T, data []byte, separator rune) ([]byte, Summary, error) {
	t.Helper()

	var out bytes.Buffer
	summary, err := Convert(bytes.NewReader(data), &out, Options{Separator: separator, MaxRecords: trainingdata.DefaultMaxRecords})
	return out.Bytes(), summary, err
}

// sameRecords compares JSON Lines record by record, as JSON values.
func sameRecords(t *testing.T, got, want []byte) {
	t.Helper()

	gotLines := bytes.Split(bytes.TrimSuffix(got, []byte("\n")), []byte("\n"))
	wantLines := bytes.Split(bytes.TrimSuffix(want, []byte("\n")), []byte("\n"))
	if len(gotLines) != len(wantLines) {
		t.Fatalf("%d records, want %d", len(gotLines), len(wantLines))
	}

	for i := range wantLines {
		var g, w any
		if err := json.Unmarshal(gotLines[i], &g); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(wantLines[i], &w); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("record %d:\n got %s\nwant %s", i+1, gotLines[i], wantLines[i])
		}
	}
}

// validExamples returns the valid records of the specification examples.
func validExamples(t *testing.T) []byte {
	t.Helper()

	var out bytes.Buffer
	_, err := trainingdata.Scan(bytes.NewReader(read(t, filepath.Join(examplesDir, "dataset.jsonl"))), trainingdata.PixRiskV1,
		trainingdata.DefaultMaxRecords, func(r trainingdata.Record) error {
			if r.Category != trainingdata.Invalid {
				line := bytes.Split(read(t, filepath.Join(examplesDir, "dataset.jsonl")), []byte("\n"))[r.Line-1]
				out.Write(append(line, '\n'))
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// nullCounters is the first specification example with one DICT counter
// of the person and one of the key missing as a whole.
func nullCounters(t *testing.T) []byte {
	t.Helper()

	var record map[string]any
	decoder := json.NewDecoder(bytes.NewReader(read(t, filepath.Join(examplesDir, "01-usable-legitimate.jsonl"))))
	decoder.UseNumber()
	if err := decoder.Decode(&record); err != nil {
		t.Fatal(err)
	}

	observations := record["scoring_input"].(map[string]any)["observations"].(map[string]any)
	observations["recipient_person"].(map[string]any)["fraud_markers"].(map[string]any)["mule_accounts"] = nil
	observations["recipient_pix_key"].(map[string]any)["entries"].(map[string]any)["distinct_accounts"] = nil

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

// Records written as CSV and converted back are the same records: the
// specification examples and a synthetic dataset with every absence
// pattern, with either separator.
func TestConvertRoundTrips(t *testing.T) {
	var generated bytes.Buffer
	if err := synthetic.Generate(&generated, synthetic.DefaultOptions(5000, 21)); err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		"specification examples": validExamples(t),
		"synthetic dataset":      generated.Bytes(),
		"null counters":          nullCounters(t),
	}

	for name, jsonl := range cases {
		for _, separator := range []rune{',', ';'} {
			t.Run(name+" "+string(separator), func(t *testing.T) {
				converted, summary, err := convert(t, toCSV(t, jsonl, separator), separator)
				if err != nil {
					t.Fatalf("Convert() error = %v", err)
				}
				sameRecords(t, converted, jsonl)

				if summary.Rows != bytes.Count(jsonl, []byte("\n")) || summary.Records.Total != summary.Rows ||
					summary.Records.Invalid != 0 || summary.DatasetBytes != int64(len(converted)) {
					t.Errorf("summary = %+v", summary)
				}
			})
		}
	}
}

// The converted records are exactly the compact JSON train reads, and the
// summary fingerprints them as train will.
func TestConvertSummarizesWhatTrainReads(t *testing.T) {
	var generated bytes.Buffer
	if err := synthetic.Generate(&generated, synthetic.DefaultOptions(2000, 22)); err != nil {
		t.Fatal(err)
	}

	converted, summary, err := convert(t, toCSV(t, generated.Bytes(), ','), ',')
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(converted, generated.Bytes()) {
		t.Error("the conversion of a compact dataset is not byte for byte the same")
	}

	scanned, err := trainingdata.Scan(bytes.NewReader(converted), trainingdata.PixRiskV1, trainingdata.DefaultMaxRecords,
		func(trainingdata.Record) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if summary.Records != scanned.Records || summary.UsableLabels != scanned.UsableLabels ||
		summary.DatasetSHA256 != scanned.DatasetSHA256 || summary.DatasetBytes != scanned.DatasetBytes {
		t.Errorf("summary = %+v, want the scan's %+v", summary, scanned)
	}
	if summary.Records.Insufficient == 0 || summary.Records.Immature == 0 {
		t.Errorf("records = %+v, want insufficient and immature records", summary.Records)
	}
}

// table builds a CSV file from the template header and rows given as
// column → value changes to the first template row.
func table(t *testing.T, rows ...map[string]string) []byte {
	t.Helper()

	template, err := csv.NewReader(bytes.NewReader(read(t, templatePath))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	header := template[0]

	var out bytes.Buffer
	writer := csv.NewWriter(&out)
	writer.Write(header)
	for _, changes := range rows {
		row := slices.Clone(template[1])
		for column, value := range changes {
			i := slices.Index(header, column)
			if i < 0 {
				t.Fatalf("no column %s", column)
			}
			row[i] = value
		}
		writer.Write(row)
	}
	writer.Flush()
	return out.Bytes()
}

func problems(t *testing.T, err error) []Problem {
	t.Helper()

	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *InvalidError", err)
	}
	for _, p := range invalid.Problems {
		if p.Message == "" {
			t.Errorf("problem %+v has no message", p)
		}
	}
	return invalid.Problems
}

type located struct {
	Row    int
	Column string
	Code   string
}

func locations(list []Problem) []located {
	out := []located{}
	for _, p := range list {
		out = append(out, located{p.Row, p.Column, p.Code})
	}
	return out
}

// Every problem names its row and column and uses the codes of the
// contracts, and nothing converted is valid.
func TestConvertReportsProblemsByRowAndColumn(t *testing.T) {
	const (
		amount     = "pix_payment.amount_cents"
		keyUsage   = "pix_payment.recipient_key_usage"
		deviceAsOf = "observations.payer_device.registration.as_of"
		status     = "observations.payer_device.registration.status"
	)
	header := strings.Join(func() []string {
		var names []string
		for _, c := range Columns() {
			names = append(names, c.Name)
		}
		return names
	}(), ",")

	cases := []struct {
		name string
		data []byte
		want []located
	}{
		{"unknown, duplicate, and missing columns", []byte(strings.Replace(header, "group_key", "groupkey,decision_time", 1) + "\n"),
			[]located{{1, "groupkey", CodeUnknownColumn}, {1, "decision_time", CodeDuplicateColumn}, {1, "group_key", CodeMissingColumn}}},
		{"no header", nil, []located{{1, "", CodeMissingColumn}}},
		{"amount with a comma", table(t, map[string]string{amount: "12,50"}),
			[]located{{2, amount, "wrong_type"}}},
		{"negative zero", table(t, map[string]string{amount: "-0"}),
			[]located{{2, amount, "invalid_number_format"}}},
		{"required value empty", table(t, map[string]string{amount: ""}),
			[]located{{2, amount, "null_not_allowed"}}},
		{"unknown enumeration value", table(t, map[string]string{status: "maybe"}),
			[]located{{2, status, "invalid_enum_value"}}},
		{"value without its as_of", table(t, map[string]string{deviceAsOf: ""}),
			[]located{{2, status, CodeValueWithoutAsOf}}},
		{"subject that does not apply", table(t, map[string]string{keyUsage: "no_pix_key"}), keyColumns(t)},
		{"problems of several rows", table(t, nil, map[string]string{amount: "x"}, map[string]string{"outcome.label": "chargeback"}),
			[]located{{3, amount, "wrong_type"}, {4, "outcome.label", "invalid_enum_value"}}},
		{"wrong number of cells", []byte(header + "\n1,2\n"), []located{{2, "", CodeInvalidCSV}}},
		{"unterminated quote", []byte(header + "\n\"x\n"), []located{{2, "", CodeInvalidCSV}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := convert(t, c.data, ',')
			got := locations(problems(t, err))

			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("problems = %+v\nwant     %+v", got, c.want)
			}
		})
	}
}

// keyColumns are the problems of a first template row whose key usage
// becomes no_pix_key: every key column with a value.
func keyColumns(t *testing.T) []located {
	t.Helper()

	rows, err := csv.NewReader(bytes.NewReader(read(t, templatePath))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	var want []located
	for i, name := range rows[0] {
		if strings.HasPrefix(name, "observations.recipient_pix_key.") && rows[1][i] != "" {
			want = append(want, located{2, name, CodeMemberNotAllowed})
		}
	}
	if len(want) == 0 {
		t.Fatal("the first template row has no Pix key")
	}
	return want
}

// Spreadsheet conventions are accepted: a byte order mark, CRLF line
// endings, quoted cells, columns in any order, and empty rows, which are
// skipped.
func TestConvertAcceptsSpreadsheetConventions(t *testing.T) {
	want, _, err := convert(t, read(t, templatePath), ',')
	if err != nil {
		t.Fatal(err)
	}

	rows, err := csv.NewReader(bytes.NewReader(read(t, templatePath))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	// Reverse the columns, quote every cell, use CRLF, add a BOM and empty
	// rows, and use semicolons.
	var out bytes.Buffer
	out.WriteString("\xef\xbb\xbf")
	for i, row := range rows {
		cells := slices.Clone(row)
		slices.Reverse(cells)
		for j, cell := range cells {
			cells[j] = `"` + strings.ReplaceAll(cell, `"`, `""`) + `"`
		}
		out.WriteString(strings.Join(cells, ";") + "\r\n")
		if i == 1 {
			out.WriteString(strings.Repeat(";", len(row)-1) + "\r\n")
		}
	}

	got, summary, err := convert(t, out.Bytes(), ';')
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("the spreadsheet conventions changed the records")
	}
	if summary.EmptyRows != 1 || summary.Rows != len(rows)-1 {
		t.Errorf("summary = %+v, want one empty row skipped", summary)
	}
}

func TestConvertStopsAtTheRecordLimit(t *testing.T) {
	data := read(t, templatePath)

	_, err := Convert(bytes.NewReader(data), &bytes.Buffer{}, Options{Separator: ',', MaxRecords: 3})
	var limit *trainingdata.RecordLimitError
	if !errors.As(err, &limit) || limit.Limit != 3 {
		t.Errorf("Convert() error = %v, want a *trainingdata.RecordLimitError", err)
	}

	for _, options := range []Options{{Separator: '\t', MaxRecords: 10}, {Separator: ',', MaxRecords: 0}} {
		if _, err := Convert(bytes.NewReader(data), &bytes.Buffer{}, options); err == nil {
			t.Errorf("Convert() with %+v: error = nil", options)
		}
	}
}

// The template holds the template examples, converts, and trains with the
// baseline configuration.
func TestTemplateConvertsAndTrains(t *testing.T) {
	var examples bytes.Buffer
	for _, name := range templateExamples {
		examples.Write(read(t, filepath.Join(examplesDir, name)))
	}

	if *update {
		if err := os.WriteFile(templatePath, toCSV(t, examples.Bytes(), ','), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	converted, summary, err := convert(t, read(t, templatePath), ',')
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	sameRecords(t, converted, examples.Bytes())
	if summary.Records.Usable != len(templateExamples) {
		t.Errorf("summary = %+v, want every row usable", summary)
	}

	cfg, err := trainingconfig.Decode(read(t, baselineCheck))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs = 100
	if _, err := training.Run(context.Background(), bytes.NewReader(converted), cfg, training.DefaultLimits, nil); err != nil {
		t.Errorf("training.Run() error = %v", err)
	}
}

const (
	beginColumns = "<!-- columns: generated by go test ./trainingcsv -update -->"
	endColumns   = "<!-- end of columns -->"
)

func columnTable() string {
	var b strings.Builder
	b.WriteString("| Column | Value | Empty cell |\n|---|---|---|\n")
	for _, c := range Columns() {
		b.WriteString("| `" + c.Name + "` | " + c.Values + " | " + c.Empty + " |\n")
	}
	return b.String()
}

// The guide lists exactly the columns of the layout, in template order.
func TestGuideListsTheColumns(t *testing.T) {
	guide := string(read(t, guidePath))
	start, end := strings.Index(guide, beginColumns), strings.Index(guide, endColumns)
	if start < 0 || end < start {
		t.Fatalf("%s has no column table markers", guidePath)
	}

	generated := guide[:start+len(beginColumns)] + "\n\n" + columnTable() + "\n" + guide[end:]
	if *update {
		if err := os.WriteFile(guidePath, []byte(generated), 0o644); err != nil {
			t.Fatal(err)
		}
		guide = generated
	}

	if guide != generated {
		t.Errorf("the column table of %s is out of date; run go test ./trainingcsv -update", guidePath)
	}

	header, err := csv.NewReader(bytes.NewReader(read(t, templatePath))).Read()
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range Columns() {
		if header[i] != column.Name {
			t.Fatalf("template column %d is %s, want %s", i+1, header[i], column.Name)
		}
	}
}
