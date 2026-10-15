package synthetic_test

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/internal/synthetic"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

func generate(t *testing.T, options synthetic.Options) []byte {
	t.Helper()

	var buffer bytes.Buffer
	if err := synthetic.Generate(&buffer, options); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	return buffer.Bytes()
}

// scanned classifies a generated dataset and prepares its usable records.
type scanned struct {
	summary  trainingdata.Summary
	prepared []pixrisk.Prepared
	frauds   []trainingdata.Example
	others   []trainingdata.Example
	blockers map[string]int
	ordered  bool
}

func scan(t *testing.T, data []byte) scanned {
	t.Helper()

	result := scanned{blockers: map[string]int{}, ordered: true}
	var previous time.Time

	summary, err := trainingdata.Scan(bytes.NewReader(data), trainingdata.PixRiskV1, trainingdata.DefaultMaxRecords, func(record trainingdata.Record) error {
		if record.Category == trainingdata.Invalid {
			return errors.New(record.Diagnostics[0].Path + ": " + record.Diagnostics[0].Code + ": " + record.Diagnostics[0].Message)
		}

		decision := record.Example.ScoringInput.DecisionTime
		if decision.Before(previous) {
			result.ordered = false
		}
		previous = decision

		for _, blocker := range record.Blockers {
			result.blockers[blocker.Reason]++
		}
		if record.Category != trainingdata.Usable {
			return nil
		}

		prepared, err := pixrisk.Prepare(record.Example.ScoringInput)
		if err != nil {
			return err
		}
		result.prepared = append(result.prepared, prepared)

		if record.Example.Outcome.Label == trainingdata.Fraud {
			result.frauds = append(result.frauds, record.Example)
		} else {
			result.others = append(result.others, record.Example)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	result.summary = summary
	return result
}

// Every generated record is valid, in decision time order, and the
// dataset mixes usable, insufficient, and immature records.
func TestGenerateWritesValidRecords(t *testing.T) {
	options := synthetic.DefaultOptions(5000, 1)
	s := scan(t, generate(t, options))

	records := s.summary.Records
	if records.Total != 5000 || records.Invalid != 0 {
		t.Fatalf("records = %+v, want 5000 valid records", records)
	}
	if !s.ordered {
		t.Error("records are not in decision time order")
	}

	// 2% insufficient; records decided after 2026-06-02 are immature.
	if records.Insufficient < 50 || records.Insufficient > 150 {
		t.Errorf("insufficient = %d, want about 2%%", records.Insufficient)
	}
	if records.Immature < 200 || records.Immature > 350 {
		t.Errorf("immature = %d, want about 28 of 547 days", records.Immature)
	}
	if s.blockers[pixrisk.ReasonMissing] == 0 || s.blockers[pixrisk.ReasonStale] == 0 {
		t.Errorf("blockers = %v, want missing and stale required facts", s.blockers)
	}
}

// The same options give the same bytes, and another seed other bytes.
func TestGenerateIsDeterministic(t *testing.T) {
	options := synthetic.DefaultOptions(300, 7)

	first, again := generate(t, options), generate(t, options)
	if !bytes.Equal(first, again) {
		t.Error("the same options generated different datasets")
	}

	options.Seed = 8
	if bytes.Equal(first, generate(t, options)) {
		t.Error("another seed generated the same dataset")
	}
}

// Fraud is as rare as configured, and it shifts the amount, the payer
// device history, and the relationship with the recipient.
func TestGenerateSkewsLabelsAndFeatures(t *testing.T) {
	options := synthetic.DefaultOptions(20000, 3)
	options.FraudRate = 0.02
	s := scan(t, generate(t, options))

	usable := s.summary.UsableLabels
	share := float64(usable.Fraud) / float64(usable.Fraud+usable.Legitimate)
	if share < 0.015 || share > 0.025 {
		t.Errorf("fraud share = %.4f, want about 0.02", share)
	}

	median := func(examples []trainingdata.Example) float64 {
		total := 0.0
		for _, e := range examples {
			total += math.Log(float64(e.ScoringInput.PixPayment.AmountCents))
		}
		return math.Exp(total / float64(len(examples)))
	}
	if fraud, legitimate := median(s.frauds), median(s.others); fraud < 4*legitimate {
		t.Errorf("geometric mean amount: fraud %.0f, legitimate %.0f; want fraud several times larger", fraud, legitimate)
	}

	newRecipient := func(examples []trainingdata.Example) float64 {
		count := 0
		for _, e := range examples {
			if e.ScoringInput.Observations.RecipientPerson.SettledPixFromPayerAccount365d.Count == 0 {
				count++
			}
		}
		return float64(count) / float64(len(examples))
	}
	if fraud, legitimate := newRecipient(s.frauds), newRecipient(s.others); fraud < legitimate+0.3 {
		t.Errorf("share paying a new recipient: fraud %.2f, legitimate %.2f", fraud, legitimate)
	}

	unknown := 0
	for _, e := range s.frauds {
		if e.Outcome.LossAmountCents == nil {
			unknown++
		}
	}
	if unknown == 0 || unknown == len(s.frauds) {
		t.Errorf("%d of %d frauds have an unknown loss, want some", unknown, len(s.frauds))
	}
}

// Every supported population and every availability state of every
// optional source occurs.
func TestGenerateCoversAbsencePatterns(t *testing.T) {
	s := scan(t, generate(t, synthetic.DefaultOptions(20000, 5)))
	coverage := trainingreport.NewPartitionCoverage(s.prepared)

	for population, count := range coverage.Populations {
		if count == 0 {
			t.Errorf("no record in population %s", population)
		}
	}
	for period, count := range coverage.LimitPeriods {
		if count == 0 {
			t.Errorf("no record with limit period %s", period)
		}
	}
	for source, states := range coverage.Sources {
		for state, count := range states {
			if count == 0 {
				t.Errorf("no record with %s %s", source, state)
			}
		}
	}

	statuses := map[string]bool{}
	for _, p := range s.prepared {
		for _, note := range p.Notes {
			statuses[note.Status] = true
		}
	}
	for _, status := range []string{"missing", "stale", "not_applicable", "no_history", "zero_limit", "unrecognized"} {
		if !statuses[status] {
			t.Errorf("no preparation note %s", status)
		}
	}
}

func TestGenerateRejectsInvalidOptions(t *testing.T) {
	cases := map[string]func(*synthetic.Options){
		"no records":        func(o *synthetic.Options) { o.Records = 0 },
		"no fraud":          func(o *synthetic.Options) { o.FraudRate = 0 },
		"only fraud":        func(o *synthetic.Options) { o.FraudRate = 1 },
		"reversed range":    func(o *synthetic.Options) { o.Start = o.End },
		"labels before end": func(o *synthetic.Options) { o.LabelAsOf = o.End.Add(-time.Hour) },
		"no accounts":       func(o *synthetic.Options) { o.Accounts = 0 },
		"not a number":      func(o *synthetic.Options) { o.FraudRate = math.NaN() },
	}

	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			options := synthetic.DefaultOptions(10, 1)
			change(&options)

			var buffer bytes.Buffer
			err := synthetic.Generate(&buffer, options)
			if err == nil || !strings.HasPrefix(err.Error(), "synthetic dataset:") || buffer.Len() != 0 {
				t.Errorf("Generate() error = %v, wrote %d bytes", err, buffer.Len())
			}
		})
	}
}
