package modelinspection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/evaluation"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingdata"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

const (
	// selectionFixture has six train records (lines 1–6), two legitimate
	// and two fraud validation records (lines 7–10), and two legitimate
	// test records and a test fraud with an unknown loss (lines 11–13),
	// with the boundaries of selectionConfig.
	selectionFixture = "../training/testdata/pix-risk-v1-selection.jsonl"
	invalidDataset   = "../docs/contracts/training-examples-v2/dataset.jsonl"
	baseline         = "../docs/contracts/training-config-examples/01-baseline.json"
	specification    = "../docs/features/pix-risk-v1.md"
)

var software = modelartifact.Software{Module: "github.com/digocelos/transaction-risk-go", Version: "(devel)", Revision: "0123abc"}

func read(t testing.TB, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// selectionConfig restores the best validation loss and selects the
// threshold on validation, with the boundaries of the selection fixture.
func selectionConfig(t testing.TB) trainingconfig.Config {
	t.Helper()

	cfg, err := trainingconfig.Decode(read(t, baseline))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs = 300
	cfg.TrainEnd = time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	cfg.ValidationEnd = time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	cfg.Checkpoint = trainingconfig.Checkpoint{
		Policy:             trainingconfig.CheckpointBestValidation,
		Objective:          trainingconfig.ObjectiveValidationLoss,
		EvaluationInterval: 10,
	}
	cfg.Threshold = trainingconfig.Threshold{
		Policy:               trainingconfig.ThresholdMaxRecallAtMaxFalsePositiveRate,
		MaxFalsePositiveRate: 0.5,
	}
	return cfg
}

// trainModel trains a model on a dataset and returns the bytes of its
// artifact and the training result.
func trainModel(t testing.TB, dataset []byte, cfg trainingconfig.Config) ([]byte, training.Result) {
	t.Helper()

	result, err := training.Run(context.Background(), bytes.NewReader(dataset), cfg, training.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}

	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := modelartifact.FromTraining(result.Network, result.State, result.Report, digest(report), software)
	if err != nil {
		t.Fatal(err)
	}

	model, err := modelartifact.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}

	return model, result
}

func inspect(t testing.TB, model []byte, cfg trainingconfig.Config, dataset []byte) Report {
	t.Helper()

	report, err := Inspect(context.Background(), model, cfg, bytes.NewReader(dataset), Options{MaxRecords: trainingdata.DefaultMaxRecords})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	return report
}

func featureIndex(t testing.TB, name string) int {
	t.Helper()

	for i, feature := range pixrisk.Descriptor() {
		if feature.Name == name {
			return i
		}
	}
	t.Fatalf("no feature %s", name)
	return -1
}

// The inspection identifies the model and the dataset, reproduces the
// validation metrics of the training report at baseline, scores with the
// arithmetic of the network, and lists exactly the inputs no train record
// exercised.
func TestInspectMatchesScoringAndTraining(t *testing.T) {
	dataset := read(t, selectionFixture)
	cfg := selectionConfig(t)
	model, result := trainModel(t, dataset, cfg)

	report := inspect(t, model, cfg, dataset)

	if report.Model.ArtifactSHA256 != digest(model) || report.Dataset.DatasetSHA256 != digest(dataset) {
		t.Errorf("identities = %s, %s", report.Model.ArtifactSHA256, report.Dataset.DatasetSHA256)
	}
	if !reflect.DeepEqual(report.Parameters.Sizes, cfg.Sizes()) || report.Parameters.Seed != cfg.Seed {
		t.Errorf("parameters = %v seed %d", report.Parameters.Sizes, report.Parameters.Seed)
	}
	if !reflect.DeepEqual(report.Partitions, result.Report.Partitions) {
		t.Errorf("partitions = %+v, want the training report's", report.Partitions)
	}

	validation := result.Report.Validation
	baseline := report.Missingness.Baseline
	if *baseline.Recall != *validation.Recall || baseline.FalsePositives != validation.FalsePositives ||
		*baseline.PRAUC != *validation.PRAUC || *baseline.FalsePositiveRate != *validation.FalsePositiveRate {
		t.Errorf("baseline = %+v, want the training report's validation metrics %+v", baseline, validation)
	}
	if report.Missingness.Threshold != result.Report.Selection.Threshold.Value {
		t.Errorf("threshold = %v", report.Missingness.Threshold)
	}

	// The forward pass is the network's.
	prepared, err := training.PrepareDataset(context.Background(), bytes.NewReader(dataset), cfg.Boundaries(), trainingdata.DefaultMaxRecords)
	if err != nil {
		t.Fatal(err)
	}
	layers := result.Network.Layers()
	for _, example := range slices.Concat(prepared.Train, prepared.Validation, prepared.Test) {
		want, err := result.Network.Predict(example.Values)
		if err != nil {
			t.Fatal(err)
		}
		if got := score(layers, example.Values); got != want[0] {
			t.Errorf("line %d: score %v, want Predict's %v", example.Line, got, want[0])
		}
	}

	// Unexercised inputs are exactly the features that are 0 in every
	// train record, and only their columns kept their initialization.
	var want []string
	for j, feature := range pixrisk.Descriptor() {
		exercised := false
		for _, example := range prepared.Train {
			exercised = exercised || example.Values[j] != 0
		}
		if !exercised {
			want = append(want, feature.Name)
		}
		input := report.Parameters.Inputs[j]
		if input.Name != feature.Name || input.ExercisedInTrain != exercised || (input.ChangeNorm == 0) != !exercised {
			t.Errorf("input %s = %+v, exercised %v", feature.Name, input, exercised)
		}
		if input.ColumnNorm <= 0 || input.MaxAbsChange > input.ChangeNorm {
			t.Errorf("input %s = %+v", feature.Name, input)
		}
	}
	if len(want) == 0 || !reflect.DeepEqual(report.UnexercisedFeatures, want) {
		t.Errorf("unexercised = %v, want %v", report.UnexercisedFeatures, want)
	}

	// Hidden layers follow the architecture, with activations in range.
	if len(report.Parameters.HiddenLayers) != len(cfg.HiddenLayers) {
		t.Fatalf("%d hidden layers, want %d", len(report.Parameters.HiddenLayers), len(cfg.HiddenLayers))
	}
	for i, layer := range report.Parameters.HiddenLayers {
		if len(layer.Neurons) != cfg.HiddenLayers[i] || (layer.MaxAbsCosine == nil) != (cfg.HiddenLayers[i] < 2) {
			t.Errorf("hidden layer %d = %+v", i, layer)
		}
		for _, neuron := range layer.Neurons {
			a := neuron.Activation
			if a.Mean < 0 || a.Mean > 1 || a.Below0_01 < 0 || a.Below0_01 > 1 || a.Above0_99 < 0 || a.Above0_99 > 1 ||
				neuron.InputNorm <= 0 || neuron.OutputNorm <= 0 {
				t.Errorf("neuron = %+v", neuron)
			}
		}
	}
	if len(report.Parameters.Output.Weights) != cfg.HiddenLayers[len(cfg.HiddenLayers)-1] {
		t.Errorf("output = %+v", report.Parameters.Output)
	}

	// Partial dependence covers every standardized feature on a 12-point
	// grid, with both labels present in the validation partition.
	pd := report.PartialDependence
	if pd.Partition != evaluation.Validation || pd.Records != (trainingdata.LabelCounts{Fraud: 2, Legitimate: 2}) || pd.Tolerance != Tolerance {
		t.Errorf("partial dependence = %+v", pd.Records)
	}
	standardized := 0
	for _, feature := range pixrisk.Descriptor() {
		if feature.Kind == pixrisk.Standardized {
			standardized++
		}
	}
	if len(pd.Features) != standardized {
		t.Fatalf("%d features, want %d", len(pd.Features), standardized)
	}
	for _, feature := range pd.Features {
		direction, ok := Direction(feature.Name)
		if !ok || feature.ExpectedDirection != direction || len(feature.Grid) != 12 || feature.TrainRange.Min > feature.TrainRange.Max {
			t.Errorf("feature %s = %+v", feature.Name, feature)
		}
		for g, point := range feature.Grid {
			if g > 0 && point.Value <= feature.Grid[g-1].Value {
				t.Errorf("%s: the grid is not increasing at %v", feature.Name, point.Value)
			}
			for _, scores := range []*LabelScores{point.Fraud, point.Legitimate} {
				if scores == nil || scores.MeanScore < 0 || scores.MeanScore > 1 || scores.FlaggedShare < 0 || scores.FlaggedShare > 1 {
					t.Errorf("%s at %v: %+v", feature.Name, point.Value, scores)
				}
			}
		}
		if feature.Violations == nil {
			t.Errorf("%s: violations must be a list", feature.Name)
		}
	}

	// Probes come in report order, and the populations mask what the
	// preparation would.
	var names []string
	for _, probe := range report.Missingness.Probes {
		names = append(names, probe.Name)
	}
	wantNames := append(pixrisk.Sources(), ProbeDictStatistics, pixrisk.PopulationNoPixKey, pixrisk.PopulationNoPayerDevice)
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("probes = %v, want %v", names, wantNames)
	}
	keyless := report.Missingness.Probes[len(report.Missingness.Probes)-2]
	if !reflect.DeepEqual(keyless.Set, []string{"no_pix_key"}) ||
		!slices.Contains(keyless.Masked, "key_ownership_age") || !slices.Contains(keyless.Masked, "key_distinct_accounts_m12") ||
		slices.Contains(keyless.Masked, "person_fraud_markers_d90") {
		t.Errorf("no_pix_key probe = %+v", keyless)
	}
	for _, probe := range report.Missingness.Probes {
		if probe.FraudsEvading < 0 || probe.FraudsEvading > 2 || probe.LegitimateNewlyFlagged < 0 || probe.LegitimateNewlyFlagged > 2 ||
			probe.Result.MeanScore.Fraud == nil || probe.MeanScoreChange.Legitimate == nil {
			t.Errorf("probe %s = %+v", probe.Name, probe)
		}
	}
}

// The probes mask whole sources as pixrisk represents them, and the
// populations add their discriminator.
func TestProbesMaskWholeSources(t *testing.T) {
	list := probes()
	if len(list) != len(pixrisk.Sources())+3 {
		t.Fatalf("%d probes", len(list))
	}

	for i, name := range pixrisk.Sources() {
		if list[i].name != name || list[i].kind != ProbeSource || len(list[i].set) != 0 ||
			!reflect.DeepEqual(list[i].masked, pixrisk.SourceFeatures(name)) {
			t.Errorf("probe %d = %+v, want source %s", i, list[i], name)
		}
	}

	dict := list[len(pixrisk.Sources())]
	keyless := list[len(list)-2]
	deviceless := list[len(list)-1]
	if dict.name != ProbeDictStatistics || dict.kind != ProbeSources || len(dict.masked) != 28 {
		t.Errorf("dict probe = %+v", dict)
	}
	if keyless.name != pixrisk.PopulationNoPixKey || !reflect.DeepEqual(keyless.set, []int{indexNoPixKey}) ||
		len(keyless.masked) != 9+14 {
		t.Errorf("no_pix_key probe = %+v", keyless)
	}
	if deviceless.name != pixrisk.PopulationNoPayerDevice || !reflect.DeepEqual(deviceless.set, []int{indexNoPayerDevice}) ||
		len(deviceless.masked) != 4 {
		t.Errorf("no_payer_device probe = %+v", deviceless)
	}

	values := make([]float64, pixrisk.FeatureCount)
	for i := range values {
		values[i] = 0.5
	}
	applied := keyless.apply(values)
	if applied[indexNoPixKey] != 1 || applied[featureIndex(t, "key_ownership_age")] != 0 ||
		applied[featureIndex(t, "person_fraud_markers_d90")] != 0.5 || values[indexNoPixKey] != 0.5 {
		t.Errorf("apply = %v", applied)
	}
}

// Inspection refuses another configuration, another dataset, an invalid
// dataset, a damaged artifact, and a canceled context, and reports why.
func TestInspectRejectsOtherInputs(t *testing.T) {
	dataset := read(t, selectionFixture)
	cfg := selectionConfig(t)
	model, _ := trainModel(t, dataset, cfg)

	otherConfig := cfg
	otherConfig.Seed++
	if _, err := Inspect(context.Background(), model, otherConfig, bytes.NewReader(dataset), Options{MaxRecords: trainingdata.DefaultMaxRecords}); err == nil || !strings.Contains(err.Error(), "not the model's configuration") {
		t.Errorf("another configuration: error = %v", err)
	}

	lines := bytes.Split(bytes.TrimSuffix(dataset, []byte("\n")), []byte("\n"))
	other := slices.Concat(dataset, lines[len(lines)-1], []byte("\n"))
	if _, err := Inspect(context.Background(), model, cfg, bytes.NewReader(other), Options{MaxRecords: trainingdata.DefaultMaxRecords}); err == nil || !strings.Contains(err.Error(), "not the model's training dataset") {
		t.Errorf("another dataset: error = %v", err)
	}

	var invalid *training.InvalidDatasetError
	if _, err := Inspect(context.Background(), model, cfg, bytes.NewReader(read(t, invalidDataset)), Options{MaxRecords: trainingdata.DefaultMaxRecords}); !errors.As(err, &invalid) || invalid.Report.Records.Invalid != 2 {
		t.Errorf("invalid dataset: error = %v", err)
	}

	damaged := bytes.Replace(model, []byte(`"artifact_version": "pix-risk-model-v1"`), []byte(`"artifact_version": "pix-risk-model-v0"`), 1)
	if _, err := Inspect(context.Background(), damaged, cfg, bytes.NewReader(dataset), Options{MaxRecords: trainingdata.DefaultMaxRecords}); err == nil || !strings.Contains(err.Error(), "unsupported model artifact version") {
		t.Errorf("damaged artifact: error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Inspect(ctx, model, cfg, bytes.NewReader(dataset), Options{MaxRecords: trainingdata.DefaultMaxRecords}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: error = %v", err)
	}
}

// The same report always encodes to the same bytes, and the encoder
// refuses reports that contradict themselves.
func TestMarshalIsDeterministicAndRefusesInconsistentReports(t *testing.T) {
	dataset := read(t, selectionFixture)
	cfg := selectionConfig(t)
	model, _ := trainModel(t, dataset, cfg)
	report := inspect(t, model, cfg, dataset)

	first, err := Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.HasSuffix(first, []byte("}\n")) ||
		!bytes.HasPrefix(first, []byte("{\n  \"inspection_report_version\": \"pix-model-inspection-v1\",")) {
		t.Errorf("encoding is not deterministic or not the documented layout")
	}

	again := inspect(t, model, cfg, dataset)
	if third, _ := Marshal(again); !bytes.Equal(first, third) {
		t.Error("two inspections of the same run encode differently")
	}

	cases := map[string]func(*Report){
		"tolerance":       func(r *Report) { r.PartialDependence.Tolerance = 0.5 },
		"threshold":       func(r *Report) { r.Missingness.Threshold = 0.3 },
		"unexercised":     func(r *Report) { r.UnexercisedFeatures = []string{} },
		"probes":          func(r *Report) { r.Missingness.Probes = r.Missingness.Probes[:3] },
		"input name":      func(r *Report) { r.Parameters.Inputs[0].Name = "other" },
		"dataset":         func(r *Report) { r.Dataset.DatasetSHA256 = strings.Repeat("0", 64) },
		"partition":       func(r *Report) { r.PartialDependence.Partition = evaluation.Test },
		"boundaries":      func(r *Report) { r.Partitions.TrainEnd = r.Partitions.TrainEnd.Add(time.Hour) },
		"records":         func(r *Report) { r.PartialDependence.Records.Fraud++ },
		"standardized":    func(r *Report) { r.PartialDependence.Features = r.PartialDependence.Features[1:] },
		"hidden neurons":  func(r *Report) { r.Parameters.HiddenLayers[0].Neurons = r.Parameters.HiddenLayers[0].Neurons[1:] },
		"exercised":       func(r *Report) { r.Parameters.Inputs[0].ExercisedInTrain = false },
		"invalid records": func(r *Report) { r.Dataset.Records.Invalid = 1 },
		"probe name":      func(r *Report) { r.Missingness.Probes[0].Name = "other" },
		"missing cosine":  func(r *Report) { r.Parameters.HiddenLayers[0].MaxAbsCosine = nil },
		"output weights":  func(r *Report) { r.Parameters.Output.Weights = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			// A deep copy of the slices the mutation touches.
			r := inspect(t, model, cfg, dataset)
			mutate(&r)
			if _, err := Marshal(r); err == nil || !strings.HasPrefix(err.Error(), "invalid inspection report: ") {
				t.Errorf("Marshal() error = %v, want a refusal", err)
			}
		})
	}
}

// A move against the expected direction by more than the tolerance is a
// violation; smaller moves, moves along the direction, and features
// without a direction are not.
func TestViolationsFollowTheExpectedDirection(t *testing.T) {
	grid := func(fraud, legitimate []float64, inRange []bool) []GridPoint {
		points := make([]GridPoint, len(fraud))
		for i := range fraud {
			points[i] = GridPoint{
				Value:        float64(i),
				InTrainRange: inRange[i],
				Fraud:        &LabelScores{MeanScore: fraud[i]},
				Legitimate:   &LabelScores{MeanScore: legitimate[i]},
			}
		}
		return points
	}

	points := grid(
		[]float64{0.5, 0.6, 0.6005, 0.4},
		[]float64{0.1, 0.05, 0.2, 0.2},
		[]bool{true, true, false, false},
	)

	got := violations(RiskIncreasing, points)
	want := []Violation{
		{Label: trainingdata.Fraud, From: 2, To: 3, InTrainRange: false, MeanScoreFrom: 0.6005, MeanScoreTo: 0.4},
		{Label: trainingdata.Legitimate, From: 0, To: 1, InTrainRange: true, MeanScoreFrom: 0.1, MeanScoreTo: 0.05},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("risk_increasing violations = %+v, want %+v", got, want)
	}

	got = violations(RiskDecreasing, points)
	want = []Violation{
		{Label: trainingdata.Fraud, From: 0, To: 1, InTrainRange: true, MeanScoreFrom: 0.5, MeanScoreTo: 0.6},
		{Label: trainingdata.Legitimate, From: 1, To: 2, InTrainRange: false, MeanScoreFrom: 0.05, MeanScoreTo: 0.2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("risk_decreasing violations = %+v, want %+v", got, want)
	}

	if got := violations(NoDirection, points); len(got) != 0 {
		t.Errorf("none violations = %+v, want none", got)
	}

	points[1].Fraud = nil
	if got := violations(RiskIncreasing, points); len(got) != 2 || got[0].Label != trainingdata.Fraud || got[0].From != 2 {
		t.Errorf("a missing label skips its pairs: %+v", got)
	}
}

// The expected directions are the ones the feature schema declares, for
// every feature, and every standardized feature has real units and a
// 12-point grid that its transformation inverts.
func TestDirectionsAndScalesMatchTheSpecification(t *testing.T) {
	text := string(read(t, specification))
	section := text[strings.Index(text, "## 20. Expected directions"):]
	rows := regexp.MustCompile("(?m)^\\| (\\d+) \\| `([a-z0-9_]+)` \\| `(risk_increasing|risk_decreasing|none)` \\|$").
		FindAllStringSubmatch(section, -1)

	descriptor := pixrisk.Descriptor()
	if len(rows) != len(descriptor) || len(directions) != len(descriptor) {
		t.Fatalf("specification declares %d directions, the package %d, want %d", len(rows), len(directions), len(descriptor))
	}
	for i, row := range rows {
		direction, ok := Direction(row[2])
		if row[1] != strconv.Itoa(i) || descriptor[i].Name != row[2] || !ok || direction != row[3] {
			t.Errorf("feature %d: specification says %s %s, package says %s", i, row[2], row[3], direction)
		}
	}

	units := map[string]bool{UnitCentavos: true, UnitCount: true, UnitRatio: true, UnitDays: true}
	for _, feature := range descriptor {
		if _, explicit := scales[feature.Name]; explicit && feature.Kind != pixrisk.Standardized {
			t.Errorf("%s has a scale but is not standardized", feature.Name)
		}
		if feature.Kind != pixrisk.Standardized {
			continue
		}
		s := scaleOf(feature.Name)
		if !units[s.unit] || len(s.grid) != 12 {
			t.Errorf("%s: scale = %+v", feature.Name, s)
		}
		for g, raw := range s.grid {
			if g > 0 && raw <= s.grid[g-1] {
				t.Errorf("%s: the grid is not increasing at %v", feature.Name, raw)
			}
			if back := s.raw(s.transform(raw)); math.Abs(back-raw) > 1e-6*math.Max(1, raw) {
				t.Errorf("%s: raw(transform(%v)) = %v", feature.Name, raw, back)
			}
		}
	}

	// The transformations are the fixed ones of pix-risk-v1 §9.
	if scaleOf("amount").transform(99) != math.Log1p(99) || scaleOf("amount_to_median_90d").transform(2.5) != math.Log(2.5) ||
		scaleOf("limit_utilization").transform(0.5) != math.Log1p(0.5) {
		t.Error("the transformations are not the fixed ones of the schema")
	}
}
