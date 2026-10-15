// Package evaluation splits training datasets into temporal partitions and
// measures fraud models on scored examples, following the evaluation
// protocol of the pix-training-example-v2 contract. It never trains a
// model, reads the system clock, or splits data at random.
package evaluation

import (
	"errors"
	"time"

	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// Partition is one temporal subset of a dataset.
type Partition string

const (
	Train      Partition = "train"
	Validation Partition = "validation"
	Test       Partition = "test"
)

// Boundaries split records by the decision time of their scoring input:
// train is decision_time ≤ TrainEnd, validation is TrainEnd < decision_time
// ≤ ValidationEnd, and test is decision_time > ValidationEnd. Boundaries are
// compared as instants, whatever their offsets.
type Boundaries struct {
	TrainEnd      time.Time
	ValidationEnd time.Time
}

// PartitionOf returns the partition of a decision time.
func (b Boundaries) PartitionOf(decisionTime time.Time) Partition {
	switch {
	case !decisionTime.After(b.TrainEnd):
		return Train
	case !decisionTime.After(b.ValidationEnd):
		return Validation
	default:
		return Test
	}
}

// PartitionCounts holds the records of one partition by outcome. Fraud and
// Legitimate count usable records only.
type PartitionCounts struct {
	Fraud        int `json:"fraud"`
	Legitimate   int `json:"legitimate"`
	Insufficient int `json:"insufficient"`
	Immature     int `json:"immature"`
}

// GroupOverlap discloses group keys shared across partitions. It counts
// usable records with a group key only; keys are never model inputs.
type GroupOverlap struct {
	// SharedKeys is the number of distinct keys found in more than one
	// partition.
	SharedKeys int `json:"shared_keys"`

	// RecordsSharingTrainKeys is the number of validation and test records
	// whose key also appears in the train partition.
	RecordsSharingTrainKeys int `json:"records_sharing_train_keys"`
}

// PartitionReport describes how a dataset was partitioned.
type PartitionReport struct {
	TrainEnd      time.Time       `json:"train_end"`
	ValidationEnd time.Time       `json:"validation_end"`
	Train         PartitionCounts `json:"train"`
	Validation    PartitionCounts `json:"validation"`
	Test          PartitionCounts `json:"test"`
	GroupOverlap  GroupOverlap    `json:"group_overlap"`
}

// Partitioner assigns records to partitions and counts them. Each record
// that is not invalid belongs to exactly one partition.
type Partitioner struct {
	boundaries Boundaries
	counts     map[Partition]*PartitionCounts
	keys       map[string]*[3]int
}

// NewPartitioner validates boundaries: both must be set, and TrainEnd must
// be earlier than ValidationEnd.
func NewPartitioner(boundaries Boundaries) (*Partitioner, error) {
	switch {
	case boundaries.TrainEnd.IsZero() || boundaries.ValidationEnd.IsZero():
		return nil, errors.New("train_end and validation_end are required")
	case !boundaries.TrainEnd.Before(boundaries.ValidationEnd):
		return nil, errors.New("train_end must be earlier than validation_end")
	}

	return &Partitioner{
		boundaries: boundaries,
		counts: map[Partition]*PartitionCounts{
			Train:      {},
			Validation: {},
			Test:       {},
		},
		keys: map[string]*[3]int{},
	}, nil
}

// Add assigns a record to its partition and counts it. Invalid records have
// no reliable decision time, so they are not partitioned and Add returns
// false.
func (p *Partitioner) Add(record trainingdata.Record) (Partition, bool) {
	if record.Category == trainingdata.Invalid {
		return "", false
	}

	partition := p.boundaries.PartitionOf(record.Example.ScoringInput.DecisionTime)
	counts := p.counts[partition]

	switch record.Category {
	case trainingdata.Insufficient:
		counts.Insufficient++
	case trainingdata.Immature:
		counts.Immature++
	case trainingdata.Usable:
		if record.Example.Outcome.Label == trainingdata.Fraud {
			counts.Fraud++
		} else {
			counts.Legitimate++
		}

		if key := record.Example.GroupKey; key != nil {
			if p.keys[*key] == nil {
				p.keys[*key] = &[3]int{}
			}
			p.keys[*key][partitionIndex(partition)]++
		}
	}

	return partition, true
}

// Report returns the partition counts and the group overlap.
func (p *Partitioner) Report() PartitionReport {
	report := PartitionReport{
		TrainEnd:      p.boundaries.TrainEnd,
		ValidationEnd: p.boundaries.ValidationEnd,
		Train:         *p.counts[Train],
		Validation:    *p.counts[Validation],
		Test:          *p.counts[Test],
	}

	for _, counts := range p.keys {
		partitions := 0
		for _, count := range counts {
			if count > 0 {
				partitions++
			}
		}

		if partitions > 1 {
			report.GroupOverlap.SharedKeys++
		}

		if counts[partitionIndex(Train)] > 0 {
			report.GroupOverlap.RecordsSharingTrainKeys +=
				counts[partitionIndex(Validation)] + counts[partitionIndex(Test)]
		}
	}

	return report
}

func partitionIndex(partition Partition) int {
	switch partition {
	case Train:
		return 0
	case Validation:
		return 1
	default:
		return 2
	}
}
