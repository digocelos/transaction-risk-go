package e2e

import (
	"path/filepath"
	"testing"
)

// The committed scenario dataset converts with the built command and no
// problem, and its summary is the one datasets/README.md documents.
func TestScenarioDataset(t *testing.T) {
	bin, work := commands(t), t.TempDir()

	type counts struct {
		rows, usable, insufficient, fraud, legitimate int
	}
	for file, want := range map[string]counts{
		"pix-training-scenarios-v3.csv": {8757, 8715, 42, 785, 7930},
	} {
		t.Run(file, func(t *testing.T) {
			summary := convertDataset(t, bin, work, file)
			if summary.Status != "converted" || summary.Rows != want.rows || summary.EmptyRows != 0 {
				t.Errorf("convert = %+v, want %d converted rows", summary, want.rows)
			}
			records := summary.Records
			if records.Total != want.rows || records.Usable != want.usable || records.Invalid != 0 ||
				records.Insufficient != want.insufficient || records.Immature != 0 {
				t.Errorf("records = %+v, want %d usable and %d insufficient of %d", records, want.usable, want.insufficient, want.rows)
			}
			if labels := summary.UsableLabels; labels.Fraud != want.fraud || labels.Legitimate != want.legitimate {
				t.Errorf("usable labels = %+v, want %d fraud and %d legitimate", labels, want.fraud, want.legitimate)
			}
		})
	}
}

type datasetSummary struct {
	Status    string `json:"status"`
	Rows      int    `json:"rows"`
	EmptyRows int    `json:"empty_rows"`
	Records   struct {
		Total        int `json:"total"`
		Usable       int `json:"usable"`
		Invalid      int `json:"invalid"`
		Insufficient int `json:"insufficient"`
		Immature     int `json:"immature"`
	} `json:"records"`
	UsableLabels struct {
		Fraud      int `json:"fraud"`
		Legitimate int `json:"legitimate"`
	} `json:"usable_labels"`
}

// convertDataset converts a committed dataset with the built command.
func convertDataset(t *testing.T, bin, work, file string) datasetSummary {
	t.Helper()

	var summary datasetSummary
	decode(t, "convert", run(t, bin, "convert", nil,
		"-input", filepath.Join("datasets", file),
		"-output", filepath.Join(work, file+".jsonl")), 0, &summary)
	return summary
}
