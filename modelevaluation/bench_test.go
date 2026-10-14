package modelevaluation

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/internal/synthetic"
)

// BenchmarkEvaluate evaluates a model on the test partition of a synthetic
// dataset.
func BenchmarkEvaluate(b *testing.B) {
	records := 10_000
	if testing.Short() {
		records = 1_000
	}

	var data bytes.Buffer
	if err := synthetic.Generate(&data, synthetic.DefaultOptions(records, 1)); err != nil {
		b.Fatal(err)
	}

	cfg := baselineConfig(b)
	cfg.MaxEpochs = 20
	cfg.TrainEnd = time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	cfg.ValidationEnd = time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	model, _ := trainModel(b, data.Bytes(), cfg)

	b.SetBytes(int64(data.Len()))
	b.ReportAllocs()
	for b.Loop() {
		_, err := Evaluate(context.Background(), model, bytes.NewReader(data.Bytes()),
			Options{MaxRecords: maxRecords, Scope: PartitionScope("test")})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*records), "ns/record")
}
