package training

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// pixRiskFixture holds 8 pix-training-example-v2 records: four usable
// train records (lines 1, 2, 4, and 5) around an insufficient one (line
// 3), one validation record (line 6), and a usable and an immature test
// record (lines 7 and 8).
const pixRiskFixture = "testdata/pix-risk-v1.jsonl"

var pixRiskBoundaries = evaluation.Boundaries{
	TrainEnd:      time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
	ValidationEnd: time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC),
}

func prepareDataset(t *testing.T, data []byte) PreparedDataset {
	t.Helper()

	dataset, err := PrepareDataset(context.Background(), bytes.NewReader(data), pixRiskBoundaries, DefaultLimits.MaxRecords)
	if err != nil {
		t.Fatalf("PrepareDataset() error = %v", err)
	}

	return dataset
}

func lines(examples []PreparedExample) []int {
	result := []int{}
	for _, example := range examples {
		result = append(result, example.Line)
	}
	return result
}

func TestPrepareDatasetPartitionsUsableRecords(t *testing.T) {
	dataset := prepareDataset(t, read(t, pixRiskFixture))

	if got := [][]int{lines(dataset.Train), lines(dataset.Validation), lines(dataset.Test)}; !reflect.DeepEqual(got, [][]int{{1, 2, 4, 5}, {6}, {7}}) {
		t.Errorf("partition lines = %v", got)
	}

	wantRecords := trainingdata.Counts{Total: 8, Usable: 6, Insufficient: 1, Immature: 1}
	if dataset.Dataset.Records != wantRecords || dataset.Dataset.FeatureSchema != pixrisk.Schema ||
		dataset.Dataset.TrainingExampleVersion != trainingdata.TrainingExampleV2 {
		t.Errorf("dataset report = %+v", dataset.Dataset)
	}

	if dataset.Partitions.Train.Insufficient != 1 || dataset.Partitions.Test.Immature != 1 {
		t.Errorf("partition counts = %+v", dataset.Partitions)
	}

	fraud := dataset.Train[3]
	if fraud.Label != trainingdata.Fraud || fraud.AmountCents != 180000 || *fraud.LossAmountCents != 120000 {
		t.Errorf("line 5 = %+v", fraud)
	}

	if loss := dataset.Test[0].LossAmountCents; loss != nil {
		t.Errorf("unknown loss = %v, want nil", *loss)
	}
}

// Values of validation and test records never reach the fitted state.
func TestPrepareDatasetFitsOnTheTrainPartitionOnly(t *testing.T) {
	data := read(t, pixRiskFixture)
	baseline := prepareDataset(t, data).State

	changed := data
	for _, line := range []int{6, 7} {
		changed = replaceLine(t, changed, line, `"initiated_outgoing_pix_24h":{"count":9`, `"initiated_outgoing_pix_24h":{"count":900`)
		changed = replaceLine(t, changed, line, `"spi":{"settlements":{"d90":12`, `"spi":{"settlements":{"d90":12000`)
	}
	changed = replaceLine(t, changed, 8, `"amount_cents":250000`, `"amount_cents":990000`)

	if got := prepareDataset(t, changed).State; !reflect.DeepEqual(got, baseline) {
		t.Error("changing validation and test records changed the fitted state")
	}

	changedTrain := replaceLine(t, data, 4, `"amount_cents":15000`, `"amount_cents":990000`)
	if got := prepareDataset(t, changedTrain).State; reflect.DeepEqual(got, baseline) {
		t.Error("changing a train record did not change the fitted state")
	}
}

// Training and scoring produce the same vector for the same input and the
// same frozen state.
func TestPrepareDatasetMatchesScoringPreparation(t *testing.T) {
	data := read(t, pixRiskFixture)
	dataset := prepareDataset(t, data)
	records := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")

	examples := append(append(append([]PreparedExample{}, dataset.Train...), dataset.Validation...), dataset.Test...)
	for _, example := range examples {
		var record struct {
			ScoringInput json.RawMessage `json:"scoring_input"`
		}
		if err := json.Unmarshal([]byte(records[example.Line-1]), &record); err != nil {
			t.Fatal(err)
		}

		input, err := pixinput.Decode(record.ScoringInput, pixinput.ContractV2)
		if err != nil {
			t.Fatal(err)
		}

		prepared, err := pixrisk.Prepare(input)
		if err != nil {
			t.Fatal(err)
		}

		values, err := dataset.State.Apply(prepared)
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(values, example.Values) || prepared.Values != example.Prepared.Values {
			t.Errorf("line %d: training and scoring vectors differ", example.Line)
		}

		if len(example.Values) != pixrisk.FeatureCount {
			t.Errorf("line %d has %d values", example.Line, len(example.Values))
		}
	}
}

func TestPrepareDatasetStopsOnInvalidRecords(t *testing.T) {
	_, err := PrepareDataset(context.Background(), bytes.NewReader(read(t, "../docs/contracts/training-examples-v2/dataset.jsonl")), pixRiskBoundaries, DefaultLimits.MaxRecords)

	var invalid *InvalidDatasetError
	if !errors.As(err, &invalid) {
		t.Fatalf("PrepareDataset() error = %v, want an InvalidDatasetError", err)
	}

	if invalid.Report.Records.Invalid != 2 || len(invalid.Diagnostics) != 2 {
		t.Errorf("report = %+v, diagnostics = %+v", invalid.Report.Records, invalid.Diagnostics)
	}
}

func TestPrepareDatasetStopsWhenAFeatureIsNeverKnownInTrain(t *testing.T) {
	examples := "../docs/contracts/training-examples-v2/"

	// Both train records have no payer device and no period limit.
	legitimate := read(t, examples+"06-legitimate-no-device-no-period-limit.jsonl")
	fraud := strings.NewReplacer(
		"2026-05-25", "2026-05-26",
		`"label":"legitimate"`, `"label":"fraud"`,
	).Replace(string(legitimate))

	data := append(append(legitimate, fraud...),
		read(t, examples+"03-fraud-registered-device-pre-registered-recipient.jsonl")...)

	_, err := PrepareDataset(context.Background(), bytes.NewReader(data), pixRiskBoundaries, DefaultLimits.MaxRecords)

	var never *pixrisk.FeatureNeverKnownError
	if !errors.As(err, &never) {
		t.Fatalf("PrepareDataset() error = %v, want a FeatureNeverKnownError", err)
	}

	if want := []string{"limit_utilization", "device_settled_count_90d"}; !reflect.DeepEqual(never.Features, want) {
		t.Errorf("features = %v, want %v", never.Features, want)
	}
}

func TestPrepareDatasetValidatesBoundariesBeforeReading(t *testing.T) {
	reversed := evaluation.Boundaries{TrainEnd: pixRiskBoundaries.ValidationEnd, ValidationEnd: pixRiskBoundaries.TrainEnd}

	if _, err := PrepareDataset(context.Background(), failingReader{t}, reversed, DefaultLimits.MaxRecords); err == nil {
		t.Error("PrepareDataset() error = nil, want an error")
	}
}

// Records of another training example version are invalid.
func TestPrepareDatasetRejectsVersion1Records(t *testing.T) {
	v1 := read(t, "../docs/contracts/training-examples/dataset.jsonl")
	_, err := PrepareDataset(context.Background(), bytes.NewReader(v1), pixRiskBoundaries, DefaultLimits.MaxRecords)

	var invalid *InvalidDatasetError
	if !errors.As(err, &invalid) || invalid.Report.Records.Invalid != 7 {
		t.Errorf("PrepareDataset() error = %v, want every record invalid", err)
	}
}
