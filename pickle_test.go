package netsec

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

// 合法嵌套结构（协议 2）：{"a": [1, (2, "x"), None], "b": {"c": "y"}}
var nestedPickle = []byte{
	0x80, 0x02, // PROTO 2
	'}', '(',
	'X', 1, 0, 0, 0, 'a',
	']', '(',
	'K', 1,
	'(', 'K', 2, 'X', 1, 0, 0, 0, 'x', 't',
	'N',
	'e',
	'X', 1, 0, 0, 0, 'b',
	'}', '(', 'X', 1, 0, 0, 0, 'c', 'X', 1, 0, 0, 0, 'y', 'u',
	'u',
	'.',
}

func TestUnmarshalNestedStructure(t *testing.T) {
	got, err := Unmarshal(nestedPickle)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := map[interface{}]interface{}{
		"a": []interface{}{int64(1), Tuple{int64(2), "x"}, nil},
		"b": map[interface{}]interface{}{"c": "y"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestUnmarshalScalars(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want interface{}
	}{
		{"none", []byte{0x80, 0x02, 'N', '.'}, nil},
		{"binint1", []byte{0x80, 0x02, 'K', 200, '.'}, int64(200)},
		{"binint2", []byte{0x80, 0x02, 'M', 0x00, 0x01, '.'}, int64(256)},
		{"binint negative", []byte{0x80, 0x02, 'J', 0x00, 0xe2, 0xff, 0xff, '.'}, int64(-7680)},
		{"binunicode", []byte{0x80, 0x02, 'X', 3, 0, 0, 0, 'f', 'o', 'o', '.'}, "foo"},
		{"empty tuple", []byte{0x80, 0x02, ')', '.'}, Tuple{}},
		{"empty list", []byte{0x80, 0x02, ']', '.'}, []interface{}{}},
		{"empty dict", []byte{0x80, 0x02, '}', '.'}, map[interface{}]interface{}{}},
		{"append", []byte{0x80, 0x02, ']', 'K', 7, 'a', '.'}, []interface{}{int64(7)}},
		{"setitem", []byte{0x80, 0x02, '}', 'K', 1, 'K', 2, 's', '.'}, map[interface{}]interface{}{int64(1): int64(2)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Unmarshal(tc.data)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// 协议 4 风格：FRAME + SHORT_BINUNICODE + MEMOIZE + BINGET。
func TestUnmarshalFrameAndMemoize(t *testing.T) {
	data := []byte{
		0x80, 0x04, // PROTO 4
		0x95, 9, 0, 0, 0, 0, 0, 0, 0, // FRAME len=9
		0x8c, 3, 'a', 'b', 'c', // SHORT_BINUNICODE "abc"
		0x94,   // MEMOIZE
		'h', 0, // BINGET 0
		'.',
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != "abc" {
		t.Fatalf("got %#v, want \"abc\"", got)
	}
}

// BINPUT/BINGET 的 memo 别名：对同一列表的引用应看到后续追加。
func TestUnmarshalMemoAliasing(t *testing.T) {
	data := []byte{
		0x80, 0x02,
		']', 'q', 0, // list; BINPUT 0
		'(', 'K', 1, 'e', // APPENDS [1]
		'h', 0, 'K', 2, 'a', // BINGET 0; APPEND 2
		'h', 0, // BINGET 0
		'.',
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []interface{}{int64(1), int64(2)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRejectDangerousOpcodes(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		// 经典 REDUCE 反弹 shell 载荷：cposix\nsystem\n(S'...'\ntR.
		{"global reduce payload", []byte("cposix\nsystem\n(S'id'\ntR.")},
		{"reduce", []byte{0x80, 0x02, 'R', '.'}},
		{"global", []byte{0x80, 0x02, 'c', 'o', 's', '\n', 's', 'y', 's', 't', 'e', 'm', '\n', '.'}},
		{"stack_global", []byte{0x80, 0x04, 0x8c, 0x02, 'o', 's', 0x8c, 0x06, 's', 'y', 's', 't', 'e', 'm', 0x93, '.'}},
		{"build", []byte{0x80, 0x02, '}', 'K', 1, 'K', 2, 's', 'b', '.'}},
		{"inst", []byte{0x80, 0x02, 'i', 'o', 's', '\n', 's', 'y', 's', 't', 'e', 'm', '\n', '.'}},
		{"obj", []byte{0x80, 0x02, '(', 'o', '.'}},
		{"persid", []byte{0x80, 0x02, 'P', 'x', '\n', '.'}},
		{"binpersid", []byte{0x80, 0x02, 'K', 1, 'Q', '.'}},
		{"newobj", []byte{0x80, 0x02, '(', 0x81, '.'}},
		{"ext", []byte{0x80, 0x02, 0x82, 1, '.'}},
		{"long1", []byte{0x80, 0x02, 0x8a, 1, 0x7f, '.'}},
		{"string opcode", []byte("S'x'\n.")},
		{"int opcode", []byte("I1\n.")},
		{"protocol 1 int", []byte{0x80, 0x01, 'K', 1, '.'}},
		{"protocol 6", []byte{0x80, 0x06, 'N', '.'}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := Unmarshal(tc.data)
			if err == nil {
				t.Fatalf("expected rejection, got value %#v", v)
			}
			if !errors.Is(err, ErrUnsupportedOpcode) && !errors.Is(err, ErrUnsupportedProtocol) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRejectUndefinedMemo(t *testing.T) {
	for _, data := range [][]byte{
		{0x80, 0x02, 'h', 0, '.'},                 // BINGET 0 未定义
		{0x80, 0x02, 'K', 1, 'q', 3, 'h', 4, '.'}, // 只定义了 3，引用 4
	} {
		if _, err := Unmarshal(data); !errors.Is(err, ErrUndefinedMemo) {
			t.Fatalf("data %x: got %v, want ErrUndefinedMemo", data, err)
		}
	}
}

func nestedListPickle(depth int) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x80, 0x02})
	b.Write(bytes.Repeat([]byte{']'}, depth))
	b.Write(bytes.Repeat([]byte{'a'}, depth-1))
	b.WriteByte('.')
	return b.Bytes()
}

func TestDepthLimit(t *testing.T) {
	if _, err := Unmarshal(nestedListPickle(32)); err != nil {
		t.Fatalf("depth 32 should be accepted: %v", err)
	}
	if _, err := Unmarshal(nestedListPickle(33)); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("depth 33: got %v, want ErrDepthLimit", err)
	}
	// 循环引用（list 包含自身）必须被深度上限拦截，而不是死循环。
	cyclic := []byte{0x80, 0x02, ']', 'q', 0, 'h', 0, 'a', '.'}
	if _, err := Unmarshal(cyclic); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("cyclic: got %v, want ErrDepthLimit", err)
	}
}

func TestStackLimit(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{0x80, 0x02, '('})
	b.Write(bytes.Repeat([]byte{'N'}, maxStackSize+1))
	b.WriteByte('.')
	if _, err := Unmarshal(b.Bytes()); !errors.Is(err, ErrStackLimit) {
		t.Fatalf("got %v, want ErrStackLimit", err)
	}
}

func TestMemoLimit(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{0x80, 0x04})
	b.WriteByte('N')
	b.Write(bytes.Repeat([]byte{0x94}, maxMemoSize+1))
	b.WriteByte('.')
	if _, err := Unmarshal(b.Bytes()); !errors.Is(err, ErrMemoLimit) {
		t.Fatalf("got %v, want ErrMemoLimit", err)
	}
}

func TestRejectTrailingBytes(t *testing.T) {
	for _, data := range [][]byte{
		{0x80, 0x02, 'N', '.', '.'},      // STOP 后多一个 STOP
		{0x80, 0x02, 'N', '.', 'N', '.'}, // STOP 后还有完整数据
	} {
		if _, err := Unmarshal(data); !errors.Is(err, ErrTrailingData) {
			t.Fatalf("data %x: got %v, want ErrTrailingData", data, err)
		}
	}
}

func TestRejectMalformed(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"no stop", []byte{0x80, 0x02, 'N'}},
		{"truncated binunicode", []byte{0x80, 0x02, 'X', 5, 0, 0, 0, 'a', 'b', '.'}},
		{"truncated frame", []byte{0x80, 0x04, 0x95, 1, 0}},
		{"frame too long", []byte{0x80, 0x04, 0x95, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 'N', '.'}},
		{"stack underflow", []byte{0x80, 0x02, 'a', '.'}},
		{"mark not found", []byte{0x80, 0x02, 'K', 1, 't', '.'}},
		{"append to non-list", []byte{0x80, 0x02, 'N', 'K', 1, 'a', '.'}},
		{"setitem to non-dict", []byte{0x80, 0x02, 'N', 'K', 1, 'K', 2, 's', '.'}},
		{"odd setitems", []byte{0x80, 0x02, '}', '(', 'K', 1, 'u', '.'}},
		{"unhashable key", []byte{0x80, 0x02, '}', ']', 'K', 1, 's', '.'}},
		{"empty stack at stop", []byte{0x80, 0x02, '.'}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if v, err := Unmarshal(tc.data); err == nil {
				t.Fatalf("expected error, got value %#v", v)
			}
		})
	}
}

func TestProtocolVersionsAccepted(t *testing.T) {
	for proto := byte(2); proto <= 5; proto++ {
		data := []byte{0x80, proto, 'N', '.'}
		if _, err := Unmarshal(data); err != nil {
			t.Fatalf("protocol %d should be accepted: %v", proto, err)
		}
	}
}

func TestUnmarshalLargeValidInput(t *testing.T) {
	// 接近上限的合法输入应可解码：1000 个元素的列表。
	var b bytes.Buffer
	b.Write([]byte{0x80, 0x02, ']', '('})
	for i := 0; i < 1000; i++ {
		b.Write([]byte{'K', byte(i % 256)})
	}
	b.Write([]byte{'e', '.'})
	got, err := Unmarshal(b.Bytes())
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if lst, ok := got.([]interface{}); !ok || len(lst) != 1000 {
		t.Fatalf("got %#v", got)
	}
}
