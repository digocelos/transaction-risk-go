package trainingconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const examplesDir = "../docs/contracts/training-config-examples"

type pathCode struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func issuesOf(t *testing.T, err error) []pathCode {
	t.Helper()

	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}

	got := []pathCode{}
	for _, issue := range invalid.Issues {
		if issue.Message == "" {
			t.Errorf("issue %s %s has no message", issue.Path, issue.Code)
		}
		got = append(got, pathCode{issue.Path, issue.Code})
	}

	return got
}

func TestDecodeSpecificationExamples(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join(examplesDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	for _, input := range inputs {
		if strings.HasSuffix(input, ".expected.json") {
			continue
		}
		count++

		t.Run(filepath.Base(input), func(t *testing.T) {
			var expected struct {
				Status string     `json:"status"`
				Issues []pathCode `json:"issues"`
			}
			if err := json.Unmarshal(readFile(t, strings.TrimSuffix(input, ".json")+".expected.json"), &expected); err != nil {
				t.Fatal(err)
			}

			_, err := Decode(readFile(t, input))

			if expected.Status == "valid" {
				if err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				return
			}

			if got := issuesOf(t, err); !reflect.DeepEqual(got, expected.Issues) {
				t.Errorf("issues = %v\nwant     %v", got, expected.Issues)
			}
		})
	}

	if count != 10 {
		t.Errorf("found %d examples, want 10", count)
	}
}

func TestDecodeReadsEveryMember(t *testing.T) {
	config, err := Decode(readFile(t, filepath.Join(examplesDir, "03-two-hidden-layers.json")))
	if err != nil {
		t.Fatal(err)
	}

	want := Config{
		FeatureSchema: "pix-risk-v1",
		HiddenLayers:  []int{16, 8},
		Activation:    "sigmoid",
		Algorithm:     "full-batch-gradient-descent",
		Loss:          "mean-squared-error",
		Seed:          7,
		LearningRate:  0.25,
		MaxEpochs:     20000,
		TrainEnd:      time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		ValidationEnd: time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC),
		Checkpoint:    "final_epoch",
		Threshold:     Threshold{Policy: "fixed", Value: 0.5},
	}

	if !reflect.DeepEqual(config, want) {
		t.Errorf("config = %+v\nwant     %+v", config, want)
	}

	if got := config.Sizes(); !reflect.DeepEqual(got, []int{62, 16, 8, 1}) {
		t.Errorf("Sizes() = %v, want [62 16 8 1]", got)
	}

	noHidden, err := Decode(readFile(t, filepath.Join(examplesDir, "02-no-hidden-layer.json")))
	if err != nil {
		t.Fatal(err)
	}
	if got := noHidden.Sizes(); !reflect.DeepEqual(got, []int{62, 1}) {
		t.Errorf("Sizes() without hidden layers = %v, want [62 1]", got)
	}
}

// edit replaces exactly one occurrence of old in the baseline example.
func edit(t *testing.T, old, new string) []byte {
	t.Helper()

	text := string(readFile(t, filepath.Join(examplesDir, "01-baseline.json")))
	if strings.Count(text, old) != 1 {
		t.Fatalf("%q must occur exactly once in the baseline example", old)
	}

	return []byte(strings.Replace(text, old, new, 1))
}

func TestDecodeNumberRanges(t *testing.T) {
	valid := map[string][]byte{
		"learning rate 100":      edit(t, `"learning_rate": 1.0`, `"learning_rate": 100`),
		"learning rate exponent": edit(t, `"learning_rate": 1.0`, `"learning_rate": 1e-3`),
		"one epoch":              edit(t, `"max_epochs": 5000`, `"max_epochs": 1`),
		"a million epochs":       edit(t, `"max_epochs": 5000`, `"max_epochs": 1000000`),
		"zero seed":              edit(t, `"seed": 42`, `"seed": 0`),
		"largest seed":           edit(t, `"seed": 42`, `"seed": 9007199254740991`),
		"threshold 0":            edit(t, `"value": 0.5`, `"value": 0`),
		"threshold 1":            edit(t, `"value": 0.5`, `"value": 1`),
		"256 neurons, 4 layers":  edit(t, `"hidden_layers": [`, `"hidden_layers": [256, 256, 256, `),
		"offset boundaries":      edit(t, `"2026-06-05T00:00:00Z"`, `"2026-06-04T21:00:00-03:00"`),
		"boundaries 1 ns apart":  edit(t, `"2026-06-05T00:00:00Z"`, `"2026-06-11T23:59:59.999999999Z"`),
	}

	for name, data := range valid {
		if _, err := Decode(data); err != nil {
			t.Errorf("%s: Decode() error = %v", name, err)
		}
	}

	invalid := map[string]struct {
		data []byte
		want pathCode
	}{
		"learning rate above 100": {edit(t, `"learning_rate": 1.0`, `"learning_rate": 100.0000001`), pathCode{"/learning_rate", "out_of_range"}},
		"negative learning rate":  {edit(t, `"learning_rate": 1.0`, `"learning_rate": -1`), pathCode{"/learning_rate", "out_of_range"}},
		"learning rate string":    {edit(t, `"learning_rate": 1.0`, `"learning_rate": "1.0"`), pathCode{"/learning_rate", "wrong_type"}},
		"huge learning rate":      {edit(t, `"learning_rate": 1.0`, `"learning_rate": 1e400`), pathCode{"/learning_rate", "out_of_range"}},
		"negative seed":           {edit(t, `"seed": 42`, `"seed": -1`), pathCode{"/seed", "invalid_number_format"}},
		"fractional epochs":       {edit(t, `"max_epochs": 5000`, `"max_epochs": 5000.0`), pathCode{"/max_epochs", "invalid_number_format"}},
		"negative threshold":      {edit(t, `"value": 0.5`, `"value": -0.1`), pathCode{"/selection/threshold/value", "out_of_range"}},
		"hidden layers not array": {edit(t, `"hidden_layers": [4]`, `"hidden_layers": 4`), pathCode{"/architecture/hidden_layers", "wrong_type"}},
		"hidden layer as string":  {edit(t, `"hidden_layers": [4]`, `"hidden_layers": ["4"]`), pathCode{"/architecture/hidden_layers/0", "wrong_type"}},
		"null seed":               {edit(t, `"seed": 42`, `"seed": null`), pathCode{"/seed", "null_not_allowed"}},
		"date-only boundary":      {edit(t, `"2026-06-05T00:00:00Z"`, `"2026-06-05"`), pathCode{"/boundaries/train_end", "invalid_format"}},
		"missing selection":       {edit(t, `"selection": {`, `"choice": {`), pathCode{"/choice", "unknown_member"}},
	}

	for name, test := range invalid {
		_, err := Decode(test.data)
		got := issuesOf(t, err)
		if !containsIssue(got, test.want) {
			t.Errorf("%s: issues = %v, want %v", name, got, test.want)
		}
	}
}

func containsIssue(issues []pathCode, want pathCode) bool {
	for _, issue := range issues {
		if issue == want {
			return true
		}
	}
	return false
}

func TestDecodeDocumentRules(t *testing.T) {
	baseline := readFile(t, filepath.Join(examplesDir, "01-baseline.json"))

	tests := map[string]struct {
		data []byte
		want []pathCode
	}{
		"empty":           {nil, []pathCode{{"", "invalid_json"}}},
		"byte order mark": {append([]byte("\xEF\xBB\xBF"), baseline...), []pathCode{{"", "bom_not_allowed"}}},
		"invalid UTF-8":   {[]byte("{\"seed\": \"\xff\"}"), []pathCode{{"", "invalid_utf8"}}},
		"trailing data":   {append(append([]byte{}, baseline...), []byte("{}")...), []pathCode{{"", "trailing_data"}}},
		"array":           {[]byte("[]"), []pathCode{{"", "wrong_type"}}},
		"duplicate":       {edit(t, `"seed": 42`, `"seed": 42, "seed": 7`), []pathCode{{"/seed", "duplicate_member"}}},
		"missing version": {edit(t, `"config_version": "pix-training-config-v1",`, ``), []pathCode{{"/config_version", "required"}}},
		"too large":       {append(append([]byte{}, baseline...), bytes.Repeat([]byte(" "), MaxDocumentSize)...), []pathCode{{"", "document_too_large"}}},
	}

	for name, test := range tests {
		_, err := Decode(test.data)
		if got := issuesOf(t, err); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: issues = %v, want %v", name, got, test.want)
		}
	}
}

type endlessReader struct{ read int }

func (r *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	r.read += len(p)
	return len(p), nil
}

func TestReadStopsAtTheSizeLimit(t *testing.T) {
	reader := &endlessReader{}

	_, err := Read(reader)
	if got := issuesOf(t, err); !reflect.DeepEqual(got, []pathCode{{"", "document_too_large"}}) {
		t.Errorf("issues = %v", got)
	}

	if reader.read > MaxDocumentSize+64*1024 {
		t.Errorf("read %d bytes, want to stop soon after %d", reader.read, MaxDocumentSize)
	}

	config, err := Read(bytes.NewReader(readFile(t, filepath.Join(examplesDir, "01-baseline.json"))))
	if err != nil || config.MaxEpochs != 5000 {
		t.Errorf("Read() = %+v, %v", config, err)
	}
}

// Every decoded configuration passes Validate, which applies the same rules
// to configurations built in code.
func TestDecodedConfigurationsAreValid(t *testing.T) {
	for _, name := range []string{"01-baseline.json", "02-no-hidden-layer.json", "03-two-hidden-layers.json"} {
		config, err := Decode(readFile(t, filepath.Join(examplesDir, name)))
		if err != nil {
			t.Fatal(err)
		}
		if err := config.Validate(); err != nil {
			t.Errorf("%s: Validate() error = %v", name, err)
		}
	}
}
