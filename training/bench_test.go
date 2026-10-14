package training

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/digocelos/transaction-risk-go/internal/synthetic"
	"github.com/digocelos/transaction-risk-go/nn"
	"github.com/digocelos/transaction-risk-go/pixrisk"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
)

// benchmarkConfig splits the default synthetic time range into 12 months
// of train, 3 of validation, and 3 of test, whose last month is immature.
func benchmarkConfig(hidden []int, epochs int) (cfg trainingconfig.Config) {
	cfg = config()
	cfg.HiddenLayers = hidden
	cfg.MaxEpochs = epochs
	cfg.TrainEnd = time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	cfg.ValidationEnd = time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	return cfg
}

func syntheticDataset(b *testing.B, records int) []byte {
	b.Helper()

	var data bytes.Buffer
	if err := synthetic.Generate(&data, synthetic.DefaultOptions(records, 1)); err != nil {
		b.Fatal(err)
	}
	return data.Bytes()
}

func benchmarkSizes() []int {
	if testing.Short() {
		return []int{1_000}
	}
	return []int{1_000, 10_000, 100_000}
}

// BenchmarkPrepareDataset reads, classifies, partitions, and prepares a
// synthetic dataset, and reports the live heap per usable record that a
// run keeps.
func BenchmarkPrepareDataset(b *testing.B) {
	for _, records := range benchmarkSizes() {
		b.Run(fmt.Sprintf("records=%d", records), func(b *testing.B) {
			data := syntheticDataset(b, records)
			boundaries := benchmarkConfig([]int{4}, 1).Boundaries()
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()

			var heap float64
			for b.Loop() {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)

				prepared, err := PrepareDataset(context.Background(), bytes.NewReader(data), boundaries, DefaultLimits.MaxRecords)
				if err != nil {
					b.Fatal(err)
				}

				runtime.GC()
				runtime.ReadMemStats(&after)
				usable := len(prepared.Train) + len(prepared.Validation) + len(prepared.Test)
				heap = (float64(after.HeapAlloc) - float64(before.HeapAlloc)) / float64(usable)
				runtime.KeepAlive(prepared)
			}

			b.ReportMetric(heap, "heap-B/usable")
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*records), "ns/record")
		})
	}
}

// BenchmarkEpoch measures one full-batch epoch of the baseline and of the
// largest architecture, and reports the time per sample and per unit of
// training work.
func BenchmarkEpoch(b *testing.B) {
	data := syntheticDataset(b, 10_000)
	prepared, err := PrepareDataset(context.Background(), bytes.NewReader(data),
		benchmarkConfig([]int{4}, 1).Boundaries(), DefaultLimits.MaxRecords)
	if err != nil {
		b.Fatal(err)
	}

	cases := []struct {
		name    string
		hidden  []int
		samples int
	}{
		{"hidden=4", []int{4}, len(prepared.Train)},
		{"hidden=256x4", []int{256, 256, 256, 256}, 1024},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			sizes := append(append([]int{pixrisk.FeatureCount}, c.hidden...), 1)
			network, err := nn.NewNetwork(1, sizes...)
			if err != nil {
				b.Fatal(err)
			}

			samples := make([]nn.Sample, c.samples)
			for i := range samples {
				example := prepared.Train[i%len(prepared.Train)]
				samples[i] = nn.Sample{Inputs: example.Values, Targets: []float64{target(example.Label)}}
			}

			parameters := 0
			for i := 1; i < len(sizes); i++ {
				parameters += sizes[i-1]*sizes[i] + sizes[i]
			}

			b.ReportAllocs()
			for b.Loop() {
				if _, err := network.TrainBatchContext(context.Background(), samples, 0.01); err != nil {
					b.Fatal(err)
				}
			}

			perEpoch := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
			b.ReportMetric(perEpoch/float64(len(samples)), "ns/sample")
			b.ReportMetric(perEpoch/float64(len(samples)*parameters), "ns/work")
		})
	}
}

// BenchmarkRun trains, selects, and evaluates the baseline architecture
// end to end on a synthetic dataset.
func BenchmarkRun(b *testing.B) {
	records := 10_000
	if testing.Short() {
		records = 1_000
	}
	data := syntheticDataset(b, records)
	cfg := benchmarkConfig([]int{4}, 100)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := Run(context.Background(), bytes.NewReader(data), cfg, DefaultLimits, nil); err != nil {
			b.Fatal(err)
		}
	}
}
