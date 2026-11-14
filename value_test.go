package netsec

import (
	"strings"
	"testing"
)

func TestJoinDoesNotChooseAnOrder(t *testing.T) {
	tests := []struct {
		name string
		a    Tag
		b    Tag
		want Tag
	}{
		{"same be", TagBE, TagBE, TagBE},
		{"same le", TagLE, TagLE, TagLE},
		{"be then le", TagBE, TagLE, TagConflict},
		{"le then be", TagLE, TagBE, TagConflict},
		{"conflict absorbs be", TagConflict, TagBE, TagConflict},
		{"conflict absorbs const", TagConflict, TagConst, TagConflict},
		{"const is neutral", TagConst, TagBE, TagBE},
		{"raw with integer conflicts", TagRaw, TagBE, TagConflict},
		{"unknown dominates", TagUnknown, TagBE, TagUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Join(tt.a, tt.b); got != tt.want {
				t.Fatalf("Join(%s, %s) = %s, want %s", tt.a, tt.b, got, tt.want)
			}
			if got := Join(tt.b, tt.a); got != tt.want {
				t.Fatalf("Join is not symmetric: Join(%s, %s) = %s, want %s",
					tt.b, tt.a, got, tt.want)
			}
		})
	}
}

func TestSwapDoesNotResolveConflict(t *testing.T) {
	if got := SwapTag(TagConflict); got != TagConflict {
		t.Fatalf("SwapTag(conflict) = %s, want conflict", got)
	}
	if got := SwapTag(TagBE); got != TagLE {
		t.Fatalf("SwapTag(be) = %s, want le", got)
	}
	if got := SwapTag(TagLE); got != TagBE {
		t.Fatalf("SwapTag(le) = %s, want be", got)
	}
}

func TestExplicitConversionResolvesConflict(t *testing.T) {
	if got := ConvertTag(TagConflict, TagBE); got != TagBE {
		t.Fatalf("ConvertTag(conflict, be) = %s, want be", got)
	}
	if got := ConvertTag(TagConflict, TagLE); got != TagLE {
		t.Fatalf("ConvertTag(conflict, le) = %s, want le", got)
	}
	if got := ConvertTag(TagUnknown, TagBE); got != TagUnknown {
		t.Fatalf("ConvertTag(unknown, be) = %s, want unknown", got)
	}
}

func TestTraceLinesKeepBothMergeArms(t *testing.T) {
	left := &Trace{Step: TraceStep{Op: OpParam, Tag: TagBE, Text: "v1 = param net_len"}}
	right := &Trace{Step: TraceStep{Op: OpParam, Tag: TagLE, Text: "v2 = param host_len"}}
	join := &Trace{
		Step:    TraceStep{Op: OpPhi, Tag: TagConflict, Text: "v3 = phi(v1, v2)"},
		Parents: []*Trace{left, right},
	}

	lines := join.Lines()
	if len(lines) != 3 {
		t.Fatalf("got %d trace lines, want 3:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "param net_len") || !strings.Contains(text, "param host_len") {
		t.Fatalf("trace lost a merge arm:\n%s", text)
	}
	if !strings.Contains(text, "conflict") {
		t.Fatalf("trace does not show the conflict label:\n%s", text)
	}
}
