package netsec

import (
	"bytes"
	"testing"
)

func validContext() Context {
	return Context{
		Tenant:          []byte("tenant-a"),
		Purpose:         PurposeEncrypt,
		ProtocolVersion: []byte("v1.0"),
		Direction:       DirectionSend,
		Epoch:           7,
	}
}

func TestContextValidation(t *testing.T) {
	valid := validContext()
	if err := valid.validate(); err != nil {
		t.Fatalf("valid context rejected: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(*Context)
		wantErr error
	}{
		{"empty tenant", func(c *Context) { c.Tenant = nil }, errEmptyTenant},
		{"overlong tenant", func(c *Context) { c.Tenant = make([]byte, MaxTenantLength+1) }, errTenantTooLong},
		{"tenant at max length accepted", func(c *Context) { c.Tenant = make([]byte, MaxTenantLength) }, nil},
		{"overlong protocol version", func(c *Context) { c.ProtocolVersion = make([]byte, MaxProtocolVersionLength+1) }, errVersionTooLong},
		{"unknown purpose", func(c *Context) { c.Purpose = Purpose(99) }, errUnknownPurpose},
		{"zero purpose", func(c *Context) { c.Purpose = Purpose(0) }, errUnknownPurpose},
		{"unknown direction", func(c *Context) { c.Direction = Direction(99) }, errUnknownDirection},
		{"zero direction", func(c *Context) { c.Direction = Direction(0) }, errUnknownDirection},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := valid
			tc.mutate(&ctx)
			err := ctx.validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected acceptance, got %v", err)
				}
				return
			}
			if err != tc.wantErr {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	// Every allowed purpose and direction must validate.
	for _, purpose := range []Purpose{PurposeEncrypt, PurposeAuthenticate, PurposeTicket} {
		for _, direction := range []Direction{DirectionSend, DirectionRecv} {
			ctx := valid
			ctx.Purpose = purpose
			ctx.Direction = direction
			if err := ctx.validate(); err != nil {
				t.Fatalf("purpose %d direction %d rejected: %v", purpose, direction, err)
			}
		}
	}
}

// TestFieldBoundaryNoCollision proves the length-prefix framing removes the
// ambiguity of naive concatenation: the two contexts have identical raw
// tenant||version byte strings but must encode to distinct info strings.
func TestFieldBoundaryNoCollision(t *testing.T) {
	first := validContext()
	first.Tenant = []byte("ab")
	first.ProtocolVersion = []byte("c")

	second := validContext()
	second.Tenant = []byte("a")
	second.ProtocolVersion = []byte("bc")

	// Sanity check: without framing the variable fields really do collide.
	naive := append(append([]byte{}, first.Tenant...), first.ProtocolVersion...)
	naive2 := append(append([]byte{}, second.Tenant...), second.ProtocolVersion...)
	if !bytes.Equal(naive, naive2) {
		t.Fatalf("test setup broken: naive concatenations differ")
	}

	info1 := first.canonicalInfo()
	info2 := second.canonicalInfo()
	if bytes.Equal(info1, info2) {
		t.Fatalf("length-prefixed contexts collided:\n %x\n %x", info1, info2)
	}

	// Encoding must be stable so cache keys are deterministic.
	if !bytes.Equal(first.canonicalInfo(), info1) {
		t.Fatalf("canonical encoding is not deterministic")
	}
}

// TestEnumAndEpochEncodingUniqueness checks that every legal purpose,
// direction and epoch combination produces a unique framing and that the
// fixed-width fields are actually length prefixed.
func TestEnumAndEpochEncodingUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	base := validContext()
	for _, purpose := range []Purpose{PurposeEncrypt, PurposeAuthenticate, PurposeTicket} {
		for _, direction := range []Direction{DirectionSend, DirectionRecv} {
			for _, epoch := range []uint64{0, 1, 256, 1 << 32} {
				ctx := base
				ctx.Purpose = purpose
				ctx.Direction = direction
				ctx.Epoch = epoch
				encoded := string(ctx.canonicalInfo())
				if seen[encoded] {
					t.Fatalf("duplicate encoding for purpose %d direction %d epoch %d", purpose, direction, epoch)
				}
				seen[encoded] = true
			}
		}
	}

	// A 256-byte tenant keeps its full length prefix (0x01 0x00) rather than
	// truncating the length to a single byte.
	ctx := base
	ctx.Tenant = make([]byte, 256)
	encoded := ctx.canonicalInfo()
	if encoded[0] != 0x01 || encoded[1] != 0x00 {
		t.Fatalf("tenant length prefix wrong for 256-byte tenant: % x", encoded[:3])
	}

	// A 255-byte tenant uses the single-byte boundary value 0x00 0xff.
	ctx.Tenant = make([]byte, 255)
	encoded = ctx.canonicalInfo()
	if encoded[0] != 0x00 || encoded[1] != 0xff {
		t.Fatalf("tenant length prefix wrong for 255-byte tenant: % x", encoded[:3])
	}
}
