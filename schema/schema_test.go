package schema

import (
	"strings"
	"testing"

	"pbufsim/wire"
)

// Order is a nested message used across the schema tests.
func orderDesc() *Message {
	return NewMessage("Order",
		Field{Num: 1, Name: "id", Kind: KindUint64, Required: true},
		Field{Num: 2, Name: "price", Kind: KindSint64, Min: Int64(0), Max: Int64(1_000_000)},
		Field{Num: 3, Name: "symbol", Kind: KindString, Required: true, MaxLen: 8},
	)
}

func tickDesc() *Message {
	return NewMessage("Tick",
		Field{Num: 1, Name: "ts", Kind: KindUint64, Required: true},
		Field{Num: 2, Name: "orders", Kind: KindMessage, Repeated: true, Msg: orderDesc()},
		Field{Num: 3, Name: "volumes", Kind: KindUint32, Repeated: true, Packed: true},
		Field{Num: 4, Name: "deltas", Kind: KindSint32, Repeated: true, Packed: true},
		Field{Num: 5, Name: "note", Kind: KindString},
		Field{Num: 6, Name: "active", Kind: KindBool},
	)
}

func encodeOrder(w *wire.Writer, id uint64, price int64, symbol string) {
	var o wire.Writer
	o.AppendTag(1, wire.Varint)
	o.AppendVarint(id)
	o.AppendTag(2, wire.Varint)
	o.AppendZigZag(price)
	o.AppendTag(3, wire.Bytes)
	o.AppendBytes([]byte(symbol))
	w.AppendTag(2, wire.Bytes)
	w.AppendBytes(o.Bytes())
}

func encodeTick() []byte {
	var w wire.Writer
	w.AppendTag(1, wire.Varint)
	w.AppendVarint(1_700_000_000)
	encodeOrder(&w, 7, 9950, "BTCUSD")
	encodeOrder(&w, 8, 12300, "ETHUSD")
	var packed wire.Writer
	for _, v := range []uint64{100, 200, 300} {
		packed.AppendVarint(v)
	}
	w.AppendTag(3, wire.Bytes)
	w.AppendBytes(packed.Bytes())
	var deltas wire.Writer
	for _, v := range []int64{-5, 0, 5} {
		deltas.AppendZigZag(v)
	}
	w.AppendTag(4, wire.Bytes)
	w.AppendBytes(deltas.Bytes())
	w.AppendTag(5, wire.Bytes)
	w.AppendBytes([]byte("close"))
	w.AppendTag(6, wire.Varint)
	w.AppendVarint(1)
	return w.Bytes()
}

func TestDecodeValidTick(t *testing.T) {
	eng := &Engine{}
	rec, vs, err := eng.Decode(tickDesc(), encodeTick())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("unexpected violations: %v", vs)
	}
	if v, _ := rec.Get("ts"); v != uint64(1_700_000_000) {
		t.Fatalf("ts: %v", v)
	}
	orders, _ := rec.Get("orders")
	list := orders.([]*Record)
	if len(list) != 2 {
		t.Fatalf("orders: %d", len(list))
	}
	o0 := list[0]
	if v, _ := o0.Get("id"); v != uint64(7) {
		t.Fatalf("order id: %v", v)
	}
	if v, _ := o0.Get("price"); v != int64(9950) {
		t.Fatalf("order price: %v", v)
	}
	if v, _ := o0.Get("symbol"); v != "BTCUSD" {
		t.Fatalf("order symbol: %v", v)
	}
	vols, _ := rec.Get("volumes")
	if got := vols.([]uint64); len(got) != 3 || got[0] != 100 || got[2] != 300 {
		t.Fatalf("volumes: %v", got)
	}
	deltas, _ := rec.Get("deltas")
	if got := deltas.([]int64); len(got) != 3 || got[0] != -5 || got[2] != 5 {
		t.Fatalf("deltas: %v", got)
	}
	if v, _ := rec.Get("active"); v != true {
		t.Fatalf("active: %v", v)
	}
}

func TestMissingRequired(t *testing.T) {
	var w wire.Writer
	w.AppendTag(5, wire.Bytes)
	w.AppendBytes([]byte("x")) // only optional note; ts missing
	_, vs, err := (&Engine{}).Decode(tickDesc(), w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(vs, "required") {
		t.Fatalf("want required violation, got %v", vs)
	}
}

func TestRangeViolationNested(t *testing.T) {
	var w wire.Writer
	w.AppendTag(1, wire.Varint)
	w.AppendVarint(1)
	encodeOrder(&w, 9, 2_000_000, "BTCUSD") // price above Max
	_, vs, err := (&Engine{}).Decode(tickDesc(), w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range vs {
		if v.Rule == "range" && strings.Contains(v.Path, "orders") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want nested range violation, got %v", vs)
	}
}

func TestWireTypeMismatch(t *testing.T) {
	var w wire.Writer
	w.AppendTag(1, wire.Fixed32) // ts declared varint
	w.AppendFixed32(1)
	_, vs, err := (&Engine{}).Decode(tickDesc(), w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(vs, "wire-type") {
		t.Fatalf("want wire-type violation, got %v", vs)
	}
}

func TestUnknownField(t *testing.T) {
	var w wire.Writer
	w.AppendTag(1, wire.Varint)
	w.AppendVarint(1)
	w.AppendTag(99, wire.Varint)
	w.AppendVarint(1)

	_, vs, err := (&Engine{}).Decode(tickDesc(), w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(vs, "unknown-field") {
		t.Fatalf("want unknown-field violation, got %v", vs)
	}

	// With AllowUnknownFields the same buffer is clean (apart from
	// nothing else being wrong).
	_, vs, err = (&Engine{AllowUnknownFields: true}).Decode(tickDesc(), w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("unexpected violations: %v", vs)
	}
}

func TestMaxLenString(t *testing.T) {
	var o wire.Writer
	o.AppendTag(1, wire.Varint)
	o.AppendVarint(1)
	o.AppendTag(3, wire.Bytes)
	o.AppendBytes([]byte("WAYTOOLONGSYMBOL"))
	_, vs, err := (&Engine{}).Decode(orderDesc(), o.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(vs, "max-len") {
		t.Fatalf("want max-len violation, got %v", vs)
	}
}

func TestMaxDepth(t *testing.T) {
	// Self-referential descriptor, nested 10 deep, limit 3.
	var self *Message
	self = NewMessage("Loop", Field{Num: 1, Name: "next", Kind: KindMessage, Msg: nil})
	self.Fields[0].Msg = self

	var buf []byte
	for i := 0; i < 10; i++ {
		var w wire.Writer
		w.AppendTag(1, wire.Bytes)
		w.AppendBytes(buf)
		buf = w.Bytes()
	}
	_, vs, err := (&Engine{MaxDepth: 3}).Decode(self, buf)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(vs, "max-depth") {
		t.Fatalf("want max-depth violation, got %v", vs)
	}
}

func TestTruncatedInputIsError(t *testing.T) {
	full := encodeTick()
	// Cut inside the last field's varint payload (tag present, value
	// missing).
	if _, _, err := (&Engine{}).Decode(tickDesc(), full[:len(full)-1]); err == nil {
		t.Fatal("truncated buffer decoded without error")
	}
	// Cut inside a nested order.
	var w wire.Writer
	w.AppendTag(1, wire.Varint)
	w.AppendVarint(1)
	var o wire.Writer
	o.AppendTag(3, wire.Bytes)
	o.AppendVarint(100) // declares 100 bytes, provides none
	w.AppendTag(2, wire.Bytes)
	w.AppendBytes(o.Bytes())
	if _, _, err := (&Engine{}).Decode(tickDesc(), w.Bytes()); err == nil {
		t.Fatal("truncated nested message decoded without error")
	}
}

func TestPackedTruncatedIsError(t *testing.T) {
	var w wire.Writer
	w.AppendTag(1, wire.Varint)
	w.AppendVarint(1)
	w.AppendTag(3, wire.Bytes)
	w.AppendBytes([]byte{0x80}) // packed varint missing its second byte
	if _, _, err := (&Engine{}).Decode(tickDesc(), w.Bytes()); err == nil {
		t.Fatal("truncated packed field decoded without error")
	}
}

func TestPackedFixedAndFloat(t *testing.T) {
	desc := NewMessage("M",
		Field{Num: 1, Name: "fx", Kind: KindFixed32, Repeated: true, Packed: true},
		Field{Num: 2, Name: "fl", Kind: KindFloat, Repeated: true, Packed: true},
		Field{Num: 3, Name: "db", Kind: KindDouble, Repeated: true, Packed: true},
		Field{Num: 4, Name: "b", Kind: KindBool, Repeated: true, Packed: true},
	)
	var w wire.Writer
	var fx wire.Writer
	fx.AppendFixed32(10)
	fx.AppendFixed32(20)
	w.AppendTag(1, wire.Bytes)
	w.AppendBytes(fx.Bytes())
	var fl wire.Writer
	fl.AppendFixed32(0x3f800000) // 1.0
	w.AppendTag(2, wire.Bytes)
	w.AppendBytes(fl.Bytes())
	var db wire.Writer
	db.AppendFixed64(0x3ff0000000000000) // 1.0
	w.AppendTag(3, wire.Bytes)
	w.AppendBytes(db.Bytes())
	var bb wire.Writer
	bb.AppendVarint(1)
	bb.AppendVarint(0)
	w.AppendTag(4, wire.Bytes)
	w.AppendBytes(bb.Bytes())

	rec, vs, err := (&Engine{}).Decode(desc, w.Bytes())
	if err != nil || len(vs) != 0 {
		t.Fatalf("err=%v vs=%v", err, vs)
	}
	if got, _ := rec.Get("fx"); got.([]uint32)[1] != 20 {
		t.Fatalf("fx: %v", got)
	}
	if got, _ := rec.Get("fl"); got.([]float32)[0] != 1.0 {
		t.Fatalf("fl: %v", got)
	}
	if got, _ := rec.Get("db"); got.([]float64)[0] != 1.0 {
		t.Fatalf("db: %v", got)
	}
	if got, _ := rec.Get("b"); got.([]bool)[0] != true || got.([]bool)[1] != false {
		t.Fatalf("b: %v", got)
	}
}

func TestRepeatedUnpackedScalars(t *testing.T) {
	desc := NewMessage("M", Field{Num: 1, Name: "v", Kind: KindInt64, Repeated: true})
	var w wire.Writer
	for _, v := range []uint64{3, 1, 4} {
		w.AppendTag(1, wire.Varint)
		w.AppendVarint(v)
	}
	rec, vs, err := (&Engine{}).Decode(desc, w.Bytes())
	if err != nil || len(vs) != 0 {
		t.Fatalf("err=%v vs=%v", err, vs)
	}
	got, _ := rec.Get("v")
	l := got.([]int64)
	if len(l) != 3 || l[0] != 3 || l[1] != 1 || l[2] != 4 {
		t.Fatalf("v: %v", l)
	}
}

func hasRule(vs []Violation, rule string) bool {
	for _, v := range vs {
		if v.Rule == rule {
			return true
		}
	}
	return false
}
