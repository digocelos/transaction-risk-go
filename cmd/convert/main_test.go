package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/trainingcsv"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

const template = "../../templates/training-data/pix-training-example-v2.csv"

type result struct {
	code   int
	stdout string
	stderr string
}

func convert(t *testing.T, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return result{code, stdout.String(), stderr.String()}
}

func read(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func entries(t *testing.T, dir string) []string {
	t.Helper()

	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range list {
		names = append(names, entry.Name())
	}
	return names
}

type convertedSummary struct {
	Status        string                   `json:"status"`
	Rows          int                      `json:"rows"`
	EmptyRows     int                      `json:"empty_rows"`
	Records       trainingdata.Counts      `json:"records"`
	UsableLabels  trainingdata.LabelCounts `json:"usable_labels"`
	DatasetSHA256 string                   `json:"dataset_sha256"`
	DatasetBytes  int64                    `json:"dataset_bytes"`
}

// The template converts to the dataset the package writes, and stdout
// summarizes it.
func TestConvertWritesTheDataset(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "training.jsonl")

	r := convert(t, "-input", template, "-output", output)
	if r.code != exitConverted {
		t.Fatalf("exit code %d; stderr:\n%s", r.code, r.stderr)
	}

	var want bytes.Buffer
	summary, err := trainingcsv.Convert(bytes.NewReader(read(t, template)), &want,
		trainingcsv.Options{Separator: ',', MaxRecords: trainingdata.DefaultMaxRecords})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read(t, output), want.Bytes()) {
		t.Error("the written dataset differs from the package's conversion")
	}

	var got convertedSummary
	decoder := json.NewDecoder(strings.NewReader(r.stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&got); err != nil {
		t.Fatalf("stdout %q: %v", r.stdout, err)
	}
	wantOutput := convertedSummary{"converted", summary.Rows, summary.EmptyRows, summary.Records, summary.UsableLabels,
		summary.DatasetSHA256, summary.DatasetBytes}
	if got != wantOutput {
		t.Errorf("stdout = %+v, want %+v", got, wantOutput)
	}
	if !strings.Contains(r.stderr, "4 rows: 4 usable") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if got := entries(t, dir); !reflect.DeepEqual(got, []string{"training.jsonl"}) {
		t.Errorf("directory holds %v", got)
	}
}

// Invalid rows are reported by row and column with exit code 2, and
// nothing is written.
func TestConvertReportsInvalidRows(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Replace(read(t, template), []byte(",250000,"), []byte(",12.5x,"), 1)
	input := filepath.Join(dir, "training.csv")
	if err := os.WriteFile(input, data, 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "training.jsonl")

	r := convert(t, "-input", input, "-output", output)
	if r.code != exitInvalid {
		t.Fatalf("exit code %d; stderr:\n%s", r.code, r.stderr)
	}

	var got struct {
		Status    string                `json:"status"`
		Problems  []trainingcsv.Problem `json:"problems"`
		Truncated bool                  `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "invalid_csv" || len(got.Problems) != 1 || got.Problems[0].Row != 2 ||
		got.Problems[0].Column != "pix_payment.amount_cents" || got.Problems[0].Code != "wrong_type" || got.Truncated {
		t.Errorf("stdout = %s", r.stdout)
	}
	if !strings.Contains(r.stderr, "convert: row 2, column pix_payment.amount_cents: wrong_type:") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if got := entries(t, dir); !reflect.DeepEqual(got, []string{"training.csv"}) {
		t.Errorf("directory holds %v, want no dataset", got)
	}
}

// Semicolons, an existing output with and without -replace, and the
// output being the input.
func TestConvertOptionsAndOutputs(t *testing.T) {
	dir := t.TempDir()
	semicolons := filepath.Join(dir, "semicolons.csv")
	if err := os.WriteFile(semicolons, bytes.ReplaceAll(read(t, template), []byte(","), []byte(";")), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "training.jsonl")

	if r := convert(t, "-input", semicolons, "-separator", ";", "-output", output); r.code != exitConverted {
		t.Fatalf("semicolons: exit code %d; stderr:\n%s", r.code, r.stderr)
	}
	first := read(t, output)

	r := convert(t, "-input", template, "-output", output)
	if r.code != exitFailure || !strings.Contains(r.stderr, "already exists") || !bytes.Equal(read(t, output), first) {
		t.Errorf("existing output: exit code %d; stderr:\n%s", r.code, r.stderr)
	}
	if r := convert(t, "-input", template, "-output", output, "-replace"); r.code != exitConverted || !bytes.Equal(read(t, output), first) {
		t.Errorf("-replace: exit code %d; stderr:\n%s", r.code, r.stderr)
	}

	before := read(t, semicolons)
	if r := convert(t, "-input", semicolons, "-separator", ";", "-output", semicolons, "-replace"); r.code != exitFailure ||
		!strings.Contains(r.stderr, "-output must not be the input") || !bytes.Equal(read(t, semicolons), before) {
		t.Errorf("output as input: exit code %d; stderr:\n%s", r.code, r.stderr)
	}
}

func TestConvertRejectsInvalidArguments(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "out.jsonl")

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"-output", output}, "missing required flags: -input"},
		{[]string{"-input", template}, "missing required flags: -output"},
		{[]string{"-input", template, "-output", output, "-separator", "|"}, "-separator must be , or ;"},
		{[]string{"-input", template, "-output", output, "-max-records", "2"}, "more than 2 records"},
		{[]string{"-input", filepath.Join(dir, "absent.csv"), "-output", output}, "absent.csv"},
		{[]string{"-input", template, "-output", filepath.Join(dir, "absent", "out.jsonl")}, "does not exist"},
		{[]string{"-input", template, "-output", output, "extra"}, `unexpected argument "extra"`},
	}

	for _, c := range cases {
		r := convert(t, c.args...)
		if r.code != exitFailure || r.stdout != "" || !strings.Contains(r.stderr, c.want) {
			t.Errorf("%v: exit code %d, stdout %q, stderr:\n%s", c.args, r.code, r.stdout, r.stderr)
		}
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("directory holds %v", got)
	}

	if r := convert(t, "-h"); r.code != exitConverted || !strings.Contains(r.stderr, "usage: convert") {
		t.Errorf("-h: exit code %d, stderr:\n%s", r.code, r.stderr)
	}
}
