package netsec

import (
	"errors"
	"testing"
)

var header = []byte{0x04, 0x08}

// fixnumEnc encodes small non-negative lengths the way Marshal does.
func fixnumEnc(n int) byte {
	return byte(n + 5)
}

func str(s string) []byte {
	out := []byte{'"', fixnumEnc(len(s))}
	return append(out, s...)
}

func sym(s string) []byte {
	out := []byte{':', fixnumEnc(len(s))}
	return append(out, s...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func stream(parts ...[]byte) []byte {
	return concat(append([]byte{}, header...), concat(parts...))
}

func TestValidNestedStream(t *testing.T) {
	// [ 2, "hello", :foo, ;0, @0, true, false, nil,
	//   I"abc" with ivar, { :k => "v" }, Widget object ]
	inner := concat(
		[]byte{'i', 0x07}, // fixnum 2
		str("hello"),      // object index 0
		sym("foo"),        // symbol index 0
		[]byte{';', 0x00}, // symlink -> :foo
		[]byte{'@', 0x00}, // link -> "hello"
		[]byte{'T'},
		[]byte{'F'},
		[]byte{'0'},
		concat( // ivar-wrapped string, object index 1
			[]byte{'I'},
			str("abc"),
			[]byte{fixnumEnc(1)},
			sym("@x"),
			[]byte{'i', 0x08},
		),
		concat( // hash, object index 2
			[]byte{'{', fixnumEnc(1)},
			sym("k"),
			str("v"),
		),
		concat( // whitelisted object, object index 3
			[]byte{'o'},
			sym("Widget"),
			[]byte{fixnumEnc(1)},
			sym("@name"),
			str("w"),
		),
	)
	data := stream(append([]byte{'[', fixnumEnc(11)}, inner...))

	p := DefaultPolicy()
	p.AllowedClasses = []string{"Widget"}
	if err := Validate(data, p); err != nil {
		t.Fatalf("valid stream rejected: %v", err)
	}
}

func TestUserDefTypeRejected(t *testing.T) {
	// 'u' (user-defined _dump) must never pass the gate.
	data := stream([]byte{'u'}, sym("Exploit"), []byte{fixnumEnc(3), 1, 2, 3})
	if err := Validate(data, DefaultPolicy()); !errors.Is(err, ErrDisallowedType) {
		t.Fatalf("want ErrDisallowedType, got %v", err)
	}
}

func TestOtherDangerousTypesRejected(t *testing.T) {
	for _, code := range []byte{'U', 'e', 'C', 'd', 'S', 'M', 'c', 'm', 'f', '/', 'l', '}'} {
		data := stream([]byte{code})
		if err := Validate(data, DefaultPolicy()); !errors.Is(err, ErrDisallowedType) {
			t.Fatalf("type 0x%02x: want ErrDisallowedType, got %v", code, err)
		}
	}
}

func TestClassNotInWhitelistRejected(t *testing.T) {
	data := stream(concat(
		[]byte{'o'},
		sym("Evil"),
		[]byte{fixnumEnc(0)},
	))
	p := DefaultPolicy()
	p.AllowedClasses = []string{"Widget"}
	if err := Validate(data, p); !errors.Is(err, ErrClassNotAllowed) {
		t.Fatalf("want ErrClassNotAllowed, got %v", err)
	}
}

func TestObjectRejectedWithoutWhitelist(t *testing.T) {
	data := stream(concat([]byte{'o'}, sym("Widget"), []byte{fixnumEnc(0)}))
	if err := Validate(data, DefaultPolicy()); !errors.Is(err, ErrClassNotAllowed) {
		t.Fatalf("want ErrClassNotAllowed, got %v", err)
	}
}

func TestForgedSymlinkRejected(t *testing.T) {
	// Symbol table is empty; index 0 points to nothing yet.
	data := stream([]byte{';', 0x00})
	if err := Validate(data, DefaultPolicy()); !errors.Is(err, ErrBadLink) {
		t.Fatalf("want ErrBadLink, got %v", err)
	}
}

func TestForgedObjectLinkRejected(t *testing.T) {
	// Object table is empty; index 0 points to nothing yet.
	data := stream([]byte{'@', 0x00})
	if err := Validate(data, DefaultPolicy()); !errors.Is(err, ErrBadLink) {
		t.Fatalf("want ErrBadLink, got %v", err)
	}
}

func TestForwardObjectLinkRejected(t *testing.T) {
	// Array holds one string (object indexes 0-1); link to index 5 is forged.
	data := stream(concat(
		[]byte{'[', fixnumEnc(2)},
		str("a"),
		[]byte{'@', fixnumEnc(5)},
	))
	if err := Validate(data, DefaultPolicy()); !errors.Is(err, ErrBadLink) {
		t.Fatalf("want ErrBadLink, got %v", err)
	}
}

func TestBadHeaderRejected(t *testing.T) {
	if err := Validate([]byte{0x04, 0x07, '0'}, DefaultPolicy()); !errors.Is(err, ErrBadHeader) {
		t.Fatalf("want ErrBadHeader, got %v", err)
	}
	if err := Validate([]byte{0x04}, DefaultPolicy()); !errors.Is(err, ErrBadHeader) {
		t.Fatalf("want ErrBadHeader, got %v", err)
	}
}

func TestDepthBudgetEnforced(t *testing.T) {
	depth := 10
	inner := []byte{'0'}
	for i := 0; i < depth; i++ {
		inner = concat([]byte{'[', fixnumEnc(1)}, inner)
	}
	p := DefaultPolicy()
	p.MaxDepth = 5
	if err := Validate(stream(inner), p); !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("want ErrDepthExceeded, got %v", err)
	}
	p.MaxDepth = 16
	if err := Validate(stream(inner), p); err != nil {
		t.Fatalf("stream within depth budget rejected: %v", err)
	}
}

func TestElementBudgetEnforced(t *testing.T) {
	data := stream(concat(
		[]byte{'[', fixnumEnc(3)},
		[]byte{'T'}, []byte{'F'}, []byte{'0'},
	))
	p := DefaultPolicy()
	p.MaxElements = 3
	if err := Validate(data, p); !errors.Is(err, ErrTooManyElements) {
		t.Fatalf("want ErrTooManyElements, got %v", err)
	}
	p.MaxElements = 4
	if err := Validate(data, p); err != nil {
		t.Fatalf("stream within element budget rejected: %v", err)
	}
}

func TestTruncatedStreamRejected(t *testing.T) {
	data := stream([]byte{'"', fixnumEnc(10), 'a', 'b'})
	if err := Validate(data, DefaultPolicy()); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestTrailingDataRejected(t *testing.T) {
	data := stream([]byte{'0', '0'})
	if err := Validate(data, DefaultPolicy()); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("want ErrTrailingData, got %v", err)
	}
}
