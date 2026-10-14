package scoring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/digocelos/transaction-risk-go/internal/synthetic"
	"github.com/digocelos/transaction-risk-go/modelartifact"
	"github.com/digocelos/transaction-risk-go/scoring"
	"github.com/digocelos/transaction-risk-go/training"
	"github.com/digocelos/transaction-risk-go/trainingconfig"
	"github.com/digocelos/transaction-risk-go/trainingreport"
)

// benchmarkArtifact trains one epoch of an architecture on the fixture.
func benchmarkArtifact(b *testing.B, hidden []int) []byte {
	b.Helper()

	cfg, err := trainingconfig.Decode(read(b, baseline))
	if err != nil {
		b.Fatal(err)
	}
	cfg.MaxEpochs, cfg.HiddenLayers = 1, hidden

	result, err := training.Run(context.Background(), bytes.NewReader(read(b, fixture)), cfg, training.DefaultLimits, nil)
	if err != nil {
		b.Fatal(err)
	}
	report, err := trainingreport.Marshal(result.Report)
	if err != nil {
		b.Fatal(err)
	}
	artifact, err := modelartifact.FromTraining(result.Network, result.State, result.Report, digest(report),
		modelartifact.SoftwareFromBuild())
	if err != nil {
		b.Fatal(err)
	}
	data, err := modelartifact.Marshal(artifact)
	if err != nil {
		b.Fatal(err)
	}
	return data
}

// documents returns the scoring inputs of a synthetic dataset, with its
// absence patterns and insufficient inputs.
func documents(b *testing.B, count int) [][]byte {
	b.Helper()

	var data bytes.Buffer
	if err := synthetic.Generate(&data, synthetic.DefaultOptions(count, 2)); err != nil {
		b.Fatal(err)
	}

	var documents [][]byte
	for _, line := range bytes.Split(bytes.TrimSpace(data.Bytes()), []byte("\n")) {
		var record struct {
			ScoringInput json.RawMessage `json:"scoring_input"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			b.Fatal(err)
		}
		documents = append(documents, record.ScoringInput)
	}
	return documents
}

var architectures = []struct {
	name   string
	hidden []int
}{
	{"hidden=4", []int{4}},
	{"hidden=256x4", []int{256, 256, 256, 256}},
}

// BenchmarkLoad decodes and validates an artifact into a scorer.
func BenchmarkLoad(b *testing.B) {
	for _, a := range architectures {
		b.Run(a.name, func(b *testing.B) {
			data := benchmarkArtifact(b, a.hidden)
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()

			for b.Loop() {
				if _, err := scoring.New(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkScore scores synthetic documents one at a time, and from every
// available CPU at once with one shared scorer.
func BenchmarkScore(b *testing.B) {
	docs := documents(b, 1000)

	for _, a := range architectures {
		scorer, err := scoring.New(benchmarkArtifact(b, a.hidden))
		if err != nil {
			b.Fatal(err)
		}

		b.Run(fmt.Sprintf("%s/sequential", a.name), func(b *testing.B) {
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				if _, err := scorer.Score(docs[i%len(docs)]); err != nil {
					b.Fatal(err)
				}
				i++
			}
		})

		b.Run(fmt.Sprintf("%s/parallel", a.name), func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					if _, err := scorer.Score(docs[i%len(docs)]); err != nil {
						b.Error(err)
						return
					}
					i++
				}
			})
		})
	}
}
