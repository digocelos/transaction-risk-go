package evaluation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

var testBoundaries = Boundaries{
	TrainEnd:      time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
	ValidationEnd: time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC),
}

// record builds a classified record with the given decision time.
func record(category trainingdata.Category, decision time.Time, label trainingdata.Label, groupKey string) trainingdata.Record {
	example := trainingdata.Example{
		ScoringInput: pixinput.ScoringInput{DecisionTime: decision},
		Outcome:      trainingdata.Outcome{Label: label},
	}
	if groupKey != "" {
		example.GroupKey = &groupKey
	}

	return trainingdata.Record{Category: category, Example: example}
}

func usable(decision time.Time, label trainingdata.Label, groupKey string) trainingdata.Record {
	return record(trainingdata.Usable, decision, label, groupKey)
}

func TestPartitionerAssignsByDecisionTime(t *testing.T) {
	brasilia := time.FixedZone("", -3*60*60)

	tests := []struct {
		name     string
		decision time.Time
		want     Partition
	}{
		{"long before train_end", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Train},
		{"exactly train_end", testBoundaries.TrainEnd, Train},
		{"one nanosecond after train_end", testBoundaries.TrainEnd.Add(time.Nanosecond), Validation},
		{"validation_end in another offset", time.Date(2026, 9, 30, 20, 59, 59, 0, brasilia), Validation},
		{"one nanosecond after validation_end", testBoundaries.ValidationEnd.Add(time.Nanosecond), Test},
		{"long after validation_end", time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), Test},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			partitioner, err := NewPartitioner(testBoundaries)
			if err != nil {
				t.Fatal(err)
			}

			got, ok := partitioner.Add(usable(test.decision, trainingdata.Legitimate, ""))
			if !ok || got != test.want {
				t.Errorf("Add() = %q, %t; want %q, true", got, ok, test.want)
			}
		})
	}
}

func TestNewPartitionerRejectsInvalidBoundaries(t *testing.T) {
	tests := map[string]Boundaries{
		"equal boundaries":    {TrainEnd: testBoundaries.TrainEnd, ValidationEnd: testBoundaries.TrainEnd},
		"reversed boundaries": {TrainEnd: testBoundaries.ValidationEnd, ValidationEnd: testBoundaries.TrainEnd},
		"missing train_end":   {ValidationEnd: testBoundaries.ValidationEnd},
		"missing both":        {},
	}

	for name, boundaries := range tests {
		if _, err := NewPartitioner(boundaries); err == nil {
			t.Errorf("%s: NewPartitioner() error = nil, want an error", name)
		}
	}
}

func TestPartitionerCountsEveryRecordOnce(t *testing.T) {
	trainTime := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	validationTime := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	partitioner, err := NewPartitioner(testBoundaries)
	if err != nil {
		t.Fatal(err)
	}

	records := []trainingdata.Record{
		usable(trainTime, trainingdata.Fraud, ""),
		usable(trainTime, trainingdata.Legitimate, ""),
		usable(trainTime, trainingdata.Legitimate, ""),
		record(trainingdata.Insufficient, trainTime, trainingdata.Legitimate, ""),
		record(trainingdata.Immature, validationTime, trainingdata.Fraud, ""),
		usable(validationTime, trainingdata.Fraud, ""),
		{Category: trainingdata.Invalid, Line: 9},
	}

	partitioned := 0
	for _, r := range records {
		if _, ok := partitioner.Add(r); ok {
			partitioned++
		}
	}

	if partitioned != 6 {
		t.Errorf("partitioned %d records, want 6: invalid records have no partition", partitioned)
	}

	report := partitioner.Report()
	want := PartitionReport{
		TrainEnd:      testBoundaries.TrainEnd,
		ValidationEnd: testBoundaries.ValidationEnd,
		Train:         PartitionCounts{Fraud: 1, Legitimate: 2, Insufficient: 1},
		Validation:    PartitionCounts{Fraud: 1, Immature: 1},
		Test:          PartitionCounts{},
	}

	if report != want {
		t.Errorf("Report() = %+v\nwant       %+v", report, want)
	}
}

func TestPartitionerMeasuresGroupOverlap(t *testing.T) {
	trainTime := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	validationTime := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	testTime := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	partitioner, err := NewPartitioner(testBoundaries)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range []trainingdata.Record{
		// "a" spans train and test; its two test records share a train key.
		usable(testTime, trainingdata.Fraud, "a"),
		usable(trainTime, trainingdata.Legitimate, "a"),
		usable(testTime, trainingdata.Legitimate, "a"),
		// "b" spans validation and test, but never train.
		usable(validationTime, trainingdata.Legitimate, "b"),
		usable(testTime, trainingdata.Legitimate, "b"),
		// "c" stays in train.
		usable(trainTime, trainingdata.Fraud, "c"),
		usable(trainTime, trainingdata.Legitimate, "c"),
		// "d" is usable only in train; its test record is insufficient.
		usable(trainTime, trainingdata.Legitimate, "d"),
		record(trainingdata.Insufficient, testTime, trainingdata.Legitimate, "d"),
		// Records without a key are never counted.
		usable(trainTime, trainingdata.Legitimate, ""),
		usable(testTime, trainingdata.Legitimate, ""),
	} {
		partitioner.Add(r)
	}

	want := GroupOverlap{SharedKeys: 2, RecordsSharingTrainKeys: 2}
	if got := partitioner.Report().GroupOverlap; got != want {
		t.Errorf("GroupOverlap = %+v, want %+v", got, want)
	}
}

func TestPartitionReportJSON(t *testing.T) {
	partitioner, err := NewPartitioner(testBoundaries)
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(partitioner.Report())
	if err != nil {
		t.Fatal(err)
	}

	want := `{"train_end":"2026-06-30T23:59:59Z","validation_end":"2026-09-30T23:59:59Z",` +
		`"train":{"fraud":0,"legitimate":0,"insufficient":0,"immature":0},` +
		`"validation":{"fraud":0,"legitimate":0,"insufficient":0,"immature":0},` +
		`"test":{"fraud":0,"legitimate":0,"insufficient":0,"immature":0},` +
		`"group_overlap":{"shared_keys":0,"records_sharing_train_keys":0}}`

	if string(data) != want {
		t.Errorf("JSON = %s\nwant   %s", data, want)
	}
}
