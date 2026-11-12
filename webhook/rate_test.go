package webhook

import (
	"errors"
	"testing"
)

func TestParseAllowedRate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     Rate
		wantErr  bool
		wantSent error
	}{
		{name: "wildcard", input: "*", want: Rate{Wildcard: true}},
		{name: "integer", input: "42", want: Rate{RequestsPerSecond: 42}},
		{name: "whitespace integer", input: " 7 ", want: Rate{RequestsPerSecond: 7}},
		{name: "zero rejected", input: "0", wantErr: true, wantSent: ErrInvalidAllowedRate},
		{name: "negative rejected", input: "-3", wantErr: true, wantSent: ErrInvalidAllowedRate},
		{name: "text rejected", input: "fast", wantErr: true, wantSent: ErrInvalidAllowedRate},
		{name: "empty rejected", input: "", wantErr: true, wantSent: ErrInvalidAllowedRate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAllowedRate(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %+v", tt.input, got)
				}
				if tt.wantSent != nil && !errors.Is(err, tt.wantSent) {
					t.Fatalf("expected error wrapping %v, got %v", tt.wantSent, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMinRate(t *testing.T) {
	tests := []struct {
		name      string
		requested Rate
		allowed   Rate
		want      Rate
	}{
		{
			name:      "both numeric take smaller allowed",
			requested: Rate{RequestsPerSecond: 100},
			allowed:   Rate{RequestsPerSecond: 30},
			want:      Rate{RequestsPerSecond: 30},
		},
		{
			name:      "both numeric take smaller requested",
			requested: Rate{RequestsPerSecond: 20},
			allowed:   Rate{RequestsPerSecond: 80},
			want:      Rate{RequestsPerSecond: 20},
		},
		{
			name:      "allowed wildcard falls back to requested",
			requested: Rate{RequestsPerSecond: 25},
			allowed:   Rate{Wildcard: true},
			want:      Rate{RequestsPerSecond: 25},
		},
		{
			name:      "requested wildcard falls back to allowed",
			requested: Rate{Wildcard: true},
			allowed:   Rate{RequestsPerSecond: 12},
			want:      Rate{RequestsPerSecond: 12},
		},
		{
			name:      "both wildcard stays wildcard",
			requested: Rate{Wildcard: true},
			allowed:   Rate{Wildcard: true},
			want:      Rate{Wildcard: true},
		},
		{
			name:      "equal numeric values",
			requested: Rate{RequestsPerSecond: 50},
			allowed:   Rate{RequestsPerSecond: 50},
			want:      Rate{RequestsPerSecond: 50},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MinRate(tt.requested, tt.allowed)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRateString(t *testing.T) {
	if got := (Rate{Wildcard: true}).String(); got != "*" {
		t.Fatalf("wildcard renders as %q", got)
	}
	if got := (Rate{RequestsPerSecond: 99}).String(); got != "99" {
		t.Fatalf("numeric renders as %q", got)
	}
}

func TestMinRateRejectsNonPositive(t *testing.T) {
	if _, err := MinRate(Rate{RequestsPerSecond: 0}, Rate{RequestsPerSecond: 5}); err == nil {
		t.Fatal("zero requested rate should be rejected")
	}
	if _, err := MinRate(Rate{RequestsPerSecond: 5}, Rate{RequestsPerSecond: -1}); err == nil {
		t.Fatal("negative allowed rate should be rejected")
	}
}
