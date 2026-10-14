package modelartifact

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

const (
	fixture  = "../training/testdata/pix-risk-v1.jsonl"
	baseline = "../docs/contracts/training-config-examples/01-baseline.json"
)

// trained trains a small model on the synthetic fixture and returns it with
// the bytes of its report.
func trained(t *testing.T) (training.Result, []byte) {
	t.Helper()

	data, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := trainingconfig.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxEpochs = 300

	dataset, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	result, err := training.Run(context.Background(), bytes.NewReader(dataset), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}

	return result, report
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var software = Software{Module: "github.com/digocelos/transaction-risk-go", Version: "(devel)", Revision: "0123abc"}

func artifact(t *testing.T) (Artifact, training.Result, []byte) {
	t.Helper()

	result, report := trained(t)
	a, err := FromTraining(result.Network, result.State, result.Report, sha256Hex(report), software)
	if err != nil {
		t.Fatalf("FromTraining() error = %v", err)
	}

	data, err := Marshal(a)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	return a, result, data
}

// scoringInputs decodes the scoring input of every fixture record.
func scoringInputs(t *testing.T) []pixinput.ScoringInput {
	t.Helper()

	file, err := os.Open(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var inputs []pixinput.ScoringInput
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var record struct {
			ScoringInput json.RawMessage `json:"scoring_input"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		input, err := pixinput.Decode(record.ScoringInput, pixinput.ContractV2)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input)
	}

	return inputs
}

// The decoded artifact prepares the same vectors and produces the same
// scores, bit for bit, as the network and state that training produced.
func TestRoundTripPreservesFeaturesAndScores(t *testing.T) {
	_, result, data := artifact(t)

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	scored := 0
	for _, input := range scoringInputs(t) {
		prepared, err := pixrisk.Prepare(input)
		if err != nil {
			continue // insufficient records have no features
		}

		before, err := result.State.Apply(prepared)
		if err != nil {
			t.Fatal(err)
		}
		after, err := decoded.State.Apply(prepared)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Errorf("prepared vectors differ after the round trip")
		}

		want, err := result.Network.Predict(before)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decoded.Network.Predict(after)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] != want[0] {
			t.Errorf("score = %v after the round trip, want %v", got[0], want[0])
		}
		scored++
	}

	if scored != 7 {
		t.Errorf("scored %d fixture records, want 7", scored)
	}

	again, err := Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, data) {
		t.Error("encoding the decoded artifact changed its bytes")
	}
}

func TestArtifactRecordsItsIdentities(t *testing.T) {
	a, result, data := artifact(t)

	configSHA256, err := result.Report.Configuration.Config.SHA256()
	if err != nil {
		t.Fatal(err)
	}

	switch {
	case a.Training.ConfigSHA256 != configSHA256:
		t.Errorf("config_sha256 = %s", a.Training.ConfigSHA256)
	case a.Training.DatasetSHA256 != result.Report.Dataset.DatasetSHA256:
		t.Errorf("dataset_sha256 = %s", a.Training.DatasetSHA256)
	case a.SelectedEpoch != 300 || a.Threshold != (Threshold{Policy: "fixed", Value: 0.5}):
		t.Errorf("selection = %d %+v", a.SelectedEpoch, a.Threshold)
	case !reflect.DeepEqual(a.Sizes, []int{62, 4, 1}):
		t.Errorf("sizes = %v", a.Sizes)
	}

	for _, want := range []string{
		`"artifact_version": "pix-risk-model-v1"`,
		`"scoring_input": "pix-payer-input-v2"`,
		`"training_example": "pix-training-example-v2"`,
		`"feature_schema": "pix-risk-v1"`,
		`"config_version": "pix-training-config-v2"`,
		`"report_version": "pix-training-report-v2"`,
		`"revision": "0123abc"`,
		`"parameters_sha256": "`,
		`"preparation_sha256": "`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("artifact does not contain %s", want)
		}
	}

	if _, _, second := artifact(t); !bytes.Equal(second, data) {
		t.Error("two identical runs produced different artifacts")
	}
}

// tamper decodes an artifact into generic JSON, applies change, and encodes
// it again.
func tamper(t *testing.T, data []byte, change func(map[string]any)) []byte {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}

	change(value)

	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func object(value map[string]any, path ...string) map[string]any {
	for _, name := range path {
		value = value[name].(map[string]any)
	}
	return value
}

func TestDecodeRejectsTamperedOrInvalidArtifacts(t *testing.T) {
	_, _, data := artifact(t)

	if _, err := Decode(tamper(t, data, func(map[string]any) {})); err != nil {
		t.Fatalf("Decode() of a reformatted artifact: %v", err)
	}

	tests := map[string][]byte{
		"changed weight": tamper(t, data, func(v map[string]any) {
			layer := object(v, "network")["layers"].([]any)[0].(map[string]any)
			layer["weights"].([]any)[0].([]any)[0] = json.Number("0.123")
		}),
		"changed mean": tamper(t, data, func(v map[string]any) {
			feature := object(v, "features", "preparation")["features"].([]any)[0].(map[string]any)
			feature["mean"] = json.Number("1.5")
		}),
		"missing preparation": tamper(t, data, func(v map[string]any) {
			delete(object(v, "features"), "preparation")
		}),
		"reordered descriptor": tamper(t, data, func(v map[string]any) {
			descriptor := object(v, "features")["descriptor"].([]any)
			descriptor[0], descriptor[1] = descriptor[1], descriptor[0]
		}),
		"other sizes": tamper(t, data, func(v map[string]any) {
			object(v, "network")["sizes"] = []any{62, 5, 1}
		}),
		"unsupported version": tamper(t, data, func(v map[string]any) {
			v["artifact_version"] = "pix-risk-model-v2"
		}),
		"other contract": tamper(t, data, func(v map[string]any) {
			object(v, "contracts")["scoring_input"] = "pix-payer-input-v1"
		}),
		"other report version": tamper(t, data, func(v map[string]any) {
			object(v, "training")["report_version"] = "pix-training-report-v1"
		}),
		"unknown member": tamper(t, data, func(v map[string]any) {
			v["signature"] = "none"
		}),
		"threshold above 1": tamper(t, data, func(v map[string]any) {
			object(v, "selection", "threshold")["value"] = json.Number("1.5")
		}),
		"short report hash": tamper(t, data, func(v map[string]any) {
			object(v, "training")["report_sha256"] = "0f"
		}),
		"zero selected epoch": tamper(t, data, func(v map[string]any) {
			object(v, "selection")["selected_epoch"] = json.Number("0")
		}),
		"empty software": tamper(t, data, func(v map[string]any) {
			object(v, "software")["revision"] = ""
		}),
		"overflowing weight": []byte(strings.Replace(string(tamper(t, data, func(v map[string]any) {
			layer := object(v, "network")["layers"].([]any)[0].(map[string]any)
			layer["weights"].([]any)[0].([]any)[0] = json.Number("7777")
		})), "7777", "1e999", 1)),
		"truncated":     data[:len(data)/2],
		"trailing data": append(append([]byte{}, data...), []byte("{}")...),
		"duplicate member": []byte(strings.Replace(string(data), `"artifact_version": "pix-risk-model-v1",`,
			`"artifact_version": "pix-risk-model-v1", "artifact_version": "pix-risk-model-v1",`, 1)),
		"invalid UTF-8": append([]byte{0xff}, data...),
	}

	for name, tampered := range tests {
		if _, err := Decode(tampered); err == nil {
			t.Errorf("%s: Decode() error = nil, want an error", name)
		}
	}
}

func TestFromTrainingRejectsInvalidInputs(t *testing.T) {
	result, report := trained(t)

	if _, err := FromTraining(result.Network, result.State, result.Report, "0f", software); err == nil {
		t.Error("short report hash: FromTraining() error = nil")
	}
	if _, err := FromTraining(nil, result.State, result.Report, sha256Hex(report), software); err == nil {
		t.Error("missing network: FromTraining() error = nil")
	}
	if _, err := FromTraining(result.Network, pixrisk.State{}, result.Report, sha256Hex(report), software); err == nil {
		t.Error("missing state: FromTraining() error = nil")
	}
	if _, err := FromTraining(result.Network, result.State, result.Report, sha256Hex(report), Software{}); err == nil {
		t.Error("empty software: FromTraining() error = nil")
	}
}

func TestSoftwareFromBuildIsStableAndPrintable(t *testing.T) {
	first, second := SoftwareFromBuild(), SoftwareFromBuild()

	if first != second {
		t.Errorf("software = %+v, then %+v", first, second)
	}

	for _, text := range []string{first.Module, first.Version, first.Revision} {
		if !validText(text) {
			t.Errorf("software text %q is not printable text of 1 to 256 bytes", text)
		}
	}
}

func TestSoftwareFromBuildInformation(t *testing.T) {
	setting := func(key, value string) debug.BuildSetting { return debug.BuildSetting{Key: key, Value: value} }
	main := debug.Module{Path: "github.com/digocelos/transaction-risk-go", Version: "v1.2.3"}

	tests := map[string]struct {
		info *debug.BuildInfo
		want Software
	}{
		"no build information": {nil, Software{Unknown, Unknown, Unknown}},
		"clean checkout": {&debug.BuildInfo{Main: main, Settings: []debug.BuildSetting{
			setting("vcs.revision", "abc123"), setting("vcs.modified", "false"),
		}}, Software{"github.com/digocelos/transaction-risk-go", "v1.2.3", "abc123"}},
		"modified checkout": {&debug.BuildInfo{Main: main, Settings: []debug.BuildSetting{
			setting("vcs.revision", "abc123"), setting("vcs.modified", "true"),
		}}, Software{"github.com/digocelos/transaction-risk-go", "v1.2.3", "abc123+dirty"}},
		"no VCS": {&debug.BuildInfo{Main: debug.Module{}}, Software{Unknown, Unknown, Unknown}},
	}

	for name, test := range tests {
		if got := softwareFrom(test.info); got != test.want {
			t.Errorf("%s: software = %+v, want %+v", name, got, test.want)
		}
	}
}

// Boundaries written with any offset produce the same artifact, in UTC.
func TestMarshalWritesBoundariesInUTC(t *testing.T) {
	result, report := trained(t)

	brasilia := time.FixedZone("", -3*60*60)
	shifted := result.Report
	shifted.Partitions.TrainEnd = shifted.Partitions.TrainEnd.In(brasilia)
	shifted.Partitions.ValidationEnd = shifted.Partitions.ValidationEnd.In(brasilia)

	var encoded [][]byte
	for _, r := range []trainingreport.Report{result.Report, shifted} {
		a, err := FromTraining(result.Network, result.State, r, sha256Hex(report), software)
		if err != nil {
			t.Fatal(err)
		}
		data, err := Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, data)
	}

	if !bytes.Equal(encoded[0], encoded[1]) || !strings.Contains(string(encoded[1]), `"train_end": "2026-06-05T00:00:00Z"`) {
		t.Error("boundary offsets changed the artifact or were not written in UTC")
	}
}
