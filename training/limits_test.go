package training

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"

	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

func limits(maxRecords int, maxWork int64) Limits {
	return Limits{MaxRecords: maxRecords, MaxTrainingWork: maxWork}
}

// A dataset with more records than the limit is refused while it is read.
func TestRunRefusesDatasetsAboveTheRecordLimit(t *testing.T) {
	fixture := read(t, pixRiskFixture)

	_, err := Run(context.Background(), bytes.NewReader(fixture), config(), limits(7, DefaultMaxTrainingWork), nil)
	var limit *trainingdata.RecordLimitError
	if !errors.As(err, &limit) || limit.Limit != 7 {
		t.Fatalf("Run() error = %v, want a *trainingdata.RecordLimitError with limit 7", err)
	}

	if _, err := Run(context.Background(), bytes.NewReader(fixture), config(), limits(8, DefaultMaxTrainingWork), nil); err != nil {
		t.Errorf("at the limit: Run() error = %v", err)
	}
}

// Training work is parameters × (train records × max_epochs + validation
// records × validation evaluations), and a run whose work exceeds the
// limit is refused before training.
func TestRunRefusesTrainingWorkAboveTheLimit(t *testing.T) {
	best := func(epochs int) trainingconfig.Config {
		cfg := selectionConfig()
		cfg.MaxEpochs = epochs
		cfg.Checkpoint = bestValidation(trainingconfig.ObjectiveValidationLoss, 10)
		return cfg
	}

	// [62, 4, 1] has 62·4 + 4 + 4·1 + 1 = 257 parameters. pixRiskFixture
	// has 4 usable train records; selectionFixture has 6, and 4 usable
	// validation records.
	cases := []struct {
		name    string
		fixture string
		config  trainingconfig.Config
		work    int64
	}{
		{"final epoch", pixRiskFixture, config(), 257 * 4 * 2000},
		{"best validation", selectionFixture, best(300), 257 * (6*300 + 4*30)},
		{"best validation with a final evaluation", selectionFixture, best(305), 257 * (6*305 + 4*31)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := read(t, c.fixture)

			epochs := 0
			progress := func(int, float64) { epochs++ }

			_, err := Run(context.Background(), bytes.NewReader(data), c.config, limits(100, c.work-1), progress)
			var limit *WorkLimitError
			if !errors.As(err, &limit) || limit.Work != uint64(c.work) || limit.Limit != c.work-1 {
				t.Fatalf("Run() error = %v, want a *WorkLimitError with work %d", err, c.work)
			}
			if epochs != 0 {
				t.Errorf("trained %d epochs before refusing the work", epochs)
			}

			if _, err := Run(context.Background(), bytes.NewReader(data), c.config, limits(100, c.work), nil); err != nil {
				t.Errorf("at the limit: Run() error = %v", err)
			}
		})
	}
}

// Work that does not fit in 64 bits saturates instead of wrapping around.
func TestTrainingWorkSaturates(t *testing.T) {
	if got := trainingWork(213_761, 1<<40, 1<<20, 1_000_000, 100_000); got != math.MaxUint64 {
		t.Errorf("trainingWork() = %d, want %d", got, uint64(math.MaxUint64))
	}
	if got := trainingWork(257, 6, 4, 305, 31); got != 257*(6*305+4*31) {
		t.Errorf("trainingWork() = %d", got)
	}
}

// Limits must be positive, and they are checked before reading.
func TestRunRejectsInvalidLimitsBeforeReading(t *testing.T) {
	for name, l := range map[string]Limits{
		"no records": limits(0, DefaultMaxTrainingWork),
		"no work":    limits(DefaultLimits.MaxRecords, 0),
	} {
		if _, err := Run(context.Background(), failingReader{t}, config(), l, nil); err == nil {
			t.Errorf("%s: Run() error = nil", name)
		}
	}

	if DefaultLimits != limits(trainingdata.DefaultMaxRecords, DefaultMaxTrainingWork) {
		t.Errorf("DefaultLimits = %+v", DefaultLimits)
	}
}
