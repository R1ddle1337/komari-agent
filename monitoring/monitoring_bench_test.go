package monitoring

import "testing"

var benchmarkReport []byte

func BenchmarkGenerateReport(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		benchmarkReport = GenerateReport()
	}
}
