package trainingdata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const examplesDir = "../docs/contracts/training-examples"

// scanReport scans a dataset and builds its dataset report.
func scanReport(t *testing.T, data []byte) Report {
	t.Helper()

	var exclusions []Exclusion

	summary, err := Scan(strings.NewReader(string(data)), TransactionFeaturesV1, func(record Record) error {
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

	if len(datasets) != 8 {
		t.Fatalf("found %d example datasets, want 8", len(datasets))
	}

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

			if got := asJSONValue(t, scanReport(t, data)); !reflect.DeepEqual(got, want) {
				t.Errorf("report = %v\nwant     %v", got, want)
			}
		})
	}
}
