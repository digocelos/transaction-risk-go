package modelevaluation

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
)

// members returns the member names of the JSON object at path, in order.
func members(t *testing.T, data []byte, path ...string) []string {
	t.Helper()

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	object := data
	for _, name := range path {
		object = root[name]
		root = nil
		if err := json.Unmarshal(object, &root); err != nil {
			t.Fatalf("%s: %v", strings.Join(path, "/"), err)
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(object))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}

	var names []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, token.(string))

		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return names
}

func marshal(t *testing.T, report Report) []byte {
	t.Helper()

	data, err := Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

// The report has a fixed layout, written deterministically with two-space
// indentation, timestamps in UTC, and a final newline.
func TestMarshalWritesTheReportLayout(t *testing.T) {
	dataset := read(t, selectionFixture)
	model, _ := trainModel(t, dataset, selectionConfig(t))

	saoPaulo := time.FixedZone("-03:00", -3*60*60)
	after := time.Date(2026, 6, 5, 21, 0, 0, 0, saoPaulo)
	report := evaluate(t, model, dataset, Options{Scope: WindowScope(after, nil)})
	data := marshal(t, report)

	if again := marshal(t, report); !bytes.Equal(data, again) {
		t.Error("Marshal() is not deterministic")
	}

	// The same instants in another zone give the same bytes.
	zoned := report
	zoned.Scope.After = report.Scope.After.In(saoPaulo)
	zoned.Model.TrainEnd = report.Model.TrainEnd.In(saoPaulo)
	zoned.Model.ValidationEnd = report.Model.ValidationEnd.In(saoPaulo)
	if again := marshal(t, zoned); !bytes.Equal(data, again) {
		t.Error("Marshal() does not write timestamps in UTC")
	}
	if !bytes.HasPrefix(data, []byte("{\n  \"evaluation_report_version\": \"pix-evaluation-report-v1\",\n")) ||
		!bytes.HasSuffix(data, []byte("}\n")) {
		t.Errorf("unexpected encoding:\n%s", data)
	}

	layout := map[string][]string{
		"": {"evaluation_report_version", "software", "model", "dataset", "same_dataset_as_training", "scope",
			"records", "coverage", "group_overlap", "threshold", "metrics", "completeness"},
		"model": {"artifact_version", "artifact_sha256", "software", "contracts", "feature_schema", "config_version",
			"config_sha256", "training_report_version", "training_report_sha256", "training_dataset_sha256",
			"train_end", "validation_end", "selected_epoch", "threshold"},
		"scope":         {"partition", "after", "through"},
		"records":       {"in_scope", "out_of_scope", "in_validation_range"},
		"coverage":      {"usable_records", "populations", "limit_periods", "sources"},
		"group_overlap": {"reference", "shared_keys", "records_sharing_train_keys", "records_without_group_key"},
		"threshold":     {"source", "value"},
		"completeness":  {"complete", "incomplete"},
	}
	for path, want := range layout {
		var segments []string
		if path != "" {
			segments = []string{path}
		}
		if got := members(t, data, segments...); !reflect.DeepEqual(got, want) {
			t.Errorf("members of /%s = %v, want %v", path, got, want)
		}
	}

	var decoded struct {
		Model struct {
			TrainEnd string `json:"train_end"`
		} `json:"model"`
		Scope        map[string]any `json:"scope"`
		Completeness map[string]any `json:"completeness"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	wantScope := map[string]any{"partition": nil, "after": "2026-06-06T00:00:00Z", "through": nil}
	if !reflect.DeepEqual(decoded.Scope, wantScope) {
		t.Errorf("scope = %v, want %v", decoded.Scope, wantScope)
	}
	if decoded.Model.TrainEnd != "2026-05-31T00:00:00Z" {
		t.Errorf("model train_end = %s, want UTC", decoded.Model.TrainEnd)
	}
	if !reflect.DeepEqual(decoded.Completeness["incomplete"], []any{
		map[string]any{"reason": "unknown_loss_frauds",
			"metrics": []any{"known_loss_cents_detected", "known_loss_cents_missed"}},
	}) {
		t.Errorf("completeness = %v", decoded.Completeness)
	}
}

// Undefined values are null, never zero: the partition of a window, the
// counts of an unavailable overlap, and ratios without a denominator. A
// complete evaluation lists no reasons as an empty array.
func TestMarshalWritesUndefinedValuesAsNull(t *testing.T) {
	original := read(t, fixture)
	model, _ := trainModel(t, original, baselineConfig(t))

	report := evaluate(t, model, overlapping(t, original), Options{Scope: WindowScope(utc(6, 5), at(6, 12))})
	data := marshal(t, report)

	var decoded struct {
		GroupOverlap map[string]any `json:"group_overlap"`
		Metrics      map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"shared_keys", "records_sharing_train_keys"} {
		if value, ok := decoded.GroupOverlap[name]; !ok || value != nil {
			t.Errorf("group_overlap.%s = %v, want null", name, value)
		}
	}
	for _, name := range []string{"false_positive_rate", "false_positives_per_1000_legitimate"} {
		if value, ok := decoded.Metrics[name]; !ok || value != nil {
			t.Errorf("metrics.%s = %v, want null", name, value)
		}
	}

	selection := read(t, selectionFixture)
	selectionModel, _ := trainModel(t, selection, selectionConfig(t))
	complete := marshal(t, evaluate(t, selectionModel, selection, Options{Scope: PartitionScope(evaluation.Validation)}))
	if !bytes.Contains(complete, []byte("\"completeness\": {\n    \"complete\": true,\n    \"incomplete\": []\n  }")) {
		t.Errorf("complete evaluation:\n%s", complete)
	}
}

// Marshal refuses a report that contradicts itself.
func TestMarshalRejectsInconsistentReports(t *testing.T) {
	dataset := read(t, selectionFixture)
	model, _ := trainModel(t, dataset, selectionConfig(t))

	valid := evaluate(t, model, dataset, Options{Scope: PartitionScope(evaluation.Test)})

	cases := map[string]func(*Report){
		"unknown threshold source": func(r *Report) { r.Threshold.Source = "tuned" },
		"artifact source, other value": func(r *Report) {
			r.Threshold.Value = r.Model.Threshold.Value / 2
			r.Metrics.Threshold = r.Threshold.Value
		},
		"metrics at another threshold":   func(r *Report) { r.Metrics.Threshold = r.Threshold.Value / 2 },
		"threshold out of range":         func(r *Report) { r.Threshold = Threshold{Source: ThresholdOverride, Value: 2}; r.Metrics.Threshold = 2 },
		"invalid records":                func(r *Report) { r.Dataset.Records.Invalid = 1 },
		"scope before train_end":         func(r *Report) { r.Scope.After = r.Model.TrainEnd.Add(-time.Second) },
		"empty scope":                    func(r *Report) { r.Scope.Through = &r.Scope.After },
		"partition with other bounds":    func(r *Report) { r.Scope.After = r.Scope.After.Add(time.Hour) },
		"same dataset flag":              func(r *Report) { r.SameDatasetAsTraining = false },
		"unknown overlap reference":      func(r *Report) { r.GroupOverlap.Reference = "guess" },
		"unavailable overlap with count": func(r *Report) { r.GroupOverlap.Reference = ReferenceUnavailable },
		"in-scope counts":                func(r *Report) { r.Records.InScope.Fraud++ },
		"coverage counts":                func(r *Report) { r.Coverage.UsableRecords++ },
		"completeness":                   func(r *Report) { r.Completeness = Completeness{Complete: true, Incomplete: []Incomplete{}} },
	}

	if _, err := Marshal(valid); err != nil {
		t.Fatalf("Marshal(valid) error = %v", err)
	}

	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			report := evaluate(t, model, dataset, Options{Scope: PartitionScope(evaluation.Test)})
			change(&report)

			if _, err := Marshal(report); err == nil || !strings.Contains(err.Error(), "invalid evaluation report") {
				t.Fatalf("Marshal() error = %v, want an invalid evaluation report", err)
			}
		})
	}
}
