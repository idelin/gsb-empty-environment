package netsec

import (
	"strings"
	"testing"
)

// msgLayout is a flat message object: a plain header followed by a union whose
// members have different sizes (4, 2 and 8 bytes).
func msgLayout() Field {
	return Field{
		Name:   "msg",
		Kind:   Struct,
		Offset: 0,
		Size:   12,
		Children: []Field{
			{Name: "hdr", Kind: Leaf, Offset: 0, Size: 4},
			{
				Name:   "body",
				Kind:   Union,
				Offset: 4,
				Size:   8,
				Children: []Field{
					{Name: "u32v", Kind: Leaf, Offset: 4, Size: 4},
					{Name: "u16v", Kind: Leaf, Offset: 4, Size: 2},
					{Name: "u64v", Kind: Leaf, Offset: 4, Size: 8},
				},
			},
		},
	}
}

func assertResiduals(t *testing.T, report Report, want []Residual) {
	t.Helper()
	if len(report.Residuals) != len(want) {
		t.Fatalf("residual count = %d, want %d: %v", len(report.Residuals), len(want), report.Residuals)
	}
	for i := range want {
		got := report.Residuals[i]
		if got.Offset != want[i].Offset || got.Length != want[i].Length ||
			got.Reason != want[i].Reason || got.Source != want[i].Source {
			t.Fatalf("residual[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestDifferentSizedMembersLargeToSmall(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	if err := mon.Switch("body", "body.u64v"); err != nil {
		t.Fatalf("switch u64v: %v", err)
	}
	if err := mon.Write("body.u64v", 0, 8); err != nil {
		t.Fatalf("write u64v: %v", err)
	}

	// Switching to the smaller member must not clear or initialize anything.
	if err := mon.Switch("body", "body.u16v"); err != nil {
		t.Fatalf("switch u16v: %v", err)
	}

	// The two bytes still carry the previous member's provenance: the switch
	// alone does not make them current for u16v.
	report, err := mon.Send("body.u16v", 0, 2)
	if err != nil {
		t.Fatalf("send u16v: %v", err)
	}
	want := []Residual{{Offset: 4, Length: 2, Reason: StaleMember, Source: "msg.body.u64v"}}
	assertResiduals(t, report, want)

	// Rewriting through the active member gives the bytes current provenance.
	if err := mon.Write("body.u16v", 0, 2); err != nil {
		t.Fatalf("rewrite u16v: %v", err)
	}
	report, err = mon.Send("body.u16v", 0, 2)
	if err != nil {
		t.Fatalf("resend u16v: %v", err)
	}
	if !report.Clean {
		t.Fatalf("rewritten smaller member should be clean, got residuals: %v", report.Residuals)
	}
}

func TestDifferentSizedMembersSmallToLarge(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	if err := mon.Switch("body", "body.u16v"); err != nil {
		t.Fatalf("switch u16v: %v", err)
	}
	if err := mon.Write("body.u16v", 0, 2); err != nil {
		t.Fatalf("write u16v: %v", err)
	}
	if err := mon.Switch("body", "body.u64v"); err != nil {
		t.Fatalf("switch u64v: %v", err)
	}

	report, err := mon.Send("body.u64v", 0, 8)
	if err != nil {
		t.Fatalf("send u64v: %v", err)
	}
	if report.Clean {
		t.Fatalf("send of uninitialized tail should not be clean")
	}
	want := []Residual{
		{Offset: 4, Length: 2, Reason: StaleMember, Source: "msg.body.u16v"},
		{Offset: 6, Length: 6, Reason: NeverWritten},
	}
	assertResiduals(t, report, want)
}

func TestResidualSourceFromPreviousMember(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	if err := mon.Switch("body", "body.u32v"); err != nil {
		t.Fatalf("switch u32v: %v", err)
	}
	if err := mon.Write("body.u32v", 0, 4); err != nil {
		t.Fatalf("write u32v: %v", err)
	}
	if err := mon.Switch("body", "body.u64v"); err != nil {
		t.Fatalf("switch u64v: %v", err)
	}
	// Initialize the tail only; the first four bytes still come from u32v.
	if err := mon.Write("body.u64v", 4, 4); err != nil {
		t.Fatalf("write u64v tail: %v", err)
	}

	report, err := mon.Send("body.u64v", 0, 8)
	if err != nil {
		t.Fatalf("send u64v: %v", err)
	}
	if report.Clean {
		t.Fatalf("send containing stale member bytes should not be clean")
	}
	want := []Residual{{Offset: 4, Length: 4, Reason: StaleMember, Source: "msg.body.u32v"}}
	assertResiduals(t, report, want)

	if report.ResidualBytes() != 4 {
		t.Fatalf("ResidualBytes = %d, want 4", report.ResidualBytes())
	}
}

// nestedLayout places an inner union inside one member of an outer union.
func nestedLayout() Field {
	return Field{
		Name:   "pkt",
		Kind:   Struct,
		Offset: 0,
		Size:   8,
		Children: []Field{
			{
				Name:   "outer",
				Kind:   Union,
				Offset: 0,
				Size:   8,
				Children: []Field{
					{
						Name:   "wide",
						Kind:   Struct,
						Offset: 0,
						Size:   8,
						Children: []Field{
							{Name: "lo", Kind: Leaf, Offset: 0, Size: 4},
							{
								Name:   "inner",
								Kind:   Union,
								Offset: 4,
								Size:   4,
								Children: []Field{
									{Name: "a", Kind: Leaf, Offset: 4, Size: 2},
									{Name: "b", Kind: Leaf, Offset: 4, Size: 4},
								},
							},
						},
					},
					{Name: "flat", Kind: Leaf, Offset: 0, Size: 8},
				},
			},
		},
	}
}

func TestNestedUnionStaleSource(t *testing.T) {
	mon, err := NewMonitor(nestedLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	if err := mon.Switch("outer", "outer.wide"); err != nil {
		t.Fatalf("switch wide: %v", err)
	}
	if err := mon.Switch("outer.wide.inner", "outer.wide.inner.a"); err != nil {
		t.Fatalf("switch inner.a: %v", err)
	}
	if err := mon.Write("outer.wide.lo", 0, 4); err != nil {
		t.Fatalf("write lo: %v", err)
	}
	if err := mon.Write("outer.wide.inner.a", 0, 2); err != nil {
		t.Fatalf("write inner.a: %v", err)
	}

	// Swapping the inner member leaves the two bytes written through inner.a
	// behind; inner.b is four bytes wide and needs the rest initialized too.
	if err := mon.Switch("outer.wide.inner", "outer.wide.inner.b"); err != nil {
		t.Fatalf("switch inner.b: %v", err)
	}
	if err := mon.Write("outer.wide.inner.b", 2, 2); err != nil {
		t.Fatalf("write inner.b tail: %v", err)
	}

	report, err := mon.Send("outer.wide.inner.b", 0, 4)
	if err != nil {
		t.Fatalf("send inner.b: %v", err)
	}
	want := []Residual{{Offset: 4, Length: 2, Reason: StaleMember, Source: "pkt.outer.wide.inner.a"}}
	assertResiduals(t, report, want)
}

func TestNestedUnionOuterSwitchInvalidatesInnerWrites(t *testing.T) {
	mon, err := NewMonitor(nestedLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	if err := mon.Switch("outer", "outer.wide"); err != nil {
		t.Fatalf("switch wide: %v", err)
	}
	if err := mon.Switch("outer.wide.inner", "outer.wide.inner.a"); err != nil {
		t.Fatalf("switch inner.a: %v", err)
	}
	if err := mon.Write("outer.wide.inner.a", 0, 2); err != nil {
		t.Fatalf("write inner.a: %v", err)
	}

	if err := mon.Switch("outer", "outer.flat"); err != nil {
		t.Fatalf("switch flat: %v", err)
	}
	report, err := mon.Send("outer.flat", 0, 8)
	if err != nil {
		t.Fatalf("send flat: %v", err)
	}
	want := []Residual{
		{Offset: 0, Length: 4, Reason: NeverWritten},
		{Offset: 4, Length: 2, Reason: StaleMember, Source: "pkt.outer.wide.inner.a"},
		{Offset: 6, Length: 2, Reason: NeverWritten},
	}
	assertResiduals(t, report, want)

	// Returning to the earlier outer member restores a reachable provenance for
	// the bytes it wrote without rewriting anything.
	if err := mon.Switch("outer", "outer.wide"); err != nil {
		t.Fatalf("switch back to wide: %v", err)
	}
	report, err = mon.Send("outer.wide.inner.a", 0, 2)
	if err != nil {
		t.Fatalf("send inner.a again: %v", err)
	}
	if !report.Clean {
		t.Fatalf("re-selected member should be clean again: %v", report.Residuals)
	}
}

func TestPartialFieldCoverage(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	if err := mon.Switch("body", "body.u32v"); err != nil {
		t.Fatalf("switch u32v: %v", err)
	}
	// Only the middle two bytes of the four-byte member are initialized.
	if err := mon.Write("body.u32v", 1, 2); err != nil {
		t.Fatalf("partial write: %v", err)
	}

	report, err := mon.Send("body.u32v", 0, 4)
	if err != nil {
		t.Fatalf("send u32v: %v", err)
	}
	want := []Residual{
		{Offset: 4, Length: 1, Reason: NeverWritten},
		{Offset: 7, Length: 1, Reason: NeverWritten},
	}
	assertResiduals(t, report, want)

	// A send limited to the covered sub-range is clean.
	clean, err := mon.Send("body.u32v", 1, 2)
	if err != nil {
		t.Fatalf("partial send: %v", err)
	}
	if !clean.Clean {
		t.Fatalf("covered sub-range should be clean: %v", clean.Residuals)
	}
}

func TestRootSendAndPlainStructFields(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	if err := mon.Switch("body", "body.u16v"); err != nil {
		t.Fatalf("switch u16v: %v", err)
	}
	if err := mon.Write("hdr", 0, 4); err != nil {
		t.Fatalf("write hdr: %v", err)
	}
	if err := mon.Write("body.u16v", 0, 2); err != nil {
		t.Fatalf("write u16v: %v", err)
	}

	report, err := mon.Send("msg", 0, 12)
	if err != nil {
		t.Fatalf("send whole message: %v", err)
	}
	// Header and the active two-byte member are clean; the remaining six union
	// bytes were never touched.
	want := []Residual{{Offset: 6, Length: 6, Reason: NeverWritten}}
	assertResiduals(t, report, want)

	written, err := mon.InitializedSpan("body", 0, 8)
	if err != nil {
		t.Fatalf("InitializedSpan: %v", err)
	}
	if written != 2 {
		t.Fatalf("InitializedSpan = %d, want 2", written)
	}
}

func TestSwitchBackDoesNotReworkProvenance(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	if err := mon.Switch("body", "body.u32v"); err != nil {
		t.Fatalf("switch u32v: %v", err)
	}
	if err := mon.Write("body.u32v", 0, 4); err != nil {
		t.Fatalf("write u32v: %v", err)
	}
	if err := mon.Switch("body", "body.u16v"); err != nil {
		t.Fatalf("switch u16v: %v", err)
	}
	// Overwrite just the first byte through the new active member.
	if err := mon.Write("body.u16v", 0, 1); err != nil {
		t.Fatalf("write u16v byte: %v", err)
	}
	if err := mon.Switch("body", "body.u32v"); err != nil {
		t.Fatalf("switch back u32v: %v", err)
	}

	report, err := mon.Send("body.u32v", 0, 4)
	if err != nil {
		t.Fatalf("send u32v: %v", err)
	}
	// Byte 0 was rewritten through u16v, so its snapshot is stale for u32v;
	// the remaining three bytes still carry the original u32v provenance.
	want := []Residual{{Offset: 4, Length: 1, Reason: StaleMember, Source: "msg.body.u16v"}}
	assertResiduals(t, report, want)
}

func TestRejectsInactiveAndUnswitchedSends(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	// No active member yet.
	if _, err := mon.Send("body.u32v", 0, 4); err == nil ||
		!strings.Contains(err.Error(), "no active member") {
		t.Fatalf("expected no-active-member error, got %v", err)
	}

	if err := mon.Switch("body", "body.u16v"); err != nil {
		t.Fatalf("switch u16v: %v", err)
	}
	if _, err := mon.Send("body.u32v", 0, 4); err == nil ||
		!strings.Contains(err.Error(), "inactive member") {
		t.Fatalf("expected inactive-member error, got %v", err)
	}

	// Writes must also go through the currently active member.
	if err := mon.Write("body.u32v", 0, 4); err == nil ||
		!strings.Contains(err.Error(), "inactive member") {
		t.Fatalf("expected inactive-member write error, got %v", err)
	}
}

func TestRejectsBadInputs(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}

	if err := mon.Switch("body", "hdr"); err == nil {
		t.Fatal("switch to a non-child member must fail")
	}
	if err := mon.Switch("hdr", "hdr"); err == nil {
		t.Fatal("switch on a leaf must fail")
	}
	if err := mon.Write("body.u32v", 0, 5); err == nil {
		t.Fatal("write overflowing the field must fail")
	}
	if err := mon.Write("missing.field", 0, 1); err == nil {
		t.Fatal("write to an unknown path must fail")
	}
	if err := mon.Write("hdr", 0, 0); err == nil {
		t.Fatal("zero-length write must fail")
	}
	if _, err := mon.Send("hdr", 0, 5); err == nil {
		t.Fatal("send overflowing the field must fail")
	}
}

func TestLayoutValidation(t *testing.T) {
	cases := map[string]Field{
		"root offset not zero": {Name: "r", Kind: Leaf, Offset: 1, Size: 2},
		"empty name":           {Name: "", Kind: Leaf, Offset: 0, Size: 2},
		"non-positive size":    {Name: "r", Kind: Leaf, Offset: 0, Size: 0},
		"leaf with children": {
			Name: "r", Kind: Leaf, Offset: 0, Size: 4,
			Children: []Field{{Name: "c", Kind: Leaf, Offset: 0, Size: 1}},
		},
		"struct overlap": {
			Name: "r", Kind: Struct, Offset: 0, Size: 4,
			Children: []Field{
				{Name: "a", Kind: Leaf, Offset: 0, Size: 2},
				{Name: "b", Kind: Leaf, Offset: 1, Size: 2},
			},
		},
		"child overflow": {
			Name: "r", Kind: Struct, Offset: 0, Size: 2,
			Children: []Field{{Name: "a", Kind: Leaf, Offset: 0, Size: 3}},
		},
		"union member wrong offset": {
			Name: "r", Kind: Union, Offset: 0, Size: 4,
			Children: []Field{
				{Name: "a", Kind: Leaf, Offset: 0, Size: 2},
				{Name: "b", Kind: Leaf, Offset: 2, Size: 2},
			},
		},
	}
	for name, layout := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewMonitor(layout); err == nil {
				t.Fatalf("NewMonitor(%s) expected error, got nil", name)
			}
		})
	}
}

func TestEventLogOrdering(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	if err := mon.Switch("body", "body.u32v"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if err := mon.Write("body.u32v", 0, 4); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := mon.Send("body.u32v", 0, 4); err != nil {
		t.Fatalf("send: %v", err)
	}

	events := mon.Events()
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	wantKinds := []EventKind{EventSwitch, EventWrite, EventSend}
	for i, kind := range wantKinds {
		if events[i].Kind != kind || events[i].Seq != int64(i+1) {
			t.Fatalf("event[%d] = %+v, want kind %s seq %d", i, events[i], kind, i+1)
		}
	}

	// Returned slice is a copy and must not alias monitor state.
	events[0] = Event{}
	if mon.Events()[0].Kind != EventSwitch {
		t.Fatal("Events() must return a defensive copy")
	}
}

func TestConcurrentAccess(t *testing.T) {
	mon, err := NewMonitor(msgLayout())
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	if err := mon.Switch("body", "body.u32v"); err != nil {
		t.Fatalf("switch: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = mon.Write("body.u32v", 0, 4)
			_, _ = mon.Send("body.u32v", 0, 4)
		}
	}()
	for i := 0; i < 200; i++ {
		_ = mon.Events()
		_ = mon.Size()
		_, _ = mon.InitializedSpan("body.u32v", 0, 4)
	}
	<-done
}
