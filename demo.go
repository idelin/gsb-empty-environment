package bridge

import (
	"errors"
	"fmt"
	"strings"
)

// DemoSchemas returns the sample v1/v2 schema pair used by the replayable
// demo and the tests.
//
//	v1: 1 id(varint,req) 2 name(string) 3 score(varint) 15 ext(carrier)
//	v2: 1 id(varint,req) 2 full_name(string, renamed) 3 score(string, retyped)
//	    5 email(string, new) 6 level(varint, req, default 7) 15 ext(carrier)
func DemoSchemas() (*Schema, *Schema, error) {
	s1, err := NewSchema(1, []FieldDef{
		{ID: 1, Name: "id", Type: TVarint, Required: true},
		{ID: 2, Name: "name", Type: TString},
		{ID: 3, Name: "score", Type: TVarint},
		{ID: 15, Name: "ext", Type: TBytes, Extension: true},
	})
	if err != nil {
		return nil, nil, err
	}
	s2, err := NewSchema(2, []FieldDef{
		{ID: 1, Name: "id", Type: TVarint, Required: true},
		{ID: 2, Name: "full_name", Type: TString},
		{ID: 3, Name: "score", Type: TString},
		{ID: 5, Name: "email", Type: TString},
		{ID: 6, Name: "level", Type: TVarint, Required: true,
			Rule: Rule{HasDefault: true, DefaultNum: 7}},
		{ID: 15, Name: "ext", Type: TBytes, Extension: true},
	})
	if err != nil {
		return nil, nil, err
	}
	return s1, s2, nil
}

// Demo runs the replayable end-to-end sample and returns a deterministic log.
func Demo() string {
	var log strings.Builder
	say := func(format string, args ...any) { fmt.Fprintf(&log, format+"\n", args...) }

	s1, s2, err := DemoSchemas()
	if err != nil {
		say("schema error: %v", err)
		return log.String()
	}
	ps, err := NewPlanSet(s1, s2)
	if err != nil {
		say("plan error: %v", err)
		return log.String()
	}
	bridge := NewBridge(ps)
	lim := DefaultLimits()

	say("== plan compilation ==")
	for _, p := range []*Plan{ps.Fwd, ps.Rev} {
		l, e, u := p.Classification()
		say("plan v%d->v%d lossless=%v preserve-extension=%v unrepresentable=%v",
			p.From, p.To, l, e, u)
	}
	say("plan-set digest=%x", first8(ps.Digest()))

	say("== negotiation ==")
	bind := bridge.Bind()
	if err := bind.Negotiate(ps.Digest()); err != nil {
		say("negotiation failed: %v", err)
	} else {
		say("negotiation ok: epoch=%d digest=%x", bind.Epoch, first8(bind.Digest()))
	}
	if err := bind.Negotiate([32]byte{0xde, 0xad}); err != nil {
		say("foreign peer refused: %v", err)
	}

	say("== v1 -> v2 with unknown field 99 ==")
	v1frame := &Frame{Version: 1, Fields: []Field{
		{ID: 1, Kind: KindVarint, Num: 42},
		{ID: 2, Kind: KindBytes, Raw: []byte("alice")},
		{ID: 3, Kind: KindVarint, Num: 900},
		{ID: 99, Kind: KindBytes, Raw: []byte{0xca, 0xfe}},
	}}
	var v2frame *Frame
	bind.Convert(v1frame, lim, func(f *Frame, r *Report) {
		v2frame = f
		say("report: %s", r)
	})
	say("v2 frame: %s", frameString(v2frame))

	say("== migration ring v2 -> v1 -> v2 (unknown field 99 must survive) ==")
	var back1, back2 *Frame
	bind.Convert(v2frame, lim, func(f *Frame, r *Report) {
		back1 = f
		say("v2->v1 report: %s", r)
	})
	bind.Convert(back1, lim, func(f *Frame, r *Report) {
		back2 = f
		say("v1->v2 report: %s", r)
	})
	say("diff(v2, ring v2->v1->v2): %s", FormatDiff(Diff(v2frame, back2)))

	say("== hot reload during a chunked frame ==")
	s2v2, _ := NewSchema(2, []FieldDef{
		{ID: 1, Name: "id", Type: TVarint, Required: true},
		{ID: 2, Name: "full_name", Type: TString},
		{ID: 3, Name: "score", Type: TString},
		{ID: 5, Name: "email", Type: TString},
		{ID: 6, Name: "level", Type: TVarint, Required: true,
			Rule: Rule{HasDefault: true, DefaultNum: 7}},
		{ID: 8, Name: "tags", Type: TString},
		{ID: 15, Name: "ext", Type: TBytes, Extension: true},
	})
	ps2, err := NewPlanSet(s1, s2v2)
	if err != nil {
		say("plan error: %v", err)
		return log.String()
	}
	wire := EncodeFrame(1, v1frame.Fields)
	recv := NewReceiver(bridge, lim)
	half := len(wire) / 2
	var got *Frame
	var gotRep *Report
	sink := func(f *Frame, r *Report) { got, gotRep = f, r }
	if err := recv.Feed(wire[:half], sink); err != nil {
		say("feed error: %v", err)
	}
	newEpoch := bridge.HotReload(ps2)
	say("reloaded plans: new epoch=%d digest=%x", newEpoch, first8(ps2.Digest()))
	if err := recv.Feed(wire[half:], sink); err != nil {
		say("feed error: %v", err)
	}
	say("in-flight frame converted with epoch=%d (bridge now at epoch=%d)", gotRep.Epoch, bridge.Epoch())
	say("old-epoch report: %s", gotRep)
	say("old-epoch result: %s", frameString(got))

	say("== failure publishes nothing ==")
	broken := &Frame{Version: 1, Fields: []Field{{ID: 2, Kind: KindBytes, Raw: []byte("no-id")}}}
	published := false
	err = bridge.Bind().Convert(broken, lim, func(*Frame, *Report) { published = true })
	say("convert error: %v", err)
	say("sink called: %v", published)

	say("== strict v2 refuses negotiation ==")
	s2strict, _ := NewSchema(2, []FieldDef{
		{ID: 1, Name: "id", Type: TVarint, Required: true},
		{ID: 9, Name: "tenant", Type: TVarint, Required: true},
	})
	if _, err := NewPlanSet(s1, s2strict); err != nil {
		say("plan set refused: %v", err)
	}

	say("== chunked parse: need-more vs bad data ==")
	p := NewParser(lim)
	_, _, err = p.Next()
	say("empty parser: need-more=%v", errors.Is(err, ErrNeedMoreData))
	if err := p.Feed([]byte{9, 0, 0, 0, 1}); err != nil {
		say("bad version: %v", err)
	}
	return log.String()
}

func frameString(f *Frame) string {
	if f == nil {
		return "<nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "v%d epoch=%d [", f.Version, f.Epoch)
	for i, fl := range f.Fields {
		if i > 0 {
			b.WriteString(" ")
		}
		if fl.Kind == KindVarint {
			fmt.Fprintf(&b, "%d:varint(%d)", fl.ID, fl.Num)
		} else {
			fmt.Fprintf(&b, "%d:bytes(%x)", fl.ID, fl.Raw)
		}
	}
	b.WriteString("]")
	return b.String()
}

func first8(sum [32]byte) []byte { return sum[:8] }
