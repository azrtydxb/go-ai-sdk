package provider

import (
	"testing"
)

func TestResolveBudgetTokens(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ReasoningConfig
		want int
		ok   bool
	}{
		{
			name: "nil config",
			cfg:  nil,
			want: 0,
			ok:   false,
		},
		{
			name: "explicit budget tokens",
			cfg:  &ReasoningConfig{BudgetTokens: intPtr(2048)},
			want: 2048,
			ok:   true,
		},
		{
			name: "effort minimal",
			cfg:  &ReasoningConfig{Effort: "minimal"},
			want: 1024,
			ok:   true,
		},
		{
			name: "effort low",
			cfg:  &ReasoningConfig{Effort: "low"},
			want: 4096,
			ok:   true,
		},
		{
			name: "effort medium",
			cfg:  &ReasoningConfig{Effort: "medium"},
			want: 8192,
			ok:   true,
		},
		{
			name: "effort high",
			cfg:  &ReasoningConfig{Effort: "high"},
			want: 16384,
			ok:   true,
		},
		{
			name: "budget tokens wins over effort",
			cfg:  &ReasoningConfig{Effort: "minimal", BudgetTokens: intPtr(5000)},
			want: 5000,
			ok:   true,
		},
		{
			name: "unrecognized effort",
			cfg:  &ReasoningConfig{Effort: "unknown"},
			want: 0,
			ok:   false,
		},
		{
			name: "empty effort",
			cfg:  &ReasoningConfig{Effort: ""},
			want: 0,
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveBudgetTokens(tt.cfg)
			if got != tt.want || ok != tt.ok {
				t.Errorf("ResolveBudgetTokens(%+v) = (%d, %v), want (%d, %v)", tt.cfg, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}
