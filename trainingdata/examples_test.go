package trainingdata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const examplesDir = "../docs/contracts/training-examples-v2"

// scanReport scans a dataset and builds its dataset report.
func scanReport(t *testing.T, data []byte) Report {
	t.Helper()
	return scanReportWith(t, data, PixRiskV1)
}

// scanReportWith scans a dataset with a feature schema and builds its
// dataset report.
func scanReportWith(t *testing.T, data []byte, schema FeatureSchema) Report {
	t.Helper()

	var exclusions []Exclusion

	summary, err := Scan(strings.NewReader(string(data)), schema, DefaultMaxRecords, func(record Record) error {
		if exclusion, ok := record.Exclusion(); ok {
			exclusions = append(exclusions, exclusion)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	return NewReport(summary, exclusions)
}

// asJSONValue converts a value to its generic JSON form for comparison.
func asJSONValue(t *testing.T, value any) any {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}

	return generic
}

func TestScanSpecificationExamples(t *testing.T) {
	datasets, err := filepath.Glob(filepath.Join(examplesDir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	if len(datasets) != 11 {
		t.Fatalf("found %d example datasets, want 11", len(datasets))
	}

	checkExamples(t, datasets, PixRiskV1)
}

// checkExamples compares the dataset report of each example dataset with
// its expected report.
func checkExamples(t *testing.T, datasets []string, schema FeatureSchema) {
	t.Helper()

	for _, dataset := range datasets {
		t.Run(filepath.Base(dataset), func(t *testing.T) {
			data, err := os.ReadFile(dataset)
			if err != nil {
				t.Fatal(err)
			}

			expected, err := os.ReadFile(strings.TrimSuffix(dataset, ".jsonl") + ".expected.json")
			if err != nil {
				t.Fatal(err)
			}

			var want any
			if err := json.Unmarshal(expected, &want); err != nil {
				t.Fatal(err)
			}

			if got := asJSONValue(t, scanReportWith(t, data, schema)); !reflect.DeepEqual(got, want) {
				t.Errorf("report = %v\nwant     %v", got, want)
			}
		})
	}
}

// A feature schema reads only its own training example version, and the
// scoring input version that version embeds.
func TestScanRejectsOtherVersions(t *testing.T) {
	v2, err := os.ReadFile(filepath.Join(examplesDir, "01-usable-legitimate.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	v1 := strings.Replace(string(v2), `"pix-training-example-v2"`, `"pix-training-example-v1"`, 1)
	embeddedV1 := strings.Replace(string(v2), `"contract_version":"pix-payer-input-v2"`, `"contract_version":"pix-payer-input-v1"`, 1)

	tests := []struct {
		name string
		data string
		want IssueReference
	}{
		{"retired training example version", v1,
			IssueReference{Path: "/training_example_version", Code: CodeUnsupportedTrainingExampleVersion}},
		{"retired embedded scoring input version", embeddedV1,
			IssueReference{Path: "/scoring_input/contract_version", Code: "unsupported_contract_version"}},
	}

	for _, test := range tests {
		report := scanReportWith(t, []byte(test.data), PixRiskV1)

		want := []Exclusion{{Line: 1, Category: Invalid, Issues: []IssueReference{test.want}}}
		if !reflect.DeepEqual(report.Exclusions, want) {
			t.Errorf("%s: exclusions = %+v, want %+v", test.name, report.Exclusions, want)
		}
	}
}
