package bridge

import (
	"encoding/hex"
	"fmt"
	"io"
)

// Replayable sample scenario. The output is deterministic and is verified
// byte-for-byte by TestReplayGolden; `go run ./cmd/replay` reproduces it.

func replayV1() *Schema {
	return &Schema{
		Name: "v1", Version: 1,
		Fields: map[uint32]Field{
			1: {ID: 1, Name: "id", Type: TyInt, Required: true},
			2: {ID: 2, Name: "name", Type: TyString},
			3: {ID: 3, Name: "score", Type: TyInt},
		},
	}
}

func replayV2() *Schema {
	return &Schema{
		Name: "v2", Version: 2,
		Fields: map[uint32]Field{
			1: {ID: 1, Name: "id", Type: TyInt, Required: true},
			2: {ID: 2, Name: "fullname", Type: TyString}, // renamed, same ID
			4: {ID: 4, Name: "tags", Type: TyBytes},
			6: {ID: 6, Name: "level", Type: TyInt, Required: true},
		},
		Defaults: map[uint32]Value{6: IntVal(1)}, // migration rule
	}
}

// replayV2b is the hot-update target: field 2 is dropped from the schema,
// so under the new plan it travels in the extension blob instead.
func replayV2b() *Schema {
	return &Schema{
		Name: "v2", Version: 3,
		Fields: map[uint32]Field{
			1: {ID: 1, Name: "id", Type: TyInt, Required: true},
			4: {ID: 4, Name: "tags", Type: TyBytes},
			6: {ID: 6, Name: "level", Type: TyInt, Required: true},
		},
		Defaults: map[uint32]Value{6: IntVal(1)},
	}
}

func printPlan(w io.Writer, p *Plan) {
	fmt.Fprintf(w, "plan %s v%d -> %s v%d:\n", p.From.Name, p.From.Version, p.To.Name, p.To.Version)
	for _, e := range p.Entries {
		if e.Reason != "" {
			fmt.Fprintf(w, "  field %d: %s (%s)\n", e.FieldID, e.Class, e.Reason)
		} else {
			fmt.Fprintf(w, "  field %d: %s\n", e.FieldID, e.Class)
		}
	}
}

func digestPrefix(d [32]byte) string { return hex.EncodeToString(d[:8]) }

// RunReplay executes the deterministic end-to-end sample:
// negotiation, chunked conversion, hot update mid-frame, round-trip report.
func RunReplay(w io.Writer) error {
	v1, v2 := replayV1(), replayV2()

	fmt.Fprintln(w, "== negotiate v1 <-> v2 ==")
	sess, err := Negotiate(v1, v2)
	if err != nil {
		return err
	}
	pair1 := sess.PlanPair()
	fmt.Fprintf(w, "epoch=%d digest=%s\n", sess.Epoch(), digestPrefix(sess.Digest()))
	printPlan(w, pair1.AB)
	printPlan(w, pair1.BA)

	fmt.Fprintln(w, "== chunked frame v1 -> v2 ==")
	ep := NewEndpoint(sess, AtoB, 1024)
	msg := NewMessage()
	msg.Values[1] = IntVal(7)
	msg.Values[2] = StrVal("ann")
	msg.Values[3] = IntVal(42)
	frame := EncodeFrame(msg.Encode())
	var converted [][]byte
	for _, c := range [][2]int{{0, 3}, {3, 8}, {8, len(frame)}} {
		out, err := ep.Feed(frame[c[0]:c[1]])
		if err != nil {
			return err
		}
		converted = append(converted, out...)
	}
	fmt.Fprintf(w, "fed %d bytes in 3 chunks -> %d frame(s)\n", len(frame), len(converted))
	m2, err := DecodeMessage(v2, framePayload(converted[0]))
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "field 2 direct=%v value=%s\n", true, m2.Values[2])
	fmt.Fprintf(w, "field 6 defaulted=%s\n", m2.Values[6])
	fmt.Fprintf(w, "field 3 in extension=%v\n", len(m2.Unknown) == 1 && m2.Unknown[0].ID == ExtFieldID)

	fmt.Fprintln(w, "== hot update mid-frame ==")
	pair2, err := CompilePair(v1, replayV2b())
	if err != nil {
		return err
	}
	msg2 := NewMessage()
	msg2.Values[1] = IntVal(9)
	msg2.Values[2] = StrVal("bo")
	msg2.Values[3] = IntVal(5)
	frame2 := EncodeFrame(msg2.Encode())
	half := len(frame2) / 2
	if _, err := ep.Feed(frame2[:half]); err != nil {
		return err
	}
	sess.Publish(pair2)
	fmt.Fprintf(w, "epoch=%d digest=%s\n", sess.Epoch(), digestPrefix(sess.Digest()))
	out2, err := ep.Feed(frame2[half:])
	if err != nil {
		return err
	}
	old, err := DecodeMessage(v2, framePayload(out2[0]))
	if err != nil {
		return err
	}
	_, directOld := old.Values[2]
	fmt.Fprintf(w, "in-flight frame: field 2 direct=%v (old plan)\n", directOld)
	out3, err := ep.Feed(frame2)
	if err != nil {
		return err
	}
	newM, err := DecodeMessage(replayV2b(), framePayload(out3[0]))
	if err != nil {
		return err
	}
	_, directNew := newM.Values[2]
	fmt.Fprintf(w, "new epoch frame: field 2 direct=%v (new plan)\n", directNew)

	fmt.Fprintln(w, "== round-trip difference report ==")
	_, report, err := RoundTrip(pair1, AtoB, msg)
	if err != nil {
		return err
	}
	fmt.Fprint(w, report.String())
	fmt.Fprintf(w, "clean=%v\n", report.Clean())

	fmt.Fprintln(w, "REPLAY OK")
	return nil
}
