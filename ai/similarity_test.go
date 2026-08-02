package ai

import (
	"math"
	"testing"
)

func TestCosineSimilarity_KnownValues(t *testing.T) {
	tests := []struct {
		name string
		a, b []float64
		want float64
	}{
		{"identical", []float64{1, 0, 0}, []float64{1, 0, 0}, 1},
		{"orthogonal", []float64{1, 0}, []float64{0, 1}, 0},
		{"opposite", []float64{1, 0}, []float64{-1, 0}, -1},
		{"scaled-identical", []float64{2, 0}, []float64{4, 0}, 1},
		{"general", []float64{1, 2, 3}, []float64{4, 5, 6}, 32 / (math.Sqrt(14) * math.Sqrt(77))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CosineSimilarity(tt.a, tt.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("CosineSimilarity(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCosineSimilarity_LengthMismatch(t *testing.T) {
	_, err := CosineSimilarity([]float64{1, 2}, []float64{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for length mismatch")
	}
}

func TestCosineSimilarity_ZeroMagnitude(t *testing.T) {
	_, err := CosineSimilarity([]float64{0, 0, 0}, []float64{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for zero-magnitude vector a")
	}

	_, err = CosineSimilarity([]float64{1, 2, 3}, []float64{0, 0, 0})
	if err == nil {
		t.Fatal("expected error for zero-magnitude vector b")
	}
}
