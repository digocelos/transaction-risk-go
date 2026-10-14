package trainingdata_test

import (
	"bytes"
	"testing"

	"github.com/digocelos/transaction-risk-go/internal/synthetic"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// BenchmarkScan reads, validates, and classifies a synthetic dataset.
func BenchmarkScan(b *testing.B) {
	var data bytes.Buffer
	if err := synthetic.Generate(&data, synthetic.DefaultOptions(10_000, 1)); err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(data.Len()))
	b.ReportAllocs()

	for b.Loop() {
		summary, err := trainingdata.Scan(bytes.NewReader(data.Bytes()), trainingdata.PixRiskV1,
			trainingdata.DefaultMaxRecords, func(trainingdata.Record) error { return nil })
		if err != nil || summary.Records.Total != 10_000 {
			b.Fatalf("Scan() = %+v, %v", summary.Records, err)
		}
	}

	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*10_000), "ns/record")
}
