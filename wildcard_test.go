package netsec

import (
	"errors"
	"testing"
)

func TestAnalyzeRRSIG(t *testing.T) {
	// RFC 4035 section 5.3.4 style example: a.b.host.example. (4 labels)
	// is answered from the wildcard *.example. The RRSIG Labels field is 1
	// ("example" only; the root label and the "*" label are not counted).
	deepExpansionProof := &NSECProof{
		ClosestEncloser: &NSEC{OwnerName: "example.", NextDomainName: "host.example."},
		NextCloser:      &NSEC{OwnerName: "example.", NextDomainName: "z.example."},
	}

	tests := []struct {
		name      string
		owner     string
		labels    uint8
		proof     *NSECProof
		want      WildcardStatus
		wantErr   error
		wantSig   string
		wantEncl  string
		wantNext  string
		wantCount int
		nilResult bool
	}{
		{
			name:      "multi-label expansion with valid NSEC proof",
			owner:     "a.b.host.example.",
			labels:    1,
			proof:     deepExpansionProof,
			want:      RRSIGWildcardExpanded,
			wantSig:   "*.example.",
			wantEncl:  "example.",
			wantNext:  "host.example.",
			wantCount: 4,
		},
		{
			name:     "single-label expansion reconstructs wildcard name",
			owner:    "host.example.",
			labels:   1,
			proof:    deepExpansionProof,
			want:     RRSIGWildcardExpanded,
			wantSig:  "*.example.",
			wantEncl: "example.",
			wantNext: "host.example.",
		},
		{
			name:    "expansion without any proof is rejected",
			owner:   "a.b.host.example.",
			labels:  1,
			proof:   nil,
			want:    RRSIGWildcardExpanded,
			wantErr: ErrMissingNSECProof,
			wantSig: "*.example.",
		},
		{
			name:   "expansion with partial proof is rejected",
			owner:  "a.b.host.example.",
			labels: 1,
			proof: &NSECProof{
				ClosestEncloser: &NSEC{OwnerName: "example.", NextDomainName: "host.example."},
			},
			want:    RRSIGWildcardExpanded,
			wantErr: ErrMissingNSECProof,
		},
		{
			name:   "expansion with wrong closest encloser is rejected",
			owner:  "a.b.host.example.",
			labels: 1,
			proof: &NSECProof{
				ClosestEncloser: &NSEC{OwnerName: "other.example.", NextDomainName: "zzz.example."},
				NextCloser:      &NSEC{OwnerName: "example.", NextDomainName: "z.example."},
			},
			want:    RRSIGWildcardExpanded,
			wantErr: ErrNSECNoClosestEncloser,
		},
		{
			name:   "expansion with non-covering next-closer NSEC is rejected",
			owner:  "a.b.host.example.",
			labels: 1,
			proof: &NSECProof{
				ClosestEncloser: &NSEC{OwnerName: "example.", NextDomainName: "host.example."},
				NextCloser:      &NSEC{OwnerName: "z.example.", NextDomainName: "zzz.example."},
			},
			want:    RRSIGWildcardExpanded,
			wantErr: ErrNSECNoNextCloser,
		},
		{
			name:   "next closer equal to NSEC owner does not prove absence",
			owner:  "a.b.host.example.",
			labels: 1,
			proof: &NSECProof{
				ClosestEncloser: &NSEC{OwnerName: "example.", NextDomainName: "host.example."},
				NextCloser:      &NSEC{OwnerName: "host.example.", NextDomainName: "x.example."},
			},
			want:    RRSIGWildcardExpanded,
			wantErr: ErrNSECNoNextCloser,
		},
		{
			name:    "labels larger than owner count is a forgery",
			owner:   "host.example.",
			labels:  9,
			proof:   nil,
			want:    RRSIGForged,
			wantErr: ErrLabelsOutOfRange,
		},
		{
			name:   "exact labels on ordinary owner name",
			owner:  "host.example.",
			labels: 2,
			proof:  nil,
			want:   RRSIGExact,
		},
		{
			name:    "exact labels but owner starts with star is illegal",
			owner:   "*.example.",
			labels:  2,
			proof:   nil,
			want:    RRSIGExact,
			wantErr: ErrWildcardOwnerOnExact,
		},
		{
			name:      "malformed relative owner name",
			owner:     "host.example",
			labels:    2,
			proof:     nil,
			wantErr:   ErrInvalidName,
			nilResult: true,
		},
		{
			name:   "malformed NSEC name in proof",
			owner:  "host.example.",
			labels: 1,
			proof: &NSECProof{
				ClosestEncloser: &NSEC{OwnerName: "example.", NextDomainName: "host.example."},
				NextCloser:      &NSEC{OwnerName: "example", NextDomainName: "host.example."},
			},
			want:    RRSIGWildcardExpanded,
			wantErr: ErrInvalidName,
		},
		{
			name:     "case-insensitive owner and proof names",
			owner:    "A.B.Host.Example.",
			labels:   1,
			proof:    deepExpansionProof,
			want:     RRSIGWildcardExpanded,
			wantSig:  "*.example.",
			wantEncl: "example.",
			wantNext: "host.example.",
		},
		{
			name:   "root wildcard expansion labels zero",
			owner:  "host.",
			labels: 0,
			proof: &NSECProof{
				ClosestEncloser: &NSEC{OwnerName: ".", NextDomainName: "host."},
				NextCloser:      &NSEC{OwnerName: ".", NextDomainName: "z."},
			},
			want:     RRSIGWildcardExpanded,
			wantSig:  "*.",
			wantEncl: ".",
			wantNext: "host.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig := &RRSIG{Labels: tt.labels}
			res, err := AnalyzeRRSIG(tt.owner, sig, tt.proof)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AnalyzeRRSIG() error = %v, want %v", err, tt.wantErr)
			}
			if tt.nilResult {
				if res != nil {
					t.Fatalf("expected nil result, got %+v", res)
				}
				return
			}
			if res == nil {
				t.Fatalf("expected non-nil result even on error")
			}
			if res.Status != tt.want {
				t.Fatalf("status = %v, want %v", res.Status, tt.want)
			}
			if tt.wantCount != 0 && res.OwnerLabels != tt.wantCount {
				t.Fatalf("owner labels = %d, want %d", res.OwnerLabels, tt.wantCount)
			}
			if tt.wantSig != "" && res.SignedName != tt.wantSig {
				t.Fatalf("signed name = %q, want %q", res.SignedName, tt.wantSig)
			}
			if tt.wantEncl != "" && res.ClosestEncloserName != tt.wantEncl {
				t.Fatalf("closest encloser = %q, want %q", res.ClosestEncloserName, tt.wantEncl)
			}
			if tt.wantNext != "" && res.NextCloserName != tt.wantNext {
				t.Fatalf("next closer = %q, want %q", res.NextCloserName, tt.wantNext)
			}
		})
	}
}

func TestLabelCount(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"example.", 1},
		{"host.example.", 2},
		{"*.example.", 2},
		{"a.b.host.example.", 4},
		{".", 0},
		{"host.example", -1},
		{"bad..example.", -1},
	}
	for _, tt := range tests {
		if got := LabelCount(tt.name); got != tt.want {
			t.Errorf("LabelCount(%q) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestCanonicallyCovers(t *testing.T) {
	tests := []struct {
		name   string
		nsec   *NSEC
		target string
		want   bool
	}{
		{"inside ordinary interval", &NSEC{"example.", "host.example."}, "foo.example.", true},
		{"before interval", &NSEC{"m.example.", "z.example."}, "a.example.", false},
		{"after interval", &NSEC{"m.example.", "z.example."}, "zzz.example.", false},
		{"equal to owner is not covered", &NSEC{"example.", "host.example."}, "example.", false},
		{"equal to next is not covered", &NSEC{"example.", "host.example."}, "host.example.", false},
		{"wrap-around interval covers after owner", &NSEC{"z.example.", "a.example."}, "zz.example.", true},
		{"wrap-around interval covers before next", &NSEC{"z.example.", "a.example."}, "0.example.", true},
		{"wrap-around interval excludes middle", &NSEC{"z.example.", "a.example."}, "m.example.", false},
		{"deeper name inside interval", &NSEC{"example.", "host.example."}, "a.b.c.example.", true},
		{"nil nsec", nil, "foo.example.", false},
		{"malformed target", &NSEC{"example.", "host.example."}, "foo.example", false},
	}
	for _, tt := range tests {
		if got := CanonicallyCovers(tt.nsec, tt.target); got != tt.want {
			t.Errorf("CanonicallyCovers(%v, %q) = %v, want %v", tt.nsec, tt.target, got, tt.want)
		}
	}
}

func TestCanonicalOrdering(t *testing.T) {
	// RFC 4034 section 6.1: compare from the root upward; a proper prefix
	// sorts before the longer name.
	order := []string{
		".",
		"example.",
		"a.example.",
		"yljkjljk.a.example.",
		"z.a.example.",
		"zabc.a.example.",
		"example.z.",
	}
	for i := 1; i < len(order); i++ {
		prev := splitLabels(order[i-1])
		cur := splitLabels(order[i])
		if !canonicalLess(prev, cur) {
			t.Errorf("expected %q < %q in canonical order", order[i-1], order[i])
		}
		if canonicalLess(cur, prev) {
			t.Errorf("expected %q !< %q in canonical order", order[i], order[i-1])
		}
	}
}

func TestNilRRSIG(t *testing.T) {
	if _, err := AnalyzeRRSIG("host.example.", nil, nil); err == nil {
		t.Fatal("expected error for nil RRSIG")
	}
}
