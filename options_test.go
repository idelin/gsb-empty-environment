package netsec_test

import (
	"errors"
	"math/rand/v2"
	"net/netip"
	"reflect"
	"testing"

	netsec "example.org/netsec/q0156"
)

// refParse is an independent structural model. It is written as an
// index-based state machine and shares no code with the production decoder.
// It returns the sentinel kind, earliest rejection offset, and the number of
// fully decoded options before the rejection.
func refParse(b []byte) (error, int, int) {
	i, count := 0, 0
	for i < len(b) {
		t := b[i]
		switch t {
		case netsec.OptionEndOfOptions:
			count++
			for j := i + 1; j < len(b); j++ {
				if b[j] != 0 {
					return netsec.ErrPadding, j, count
				}
			}
			return nil, -1, count
		case netsec.OptionNoOperation:
			count++
			i++
			continue
		}

		if len(b)-i < 2 {
			return netsec.ErrTruncated, i, count
		}
		length := int(b[i+1])
		if length < 2 {
			return netsec.ErrLength, i, count
		}
		if length > len(b)-i {
			return netsec.ErrTruncated, i, count
		}

		if t == netsec.OptionLooseSourceRoute || t == netsec.OptionStrictSourceRoute {
			if length < 3 || (length-3)%4 != 0 {
				return netsec.ErrLength, i, count
			}
			pointer := int(b[i+2])
			if pointer < 4 || pointer > length+1 || (pointer-4)%4 != 0 {
				return netsec.ErrPointer, i, count
			}
		}

		count++
		i += length
	}
	return nil, -1, count
}

// refFirstActiveRoute independently locates the first active LSRR/SSRR in a
// stream already accepted by refParse.
func refFirstActiveRoute(b []byte) bool {
	i := 0
	for i < len(b) {
		t := b[i]
		if t == netsec.OptionEndOfOptions {
			return false
		}
		if t == netsec.OptionNoOperation {
			i++
			continue
		}
		length := int(b[i+1])
		if t == netsec.OptionLooseSourceRoute || t == netsec.OptionStrictSourceRoute {
			if int(b[i+2]) <= length {
				return true
			}
		}
		i += length
	}
	return false
}

func assertParseError(t *testing.T, err error, wantKind error, wantOffset int) {
	t.Helper()
	if !errors.Is(err, wantKind) {
		t.Fatalf("error kind = %v, want %v", err, wantKind)
	}
	var pe *netsec.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("error %v is not *ParseError", err)
	}
	if pe.Offset != wantOffset {
		t.Fatalf("error offset = %d, want %d (err %v)", pe.Offset, wantOffset, err)
	}
}

func TestParseTypeFields(t *testing.T) {
	// 0x9e: copied=1, class=0, number=30; option length 3 with one data byte.
	opts, err := netsec.Parse([]byte{0x01, 0x9e, 0x03, 0xaa})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 {
		t.Fatalf("decoded %d options, want 2", len(opts))
	}
	nop, unknown := opts[0], opts[1]
	if nop.Type != netsec.OptionNoOperation || !reflect.DeepEqual(nop.Raw, []byte{0x01}) || nop.Offset != 0 {
		t.Fatalf("bad NOP decode: %+v", nop)
	}
	if !unknown.Copied || unknown.Class != 0 || unknown.Number != 30 || unknown.Offset != 1 {
		t.Fatalf("bad type-field decode: %+v", unknown)
	}
	if unknown.Length() != 3 || unknown.IsSourceRoute() {
		t.Fatalf("unknown option misclassified: %+v", unknown)
	}

	// 0x20: copied=0, class=1, number=0.
	decoded, err := netsec.Parse([]byte{0x20, 0x02, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	if decoded[0].Copied || decoded[0].Class != 1 || decoded[0].Number != 0 {
		t.Fatalf("class/copy decode wrong: %+v", decoded[0])
	}
}

func TestSourceRouteActiveAndNextHop(t *testing.T) {
	// LSRR, length 7, pointer 4: one active hop 10.0.0.1.
	active := []byte{0x83, 0x07, 0x04, 10, 0, 0, 1}
	parsed, err := netsec.Parse(active)
	if err != nil {
		t.Fatal(err)
	}
	route, err := netsec.DecodeSourceRoute(parsed[0])
	if err != nil {
		t.Fatal(err)
	}
	if !route.Active() {
		t.Fatal("route with pointer 4 must be active")
	}
	next, ok := route.NextHop()
	if !ok || next != netip.MustParseAddr("10.0.0.1") {
		t.Fatalf("next hop = %v/%v, want 10.0.0.1", next, ok)
	}
	if len(route.Route) != 1 || !reflect.DeepEqual(route.Route[0], active[3:7]) {
		t.Fatalf("route slots wrong: %x", route.Route)
	}

	// Same option with pointer 8: exhausted, no next-hop change.
	exhausted := []byte{0x83, 0x07, 0x08, 10, 0, 0, 1}
	parsed, err = netsec.Parse(exhausted)
	if err != nil {
		t.Fatal(err)
	}
	route, err = netsec.DecodeSourceRoute(parsed[0])
	if err != nil {
		t.Fatal(err)
	}
	if route.Active() {
		t.Fatal("pointer == length+1 means the route is exhausted")
	}
	if _, ok := route.NextHop(); ok {
		t.Fatal("exhausted route must not yield a next hop")
	}

	// SSRR, length 11, pointer 8: second slot active.
	strict := []byte{0x87, 0x0b, 0x08, 1, 1, 1, 1, 2, 2, 2, 2}
	parsed, err = netsec.Parse(strict)
	if err != nil {
		t.Fatal(err)
	}
	route, err = netsec.DecodeSourceRoute(parsed[0])
	if err != nil {
		t.Fatal(err)
	}
	if !route.Active() || len(route.Route) != 2 {
		t.Fatalf("strict route decode wrong: %+v", route)
	}
	if next, _ := route.NextHop(); next != netip.MustParseAddr("2.2.2.2") {
		t.Fatalf("strict next hop = %v, want 2.2.2.2", next)
	}
}

func TestCheckVerdict(t *testing.T) {
	// Unknown options alone are allowed and never classified as source routes.
	verdict, err := netsec.Check([]byte{0x01, 0x9e, 0x04, 0xaa, 0xbb, 0x00, 0x00})
	if err != nil || verdict.Reject || verdict.Reason != netsec.ReasonNone {
		t.Fatalf("unknown-only stream must pass: %+v err=%v", verdict, err)
	}

	// Active LSRR is rejected with the decoded route attached.
	verdict, err = netsec.Check([]byte{0x83, 0x07, 0x04, 10, 0, 0, 1})
	if err != nil || !verdict.Reject || verdict.Reason != netsec.ReasonSourceRoute {
		t.Fatalf("active source route must be rejected: %+v err=%v", verdict, err)
	}
	if verdict.SourceRoute == nil || verdict.SourceRoute.Type != netsec.OptionLooseSourceRoute {
		t.Fatalf("verdict missing source route: %+v", verdict)
	}

	// Exhausted LSRR is structurally valid and does not change the next hop.
	verdict, err = netsec.Check([]byte{0x83, 0x03, 0x04})
	if err != nil || verdict.Reject {
		t.Fatalf("exhausted route must pass: %+v err=%v", verdict, err)
	}

	// Malformed input is rejected as malformed, not as a source route.
	verdict, err = netsec.Check([]byte{0x83, 0x07, 0x05, 0, 0, 0, 0})
	if err == nil || !verdict.Reject || verdict.Reason != netsec.ReasonMalformed {
		t.Fatalf("bad pointer must be malformed: %+v err=%v", verdict, err)
	}
}

func TestMalformedTable(t *testing.T) {
	cases := []struct {
		name   string
		input  []byte
		kind   error
		offset int
	}{
		{"missing length byte", []byte{0x83}, netsec.ErrTruncated, 0},
		{"length one on source route", []byte{0x83, 0x01}, netsec.ErrLength, 0},
		{"length two missing pointer", []byte{0x83, 0x02}, netsec.ErrLength, 0},
		{"length not 3 plus 4k", []byte{0x83, 0x06, 0x04, 0, 0, 0}, netsec.ErrLength, 0},
		{"pointer below minimum", []byte{0x83, 0x07, 0x03, 0, 0, 0, 0}, netsec.ErrPointer, 0},
		{"pointer past end", []byte{0x83, 0x07, 0x0c, 0, 0, 0, 0}, netsec.ErrPointer, 0},
		{"pointer misaligned", []byte{0x83, 0x07, 0x05, 0, 0, 0, 0}, netsec.ErrPointer, 0},
		{"non-zero padding after EOL", []byte{0x00, 0x01}, netsec.ErrPadding, 1},
		{"non-zero padding later", []byte{0x01, 0x00, 0x00, 0x03}, netsec.ErrPadding, 3},
		{"unknown option overruns", []byte{0x9e, 0x05, 0x01, 0x02}, netsec.ErrTruncated, 0},
		{"overrun after NOP", []byte{0x01, 0x9e, 0x05}, netsec.ErrTruncated, 1},
		{"declared length one", []byte{0x9e, 0x01}, netsec.ErrLength, 0},
		{"source route length overruns region", []byte{0x01, 0x87, 0x07, 0x04}, netsec.ErrTruncated, 1},
		{"bad pointer after valid option", []byte{0x01, 0x83, 0x07, 0x05, 0, 0, 0, 0}, netsec.ErrPointer, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := netsec.Parse(tc.input)
			if err == nil {
				t.Fatalf("Parse(% x) unexpectedly succeeded", tc.input)
			}
			assertParseError(t, err, tc.kind, tc.offset)
		})
	}
}

// TestTruncationSweep walks every prefix of each sample and requires the
// production decoder and the independent structural model to agree on
// accept/reject, on the rejection kind and offset (earliest rejection), and on
// the number of options decoded before that point.
func TestTruncationSweep(t *testing.T) {
	samples := map[string][]byte{
		"empty":             {},
		"nop":               {0x01},
		"eol":               {0x00},
		"eol with padding":  {0x00, 0x00, 0x00},
		"unknown option":    {0x9e, 0x03, 0xaa},
		"lsrr active":       {0x83, 0x07, 0x04, 10, 0, 0, 1},
		"lsrr exhausted":    {0x83, 0x07, 0x08, 10, 0, 0, 1},
		"ssrr active":       {0x87, 0x0b, 0x08, 1, 1, 1, 1, 2, 2, 2, 2},
		"mixed valid":       {0x01, 0x9e, 0x03, 0xaa, 0x83, 0x03, 0x04, 0x00, 0x00},
		"bad padding":       {0x00, 0x00, 0x03},
		"bad pointer":       {0x83, 0x07, 0x05, 0, 0, 0, 0},
		"overrun":           {0x01, 0x9e, 0x05, 0x01},
		"illegal length":    {0x83, 0x06, 0x04, 0, 0, 0},
		"two source routes": {0x83, 0x07, 0x08, 1, 1, 1, 1, 0x87, 0x07, 0x04, 2, 2, 2, 2},
	}

	for name, sample := range samples {
		t.Run(name, func(t *testing.T) {
			for n := 0; n <= len(sample); n++ {
				prefix := sample[:n]
				prodOpts, prodErr := netsec.Parse(prefix)
				refKind, refOff, refCount := refParse(prefix)

				if (prodErr == nil) != (refKind == nil) {
					t.Fatalf("prefix % x: production err=%v but reference kind=%v", prefix, prodErr, refKind)
				}
				if len(prodOpts) != refCount {
					t.Fatalf("prefix % x: decoded count %d, reference %d", prefix, len(prodOpts), refCount)
				}
				if prodErr != nil {
					var pe *netsec.ParseError
					if !errors.As(prodErr, &pe) {
						t.Fatalf("prefix % x: %v is not *ParseError", prefix, prodErr)
					}
					if !errors.Is(prodErr, refKind) || pe.Offset != refOff {
						t.Fatalf("prefix % x: production (%v) disagrees with reference kind=%v offset=%d",
							prefix, prodErr, refKind, refOff)
					}
					continue
				}

				// Accepted prefixes must also agree on the routing verdict.
				if got, want := refFirstActiveRoute(prefix), firstActiveViaCheck(t, prefix); got != want {
					t.Fatalf("prefix % x: active-route verdict prod=%v ref=%v", prefix, want, got)
				}
			}
		})
	}
}

func firstActiveViaCheck(t *testing.T, b []byte) bool {
	t.Helper()
	verdict, err := netsec.Check(b)
	if err != nil {
		t.Fatalf("Check(% x) rejected accepted prefix: %v", b, err)
	}
	return verdict.Reason == netsec.ReasonSourceRoute
}

// genStream builds semi-structured option regions including deliberately
// malformed items, so both decoders see framing and pointer edge cases.
func genStream(rng *rand.Rand) []byte {
	var b []byte
	for n := rng.IntN(6); n >= 0; n-- {
		switch rng.IntN(8) {
		case 0:
			b = append(b, 0x01) // NOP
		case 1:
			b = append(b, 0x00) // EOL, then padding below
		case 2: // generic unknown option, length 2..10
			length := 2 + rng.IntN(9)
			b = append(b, byte(0x10+rng.IntN(0x70)), byte(length))
			b = append(b, make([]byte, length-2)...)
		case 3: // LSRR/SSRR with legal or randomized pointer
			typeByte := byte(netsec.OptionLooseSourceRoute)
			if rng.IntN(2) == 0 {
				typeByte = netsec.OptionStrictSourceRoute
			}
			length := 3 + 4*rng.IntN(3)     // 3, 7 or 11
			pointer := 4 + rng.IntN(length) // includes length+1 and possibly misaligned
			b = append(b, typeByte, byte(length), byte(pointer))
			b = append(b, make([]byte, length-3)...)
		case 4: // source route with an illegal length (not 3+4k)
			length := 2 + rng.IntN(9)
			b = append(b, netsec.OptionLooseSourceRoute, byte(length), byte(4))
			b = append(b, make([]byte, max(0, length-3))...)
		case 5: // option whose declared length overruns a random tail
			length := 2 + rng.IntN(8)
			b = append(b, netsec.OptionStrictSourceRoute, byte(length), byte(4))
			b = append(b, make([]byte, rng.IntN(length-1))...)
		case 6, 7:
			b = append(b, byte(rng.IntN(256)))
		}
	}
	return b
}

// TestRandomDifferential fuzzes full streams and all their prefixes through
// both decoders to pin the earliest rejection position beyond hand samples.
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x01560156, 0x9e3779b97f4a7c15))
	for iter := 0; iter < 2000; iter++ {
		stream := genStream(rng)
		for n := 0; n <= len(stream); n++ {
			prefix := stream[:n]
			prodOpts, prodErr := netsec.Parse(prefix)
			refKind, refOff, refCount := refParse(prefix)

			if (prodErr == nil) != (refKind == nil) {
				t.Fatalf("iter %d prefix % x: prod err=%v ref kind=%v", iter, prefix, prodErr, refKind)
			}
			if len(prodOpts) != refCount {
				t.Fatalf("iter %d prefix % x: counts %d vs %d", iter, prefix, len(prodOpts), refCount)
			}
			if prodErr != nil {
				var pe *netsec.ParseError
				errors.As(prodErr, &pe)
				if !errors.Is(prodErr, refKind) || pe.Offset != refOff {
					t.Fatalf("iter %d prefix % x: rejection %v disagrees with kind=%v offset=%d",
						iter, prefix, prodErr, refKind, refOff)
				}
				continue
			}

			verdict, err := netsec.Check(prefix)
			if err != nil {
				t.Fatalf("iter %d prefix % x: Check rejected accepted stream: %v", iter, prefix, err)
			}
			active := refFirstActiveRoute(prefix)
			if (verdict.Reason == netsec.ReasonSourceRoute) != active {
				t.Fatalf("iter %d prefix % x: source-route verdict %v, ref active=%v",
					iter, prefix, verdict.Reason, active)
			}
		}
	}
}

func TestRawIntervalsRetainOriginal(t *testing.T) {
	input := []byte{0x01, 0x83, 0x07, 0x04, 10, 0, 0, 1, 0x00}
	parsed, err := netsec.Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	// Raw slices alias the caller's buffer and cover exactly their interval.
	if !reflect.DeepEqual(parsed[0].Raw, input[0:1]) || !sameBacking(parsed[0].Raw, input) {
		t.Fatal("NOP Raw does not retain original interval")
	}
	if !reflect.DeepEqual(parsed[1].Raw, input[1:8]) || !sameBacking(parsed[1].Raw, input) {
		t.Fatal("LSRR Raw does not retain original interval")
	}

	route, err := netsec.DecodeSourceRoute(parsed[1])
	if err != nil {
		t.Fatal(err)
	}
	if !sameBacking(route.Route[0], input) || !reflect.DeepEqual(route.Route[0], input[4:8]) {
		t.Fatal("route address slot does not retain original interval")
	}

	// Mutating the original packet is visible through the retained slices.
	input[4] = 192
	if route.Route[0][0] != 192 {
		t.Fatal("route slot was copied instead of aliasing the original buffer")
	}
}

func sameBacking(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	pa := uintptr(reflect.ValueOf(a).UnsafePointer())
	pb := uintptr(reflect.ValueOf(b).UnsafePointer())
	return pa >= pb && pa < pb+uintptr(len(b))
}

func TestDecodeSourceRouteErrors(t *testing.T) {
	// A non-source-route option cannot be decoded as one.
	parsed, err := netsec.Parse([]byte{0x9e, 0x03, 0xaa})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := netsec.DecodeSourceRoute(parsed[0]); !errors.Is(err, netsec.ErrNotSourceRoute) {
		t.Fatalf("got %v, want ErrNotSourceRoute", err)
	}

	// A hand-built source-route option failing validation is reported directly.
	bad := netsec.Option{
		Type:   netsec.OptionLooseSourceRoute,
		Offset: 7,
		Raw:    []byte{netsec.OptionLooseSourceRoute, 0x06, 0x04, 0, 0, 0},
	}
	_, err = netsec.DecodeSourceRoute(bad)
	assertParseError(t, err, netsec.ErrLength, 7)
}

func TestCheckUnknownOptionsNoReject(t *testing.T) {
	// All valid, non-source-route options (including NOP/EOOL padding) pass.
	verdict, err := netsec.Check([]byte{0x01, 0x01, 0x20, 0x02, 0x00, 0x00, 0x00})
	if err != nil || verdict.Reject || verdict.Reason != netsec.ReasonNone || len(verdict.Options) != 4 {
		t.Fatalf("benign stream rejected: %+v err=%v", verdict, err)
	}
}

func TestParseErrorString(t *testing.T) {
	err := &netsec.ParseError{Kind: netsec.ErrPointer, Offset: 9}
	if err.Error() != "netsec: source-route pointer outside valid address slots at offset 9" {
		t.Fatalf("unexpected error text: %q", err.Error())
	}
	if !errors.Is(err, netsec.ErrPointer) {
		t.Fatal("errors.Is must match the wrapped kind")
	}
}

func TestFromIPv4Header(t *testing.T) {
	// IHL 8 => 32-byte header, with an active LSRR in the option region.
	header := make([]byte, 32)
	header[0] = 0x48
	copy(header[20:], []byte{0x83, 0x07, 0x04, 10, 0, 0, 1, 0, 0, 0, 0, 0})

	options, err := netsec.FromIPv4Header(header)
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 12 {
		t.Fatalf("options region length = %d, want 12", len(options))
	}
	verdict, err := netsec.Check(options)
	if err != nil || !verdict.Reject || verdict.Reason != netsec.ReasonSourceRoute {
		t.Fatalf("header options verdict wrong: %+v err=%v", verdict, err)
	}

	if _, err := netsec.FromIPv4Header(make([]byte, 19)); err == nil {
		t.Fatal("short header must be rejected")
	}
	badVersion := make([]byte, 20)
	badVersion[0] = 0x64
	if _, err := netsec.FromIPv4Header(badVersion); err == nil {
		t.Fatal("non-IPv4 header must be rejected")
	}
	badIHL := make([]byte, 20)
	badIHL[0] = 0x44
	if _, err := netsec.FromIPv4Header(badIHL); err == nil {
		t.Fatal("IHL exceeding buffer must be rejected")
	}
}
