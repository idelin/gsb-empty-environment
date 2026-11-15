package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestAnalyzeRRSIG(t *testing.T) {
	tests := []struct {
		name    string
		rr      RRSIG
		wantErr error
		want    WildcardAnalysis
	}{
		{
			// Two labels synthesized away: *.example. signs a.b.example.
			name: "multi label expansion",
			rr:   RRSIG{OwnerName: "a.b.example.", Labels: 2},
			want: WildcardAnalysis{
				OwnerLabels:      3,
				Labels:           2,
				WildcardExpanded: true,
				ClosestEncloser:  "b.example.",
				NextCloserName:   "a.b.example.",
				SignedName:       "*.b.example.",
			},
		},
		{
			// One label synthesized away: *.example. signs host.example.
			name: "single label expansion",
			rr:   RRSIG{OwnerName: "host.example.", Labels: 2},
			want: WildcardAnalysis{
				OwnerLabels:      2,
				Labels:           2,
				WildcardExpanded: false,
				SignedName:       "host.example.",
			},
		},
		{
			// One label synthesized away using a three-label owner.
			name: "one label expansion three label owner",
			rr:   RRSIG{OwnerName: "host.a.example.", Labels: 2},
			want: WildcardAnalysis{
				OwnerLabels:      3,
				Labels:           2,
				WildcardExpanded: true,
				ClosestEncloser:  "a.example.",
				NextCloserName:   "host.a.example.",
				SignedName:       "*.a.example.",
			},
		},
		{
			// Wildcard directly under the root: labels 0 means encloser ".".
			name: "expansion with labels zero",
			rr:   RRSIG{OwnerName: "anything.", Labels: 0},
			want: WildcardAnalysis{
				OwnerLabels:      1,
				Labels:           0,
				WildcardExpanded: true,
				ClosestEncloser:  ".",
				NextCloserName:   "anything.",
				SignedName:       "*.",
			},
		},
		{
			// Labels > owner label count is a forgery.
			name:    "labels overflow is forged",
			rr:      RRSIG{OwnerName: "a.example.", Labels: 9},
			wantErr: ErrLabelsOverflow,
		},
		{
			// Labels == label count forbids a literal wildcard owner.
			name:    "star owner with equal labels is bogus",
			rr:      RRSIG{OwnerName: "*.example.", Labels: 2},
			wantErr: ErrBogusStarOwner,
		},
		{
			// A literal wildcard owner signed correctly is not an expansion.
			name: "literal wildcard owner",
			rr:   RRSIG{OwnerName: "*.example.", Labels: 1},
			want: WildcardAnalysis{
				OwnerLabels:      2,
				Labels:           1,
				WildcardExpanded: false,
				SignedName:       "*.example.",
			},
		},
		{
			// A wildcard owner with an impossible Labels count.
			name:    "star owner with underflow labels",
			rr:      RRSIG{OwnerName: "*.a.example.", Labels: 0},
			wantErr: ErrWildcardLabelsMismatch,
		},
		{
			// Equal labels on a concrete owner: ordinary signature.
			name: "concrete owner equal labels",
			rr:   RRSIG{OwnerName: "WWW.Example.", Labels: 2},
			want: WildcardAnalysis{
				OwnerLabels:      2,
				Labels:           2,
				WildcardExpanded: false,
				SignedName:       "WWW.Example.",
			},
		},
		{
			name:    "root owner rejected",
			rr:      RRSIG{OwnerName: ".", Labels: 0},
			wantErr: ErrRootName,
		},
		{
			name:    "embedded star rejected",
			rr:      RRSIG{OwnerName: "a.*.example.", Labels: 2},
			wantErr: ErrMalformedName,
		},
		{
			name:    "negative labels rejected",
			rr:      RRSIG{OwnerName: "a.example.", Labels: -1},
			wantErr: ErrLabelsOverflow,
		},
		{
			name:    "empty interior label rejected",
			rr:      RRSIG{OwnerName: "a..example.", Labels: 2},
			wantErr: ErrMalformedName,
		},
		{
			name:    "oversized label rejected",
			rr:      RRSIG{OwnerName: strings.Repeat("a", 64) + ".example.", Labels: 1},
			wantErr: ErrMalformedName,
		},
		{
			name:    "oversized wire name rejected",
			rr:      RRSIG{OwnerName: strings.Repeat("a.", 130), Labels: 1},
			wantErr: ErrMalformedName,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AnalyzeRRSIG(tc.rr)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("AnalyzeRRSIG(%v) error = %v, want %v", tc.rr, err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if got != tc.want {
				t.Fatalf("AnalyzeRRSIG(%v) = %+v, want %+v", tc.rr, got, tc.want)
			}
		})
	}
}

func TestLabelCount(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"root dot", ".", 0},
		{"root empty", "", 0},
		{"no trailing dot", "a.b.example", 3},
		{"trailing dot", "a.b.example.", 3},
		{"star counts as label", "*.example.", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LabelCount(tc.in)
			if err != nil {
				t.Fatalf("LabelCount(%q) unexpected error %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("LabelCount(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
