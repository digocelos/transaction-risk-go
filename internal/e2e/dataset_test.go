package e2e

import (
	"path/filepath"
	"testing"
)

// The committed scenario dataset converts with the built command and no
// problem, and its summary is the one datasets/README.md documents.
func TestScenarioDataset(t *testing.T) {
	bin, work := commands(t), t.TempDir()

	var summary struct {
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
	decode(t, "convert", run(t, bin, "convert", nil,
		"-input", "datasets/pix-training-scenarios-v1.csv",
		"-output", filepath.Join(work, "scenarios.jsonl")), 0, &summary)

	if summary.Status != "converted" || summary.Rows != 8000 || summary.EmptyRows != 0 {
		t.Errorf("convert = %+v, want 8000 converted rows", summary)
	}
	records := summary.Records
	if records.Total != 8000 || records.Usable != 7960 || records.Invalid != 0 ||
		records.Insufficient != 40 || records.Immature != 0 {
		t.Errorf("records = %+v, want 7960 usable and 40 insufficient of 8000", records)
	}
	if labels := summary.UsableLabels; labels.Fraud != 478 || labels.Legitimate != 7482 {
		t.Errorf("usable labels = %+v, want 478 fraud and 7482 legitimate", labels)
	}
}
