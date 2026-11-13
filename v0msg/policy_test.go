package v0msg

import (
	"errors"
	"testing"
)

// key builds a deterministic 32-byte address from a small integer.
func key(n byte) [32]byte {
	var k [32]byte
	k[0] = n
	k[31] = n
	return k
}

func shortvec(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n == 0 {
			out = append(out, b)
			return out
		}
		out = append(out, b|0x80)
	}
}

type msgBuilder struct {
	buf []byte
}

func newMsg() *msgBuilder { return &msgBuilder{buf: []byte{VersionPrefix}} }

func (b *msgBuilder) header(nSig, nRoSig, nRoNonSig byte) *msgBuilder {
	b.buf = append(b.buf, nSig, nRoSig, nRoNonSig)
	return b
}

func (b *msgBuilder) staticKeys(keys ...[32]byte) *msgBuilder {
	b.buf = append(b.buf, shortvec(len(keys))...)
	for _, k := range keys {
		b.buf = append(b.buf, k[:]...)
	}
	return b
}

func (b *msgBuilder) blockhash() *msgBuilder {
	b.buf = append(b.buf, make([]byte, 32)...)
	return b
}

func (b *msgBuilder) instruction(prog uint8, accts []uint8, data []byte) *msgBuilder {
	// caller is responsible for the instruction-count shortvec; see instructions()
	b.buf = append(b.buf, prog)
	b.buf = append(b.buf, shortvec(len(accts))...)
	b.buf = append(b.buf, accts...)
	b.buf = append(b.buf, shortvec(len(data))...)
	b.buf = append(b.buf, data...)
	return b
}

func (b *msgBuilder) instructions(n int) *msgBuilder {
	b.buf = append(b.buf, shortvec(n)...)
	return b
}

func (b *msgBuilder) lookup(table [32]byte, writable, readonly []uint8) *msgBuilder {
	b.buf = append(b.buf, table[:]...)
	b.buf = append(b.buf, shortvec(len(writable))...)
	b.buf = append(b.buf, writable...)
	b.buf = append(b.buf, shortvec(len(readonly))...)
	b.buf = append(b.buf, readonly...)
	return b
}

func (b *msgBuilder) lookups(n int) *msgBuilder {
	b.buf = append(b.buf, shortvec(n)...)
	return b
}

func (b *msgBuilder) bytes() []byte { return b.buf }

// staticResolver returns a resolver serving the given table contents.
func staticResolver(tables map[[32]byte][][32]byte) TableResolver {
	return func(t [32]byte) ([][32]byte, error) {
		addrs, ok := tables[t]
		if !ok {
			return nil, errors.New("unknown table")
		}
		return addrs, nil
	}
}

func TestDenylistedAccountHiddenInLookupTable(t *testing.T) {
	tableAddr := key(0xAA)
	evil := key(0x66)
	// Static keys: fee payer + program. The denylisted account only
	// appears via the lookup table, invisible to static-only previews.
	raw := newMsg().
		header(1, 0, 1).
		staticKeys(key(1), key(2)).
		blockhash().
		instructions(1).
		instruction(1, []uint8{0, 2}, nil). // account index 2 = loaded writable
		lookups(1).
		lookup(tableAddr, []uint8{0}, nil).
		bytes()

	resolver := staticResolver(map[[32]byte][][32]byte{
		tableAddr: {evil},
	})

	rep, err := Inspect(raw, Options{ResolveTable: resolver, Denylist: [][32]byte{evil}})
	if !errors.Is(err, ErrDeniedAccount) {
		t.Fatalf("expected ErrDeniedAccount, got %v", err)
	}
	if rep == nil || len(rep.DeniedHits) != 1 || rep.DeniedHits[0] != 2 {
		t.Fatalf("expected denylist hit at index 2, got %+v", rep)
	}
	if !rep.Accounts[2].Denied || rep.Accounts[2].Static || !rep.Accounts[2].Writable {
		t.Fatalf("loaded denied account misclassified: %+v", rep.Accounts[2])
	}
}

func TestProgramIndexPointingAtLoadedAccount(t *testing.T) {
	tableAddr := key(0xBB)
	raw := newMsg().
		header(1, 0, 0).
		staticKeys(key(1)).
		blockhash().
		instructions(1).
		instruction(1, nil, nil). // program index 1 = first loaded account
		lookups(1).
		lookup(tableAddr, []uint8{0}, nil).
		bytes()

	resolver := staticResolver(map[[32]byte][][32]byte{
		tableAddr: {key(9)},
	})

	_, err := Inspect(raw, Options{ResolveTable: resolver})
	if !errors.Is(err, ErrProgramIndexNotStatic) {
		t.Fatalf("expected ErrProgramIndexNotStatic, got %v", err)
	}
}

func TestDuplicateAddressStaticAndLoaded(t *testing.T) {
	tableAddr := key(0xCC)
	dup := key(2)
	raw := newMsg().
		header(1, 0, 0).
		staticKeys(key(1), dup).
		blockhash().
		instructions(0).
		lookups(1).
		lookup(tableAddr, nil, []uint8{0}). // loads dup again as readonly
		bytes()

	resolver := staticResolver(map[[32]byte][][32]byte{
		tableAddr: {dup},
	})

	_, err := Inspect(raw, Options{ResolveTable: resolver})
	if !errors.Is(err, ErrDuplicateAccount) {
		t.Fatalf("expected ErrDuplicateAccount, got %v", err)
	}
}

func TestVersionPrefixRejected(t *testing.T) {
	base := newMsg().
		header(1, 0, 0).
		staticKeys(key(1)).
		blockhash().
		instructions(0).
		lookups(0).
		bytes()

	for _, first := range []byte{0x81, 0xFF, 0x7F, 0x01, 0x00} {
		raw := append([]byte{first}, base[1:]...)
		if _, err := Inspect(raw, Options{}); !errors.Is(err, ErrBadVersion) {
			t.Fatalf("first byte 0x%02x: expected ErrBadVersion, got %v", first, err)
		}
	}
	if _, err := Inspect(base, Options{}); err != nil {
		t.Fatalf("valid v0 message rejected: %v", err)
	}
}

func TestWritabilityAndOrdering(t *testing.T) {
	tableW, tableR := key(0xD1), key(0xD2)
	// Header: 3 sigs (1 readonly), 1 readonly non-signer.
	// Static: [w-sig, w-sig, ro-sig, w-nonsig, ro-nonsig]
	raw := newMsg().
		header(3, 1, 1).
		staticKeys(key(1), key(2), key(3), key(4), key(5)).
		blockhash().
		instructions(0).
		lookups(2).
		lookup(tableW, []uint8{0}, []uint8{1}).
		lookup(tableR, []uint8{0}, nil).
		bytes()

	resolver := staticResolver(map[[32]byte][][32]byte{
		tableW: {key(6), key(7)},
		tableR: {key(8)},
	})

	rep, err := Inspect(raw, Options{ResolveTable: resolver})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	type want struct {
		addr     [32]byte
		static   bool
		signer   bool
		writable bool
	}
	wants := []want{
		{key(1), true, true, true},    // writable signer
		{key(2), true, true, true},    // writable signer
		{key(3), true, true, false},   // readonly signer
		{key(4), true, false, true},   // writable non-signer
		{key(5), true, false, false},  // readonly non-signer
		{key(6), false, false, true},  // table W writable load
		{key(8), false, false, true},  // table R writable load (all writables before readonlys)
		{key(7), false, false, false}, // table W readonly load
	}
	if len(rep.Accounts) != len(wants) {
		t.Fatalf("got %d accounts, want %d", len(rep.Accounts), len(wants))
	}
	for i, w := range wants {
		a := rep.Accounts[i]
		if a.Address != w.addr || a.Static != w.static || a.Signer != w.signer || a.Writable != w.writable {
			t.Errorf("account %d: got %+v, want addr=%v static=%v signer=%v writable=%v",
				i, a, w.addr, w.static, w.signer, w.writable)
		}
		if a.Index != i {
			t.Errorf("account %d: Index = %d", i, a.Index)
		}
	}
}

func TestTooManyAccounts(t *testing.T) {
	tableAddr := key(0xE1)
	// 200 static keys + 100 loaded = 300 > 256.
	keys := make([][32]byte, 200)
	for i := range keys {
		var k [32]byte
		k[0] = byte(i)
		k[1] = byte(i >> 8)
		k[31] = 0x5A
		keys[i] = k
	}
	loaded := make([][32]byte, 100)
	for i := range loaded {
		var k [32]byte
		k[0] = byte(i)
		k[1] = 0xFF
		k[31] = 0x7B
		loaded[i] = k
	}
	idx := make([]uint8, 100)
	for i := range idx {
		idx[i] = uint8(i)
	}

	raw := newMsg().
		header(1, 0, 0).
		staticKeys(keys...).
		blockhash().
		instructions(0).
		lookups(1).
		lookup(tableAddr, idx, nil).
		bytes()

	resolver := staticResolver(map[[32]byte][][32]byte{tableAddr: loaded})
	_, err := Inspect(raw, Options{ResolveTable: resolver})
	if !errors.Is(err, ErrTooManyAccounts) {
		t.Fatalf("expected ErrTooManyAccounts, got %v", err)
	}
}

func TestDenylistCheckedOnFullListIncludingStatic(t *testing.T) {
	denied := key(3)
	raw := newMsg().
		header(1, 0, 0).
		staticKeys(key(1), denied).
		blockhash().
		instructions(0).
		lookups(0).
		bytes()

	_, err := Inspect(raw, Options{Denylist: [][32]byte{denied}})
	if !errors.Is(err, ErrDeniedAccount) {
		t.Fatalf("expected ErrDeniedAccount, got %v", err)
	}
}
