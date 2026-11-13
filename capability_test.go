package netsec

import (
	"errors"
	"math/big"
	"testing"
)

// refEncoding is the arbitrary-precision reference encoding. All boundary
// arithmetic uses math/big so rounding decisions are independent of the
// uint64 host implementation.
type refEncoding struct {
	e, granule, base, top uint64
	exact                 bool
	overflow              bool
	hasLeft               bool
	leftLo, leftHi        uint64
	hasRight              bool
	rightLo, rightHi      uint64
}

// refEncode mirrors the specification using arbitrary-precision integers.
func refEncode(limit, base, length uint64, p int) refEncoding {
	bLimit := new(big.Int).SetUint64(limit)
	bBase := new(big.Int).SetUint64(base)
	bLen := new(big.Int).SetUint64(length)
	bTop := new(big.Int).Add(bBase, bLen)

	var r refEncoding
	if bTop.Cmp(bLimit) > 0 || bBase.Cmp(bLimit) > 0 || (length == 0 && base == limit) {
		r.overflow = true
		return r
	}
	if length == 0 {
		r.granule, r.base, r.top = 1, base, base
		r.exact = true
		return r
	}

	capacity := new(big.Int).Lsh(big.NewInt(1), uint(p))
	granule := big.NewInt(1)
	e := 0
	for bLen.Cmp(new(big.Int).Mul(capacity, granule)) > 0 {
		granule.Lsh(granule, 1)
		e++
	}
	r.e = uint64(e)
	r.granule = granule.Uint64()

	qFloor, remFloor := new(big.Int).QuoRem(bBase, granule, new(big.Int))
	encBase := new(big.Int).Mul(qFloor, granule)
	_ = remFloor
	qCeil, remCeil := new(big.Int).QuoRem(bTop, granule, new(big.Int))
	encTop := new(big.Int).Mul(qCeil, granule)
	if remCeil.Sign() != 0 {
		encTop.Add(encTop, granule)
	}

	r.base = encBase.Uint64()
	r.top = encTop.Uint64()
	r.exact = encBase.Cmp(bBase) == 0 && encTop.Cmp(bTop) == 0
	if encTop.Cmp(bLimit) > 0 {
		r.overflow = true
	}
	if encBase.Cmp(bBase) < 0 {
		r.hasLeft = true
		r.leftLo = encBase.Uint64()
		r.leftHi = new(big.Int).Sub(bBase, big.NewInt(1)).Uint64()
	}
	if encTop.Cmp(bTop) > 0 {
		r.hasRight = true
		r.rightLo = bTop.Uint64()
		r.rightHi = new(big.Int).Sub(encTop, big.NewInt(1)).Uint64()
	}
	return r
}

func encodingFromRef(p int, r refEncoding) Encoding {
	enc := Encoding{
		Precision: p,
		E:         int(r.e),
		Granule:   r.granule,
		Base:      r.base,
		Top:       r.top,
		Length:    r.top - r.base,
		Exact:     r.exact,
	}
	if r.hasLeft {
		enc.Extra.Left = Interval{Low: r.leftLo, High: r.leftHi}
	}
	if r.hasRight {
		enc.Extra.Right = Interval{Low: r.rightLo, High: r.rightHi}
	}
	return enc
}

// TestEncodeReferenceEnumeration exhaustively enumerates every base/length
// pair in a small address domain and compares the host encoding against the
// arbitrary-precision reference.
func TestEncodeReferenceEnumeration(t *testing.T) {
	const limit uint64 = 13
	host := testHost(limit)
	for p := MinPrecision; p <= 7; p++ {
		for base := uint64(0); base <= limit; base++ {
			for length := uint64(0); base+length <= limit; length++ {
				ref := refEncode(limit, base, length, p)
				got, err := host.Encode(base, length, p)
				if ref.overflow {
					if !errors.Is(err, ErrRangeOverflow) {
						t.Fatalf("Encode(%d,%d,p=%d): want overflow, got %v enc=%+v", base, length, p, err, got)
					}
					continue
				}
				if err != nil {
					t.Fatalf("Encode(%d,%d,p=%d): unexpected error %v", base, length, p, err)
				}
				want := encodingFromRef(p, ref)
				if got != want {
					t.Fatalf("Encode(%d,%d,p=%d): got %+v want %+v", base, length, p, got, want)
				}
			}
		}
	}
}

// TestDeriveReferenceEnumeration cross-checks every derivation decision in a
// small domain against the reference, including permission narrowing,
// inexact rejection and parent containment.
func TestDeriveReferenceEnumeration(t *testing.T) {
	const limit uint64 = 12
	host := testHost(limit)

	permSets := []Perm{0, Load, Load | Derive, ObjectPerms}
	precisions := []int{4, 6}

	for pb := uint64(0); pb < limit; pb++ {
		for pl := uint64(0); pb+pl <= limit; pl++ {
			parent, _, err := host.FromRequest(pb, pl, pb, ObjectPerms, MinPrecision, true)
			if err != nil {
				t.Fatalf("parent FromRequest(%d,%d): %v", pb, pl, err)
			}
			for rb := uint64(0); rb <= limit; rb++ {
				for rl := uint64(0); rb+rl <= limit; rl++ {
					for _, perms := range permSets {
						for _, p := range precisions {
							for _, allow := range []bool{false, true} {
								for _, cursor := range []uint64{rb, limit} {
									req := DeriveRequest{
										Base: rb, Length: rl, Cursor: cursor,
										Perms: perms, Precision: p, AllowInexact: allow,
									}
									got, gotErr := host.Derive(parent, req)
									wantCap, wantEnc, wantErr := refDerive(limit, parent, req)
									if !errors.Is(gotErr, wantErr) {
										t.Fatalf("Derive parent[%d,%d) req=%+v: err=%v want %v", pb, pl, req, gotErr, wantErr)
									}
									if wantErr == nil {
										if got.Cap != wantCap || got.Encoding != wantEnc {
											t.Fatalf("Derive parent[%d,%d) req=%+v:\n got cap=%+v enc=%+v\nwant cap=%+v enc=%+v",
												pb, pl, req, got.Cap, got.Encoding, wantCap, wantEnc)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

// refDerive is the arbitrary-precision reference for derivation decisions.
func refDerive(limit uint64, parent Cap, req DeriveRequest) (Cap, Encoding, error) {
	if !parent.Tag {
		return Cap{}, Encoding{}, ErrTagCleared
	}
	if parent.Sealed {
		return Cap{}, Encoding{}, ErrSealed
	}
	if parent.Perms&Derive == 0 {
		return Cap{}, Encoding{}, ErrPermissionEscalation
	}
	if req.Perms&^ObjectPerms != 0 {
		return Cap{}, Encoding{}, ErrInvalidPermission
	}
	if req.Perms&^parent.Perms != 0 {
		return Cap{}, Encoding{}, ErrPermissionEscalation
	}

	ref := refEncode(limit, req.Base, req.Length, req.Precision)
	if ref.overflow {
		return Cap{}, Encoding{}, ErrRangeOverflow
	}
	enc := encodingFromRef(req.Precision, ref)
	if !enc.Exact && !req.AllowInexact {
		return Cap{}, enc, ErrInexactDenied
	}
	if enc.Base < parent.Base || enc.Top > parent.Top() {
		return Cap{}, enc, ErrOutsideParent
	}
	if req.Cursor < enc.Base || req.Cursor > enc.Top {
		return Cap{}, enc, ErrCursorOutside
	}
	child := Cap{
		Base:       enc.Base,
		Length:     enc.Length,
		Cursor:     req.Cursor,
		Perms:      req.Perms,
		Tag:        true,
		Generation: parent.Generation,
	}
	return child, enc, nil
}

// TestAdjacentSecret proves outward rounding would reach the neighboring
// object and that the host refuses it unless the rounded range stays inside
// the parent.
func TestAdjacentSecret(t *testing.T) {
	host := NewHost()

	// Parent covers both the object [0,32) and the secret [32,64).
	parent, _, err := host.FromRequest(0, 64, 0, ObjectPerms, MinPrecision, true)
	if err != nil {
		t.Fatalf("FromRequest parent: %v", err)
	}

	// At p=4, length 31 needs granule 2 (31 > 16*1, 31 <= 16*2). Rounding
	// [2,33) spans [2,34): byte 33 belongs to the adjacent secret [32,64).
	req := DeriveRequest{
		Base: 2, Length: 31, Cursor: 2,
		Perms: Load | Store, Precision: MinPrecision,
	}
	res, err := host.Derive(parent, req)
	if !errors.Is(err, ErrInexactDenied) {
		t.Fatalf("default derive: want ErrInexactDenied, got %v", err)
	}
	if res.Encoding.Exact {
		t.Fatalf("encoding should be inexact: %+v", res.Encoding)
	}
	if res.Encoding.Base != 2 || res.Encoding.Top != 34 || res.Encoding.Granule != 2 {
		t.Fatalf("unexpected rounding: %+v", res.Encoding)
	}
	if res.Encoding.Extra.Left != (Interval{}) {
		t.Fatalf("left extra coverage wrong: %+v", res.Encoding.Extra)
	}
	if res.Encoding.Extra.Right != (Interval{Low: 33, High: 33}) {
		t.Fatalf("right extra coverage (secret) wrong: %+v", res.Encoding.Extra)
	}

	// AllowInexact cannot save a child whose rounded range reaches the secret,
	// even when opted in: build a narrower parent that excludes the secret,
	// so the rounded range can no longer be fully contained.
	narrowRes, err := host.Derive(parent, DeriveRequest{
		Base: 0, Length: 32, Cursor: 0,
		Perms: ObjectPerms, Precision: MinPrecision,
	})
	if err != nil {
		t.Fatalf("narrow parent derive: %v", err)
	}
	narrow := narrowRes.Cap
	req.AllowInexact = true
	if _, err := host.Derive(narrow, req); !errors.Is(err, ErrOutsideParent) {
		t.Fatalf("rounded range into secret: want ErrOutsideParent, got %v", err)
	}

	// A request that stays inside the object without rounding is accepted and
	// cannot touch the secret.
	safe, err := host.Derive(narrow, DeriveRequest{
		Base: 0, Length: 16, Cursor: 0,
		Perms: Load, Precision: MinPrecision,
	})
	if err != nil {
		t.Fatalf("safe derive: %v", err)
	}
	if !safe.Encoding.Exact || safe.Cap.Top() > 32 || safe.Cap.CanLoad(32) {
		t.Fatalf("safe child leaks toward secret: %+v", safe)
	}
}

// TestAdjacentSecretLeft covers rounding down onto a secret placed before the
// requested object.
func TestAdjacentSecretLeft(t *testing.T) {
	host := NewHost()
	// Secret sits before the object; the odd base forces rounding down.
	// length 17 exceeds 16*1 so the granule becomes 2.
	secretParent, _, err := host.FromRequest(0, 40, 0, ObjectPerms, MinPrecision, true)
	if err != nil {
		t.Fatalf("secretParent: %v", err)
	}
	res, err := host.Derive(secretParent, DeriveRequest{
		Base: 17, Length: 17, Cursor: 17,
		Perms: Load, Precision: MinPrecision,
	})
	if !errors.Is(err, ErrInexactDenied) {
		t.Fatalf("want ErrInexactDenied, got %v", err)
	}
	// [17,34) rounds down to [16,34): byte 16 (the adjacent secret) is
	// pulled in on the left; the even top 34 needs no right extension.
	if res.Encoding.Base != 16 || res.Encoding.Top != 34 || res.Encoding.Granule != 2 {
		t.Fatalf("rounding wrong: %+v", res.Encoding)
	}
	if res.Encoding.Extra.Left != (Interval{Low: 16, High: 16}) ||
		res.Encoding.Extra.Right != (Interval{}) {
		t.Fatalf("extra coverage wrong: %+v", res.Encoding.Extra)
	}
	// Opting in still fails if the rounded base reaches past the parent.
	req := DeriveRequest{
		Base: 17, Length: 17, Cursor: 17,
		Perms: Load, Precision: MinPrecision, AllowInexact: true,
	}
	// A narrower parent starting at the odd base cannot contain the rounded
	// base 16, so opting in still fails with ErrOutsideParent.
	// At p=16 the small range [17,34) fits in one granule, so the parent
	// keeps the exact base 17 and cannot contain the rounded child base 16.
	trimmed, _, err := host.FromRequest(17, 17, 17, ObjectPerms, MaxPrecision, false)
	if err != nil {
		t.Fatalf("trimmed parent FromRequest: %v", err)
	}
	if _, err := host.Derive(trimmed, req); !errors.Is(err, ErrOutsideParent) {
		t.Fatalf("left rounding outside parent: want ErrOutsideParent, got %v", err)
	}
}

// TestZeroLengthObject verifies zero-length ranges encode exactly, never
// claim extra bytes, and constrain the cursor to the single base address.
func TestZeroLengthObject(t *testing.T) {
	host := NewHost()
	root, enc, err := host.FromRequest(40, 0, 40, Load|Derive, MinPrecision, false)
	if err != nil {
		t.Fatalf("zero-length root: %v", err)
	}
	if !enc.Exact || enc.Base != 40 || enc.Top != 40 || enc.Length != 0 || enc.Granule != 1 {
		t.Fatalf("zero-length encoding wrong: %+v", enc)
	}
	if enc.Extra != (ExtraCoverage{}) {
		t.Fatalf("zero-length must have no extra coverage: %+v", enc.Extra)
	}

	// Zero-length child is exact; cursor must equal the base (base == top).
	child, err := host.Derive(root, DeriveRequest{
		Base: 40, Length: 0, Cursor: 40,
		Perms: Load, Precision: MinPrecision,
	})
	if err != nil {
		t.Fatalf("zero-length derive: %v", err)
	}
	if child.Cap.Length != 0 || !child.Encoding.Exact {
		t.Fatalf("zero-length child wrong: %+v", child)
	}
	if _, err := host.Derive(root, DeriveRequest{
		Base: 40, Length: 0, Cursor: 41,
		Perms: Load, Precision: MinPrecision,
	}); !errors.Is(err, ErrCursorOutside) {
		t.Fatalf("zero-length cursor past base: want ErrCursorOutside, got %v", err)
	}

	// No address is accessible through a zero-length capability.
	if root.Accessible(40) || root.Accessible(39) {
		t.Fatalf("zero-length capability must not access any byte: %+v", root)
	}
}

// TestTopOverflow exercises both exact requests at the address-space ceiling
// and rounded encodings whose top crosses a (small-domain) limit.
func TestTopOverflow(t *testing.T) {
	full := NewHost()
	const top32 = uint64(1) << 32

	// A range ending exactly at 2^32 is valid; one byte further overflows.
	if _, _, err := full.FromRequest(top32-1, 1, top32-1, Load, MinPrecision, false); err != nil {
		t.Fatalf("range ending exactly at 2^32 should be valid: %v", err)
	}
	if _, _, err := full.FromRequest(top32-1, 2, top32-1, Load, MinPrecision, false); !errors.Is(err, ErrRangeOverflow) {
		t.Fatalf("request crossing 2^32: want ErrRangeOverflow, got %v", err)
	}
	if _, _, err := full.FromRequest(top32, 0, top32, Load, MinPrecision, false); !errors.Is(err, ErrRangeOverflow) {
		t.Fatalf("base at 2^32: want ErrRangeOverflow, got %v", err)
	}

	// In an odd-limit domain, rounding [28,4) up to granule 8 crosses the
	// limit 31: rounded top 32 > 31 even though the request itself fits.
	const limit uint64 = 31
	small := testHost(limit)
	if _, err := small.Encode(28, 4, MinPrecision); !errors.Is(err, ErrRangeOverflow) {
		t.Fatalf("rounded top over limit: want ErrRangeOverflow, got %v", err)
	}
	if _, err := small.Encode(30, 2, MinPrecision); !errors.Is(err, ErrRangeOverflow) {
		t.Fatalf("request crossing limit: want ErrRangeOverflow, got %v", err)
	}
}

// TestPermissionEscalation ensures derivation only narrows permissions and
// the host-reserved Unseal bit can never be requested.
func TestPermissionEscalation(t *testing.T) {
	host := NewHost()
	root, _, err := host.FromRequest(0, 32, 0, Load|Derive, MinPrecision, true)
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	// Adding a permission the parent lacks (Store/Execute) is rejected.
	if _, err := host.Derive(root, DeriveRequest{
		Base: 0, Length: 8, Cursor: 0,
		Perms: Load | Store, Precision: MinPrecision,
	}); !errors.Is(err, ErrPermissionEscalation) {
		t.Fatalf("adding Store: want ErrPermissionEscalation, got %v", err)
	}
	if _, err := host.Derive(root, DeriveRequest{
		Base: 0, Length: 8, Cursor: 0,
		Perms: Load | Unseal, Precision: MinPrecision,
	}); !errors.Is(err, ErrUnsealForbidden) {
		t.Fatalf("requesting Unseal: want ErrUnsealForbidden, got %v", err)
	}

	// A parent without Derive cannot be derived from at all.
	noDerive, _, err := host.FromRequest(0, 32, 0, Load, MinPrecision, true)
	if err != nil {
		t.Fatalf("noDerive root: %v", err)
	}
	if _, err := host.Derive(noDerive, DeriveRequest{
		Base: 0, Length: 8, Cursor: 0,
		Perms: Load, Precision: MinPrecision,
	}); !errors.Is(err, ErrPermissionEscalation) {
		t.Fatalf("derive without Derive bit: want ErrPermissionEscalation, got %v", err)
	}

	// A narrowed child keeps the object generation and cannot regain rights.
	child, err := host.Derive(root, DeriveRequest{
		Base: 0, Length: 16, Cursor: 0,
		Perms: Load, Precision: MinPrecision,
	})
	if err != nil {
		t.Fatalf("narrowed child: %v", err)
	}
	if child.Cap.Generation != root.Generation {
		t.Fatalf("generation must be preserved: child=%d root=%d", child.Cap.Generation, root.Generation)
	}
	if child.Cap.Perms != Load || child.Cap.CanStore(0) || child.Cap.CanExecute(0) {
		t.Fatalf("child gained permissions: %+v", child.Cap)
	}
	if _, err := host.Derive(child.Cap, DeriveRequest{
		Base: 0, Length: 8, Cursor: 0,
		Perms: Derive, Precision: MinPrecision,
	}); !errors.Is(err, ErrPermissionEscalation) {
		t.Fatalf("child regaining Derive: want ErrPermissionEscalation, got %v", err)
	}
}

// TestSealTypeConfusion covers sealing, the hard block on sealed use, and
// type confusion attacks against the Unseal key.
func TestSealTypeConfusion(t *testing.T) {
	host := NewHost()
	root, _, err := host.FromRequest(8, 16, 8, Load|Store|Execute|Derive, MinPrecision, true)
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	const typeA, typeB uint32 = 0x1111, 0x2222
	sealed, err := host.Seal(root, typeA)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !sealed.Sealed || sealed.SealedType != typeA || sealed.Perms != 0 {
		t.Fatalf("sealed state wrong: %+v", sealed)
	}

	// Sealed capability denies all object operations and further derivation.
	if sealed.CanLoad(8) || sealed.CanStore(8) || sealed.CanExecute(8) || sealed.Accessible(8) {
		t.Fatalf("sealed capability must not be usable: %+v", sealed)
	}
	if _, err := host.Derive(sealed, DeriveRequest{
		Base: 8, Length: 8, Cursor: 8,
		Perms: 0, Precision: MinPrecision,
	}); !errors.Is(err, ErrSealed) {
		t.Fatalf("derive from sealed: want ErrSealed, got %v", err)
	}

	// Host keys are only minted by the host and are bound to a sealed type.
	keyA := host.MintKey(typeA)
	keyB := host.MintKey(typeB)
	if keyA.Perms != Unseal || keyA.SealedType != typeA || !keyA.Tag {
		t.Fatalf("minted key wrong: %+v", keyA)
	}

	// A type-B key, an untagged/cleared key, and a forged structure all fail.
	if _, err := host.Unseal(sealed, keyB); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong sealed type: want ErrWrongKey, got %v", err)
	}
	cleared := keyA
	cleared.Tag = false
	if _, err := host.Unseal(sealed, cleared); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("cleared tag key: want ErrWrongKey, got %v", err)
	}
	// A key-shaped struct fabricated directly (never minted) has no host
	// registration and must not authenticate an Unseal.
	forged := Cap{Perms: Unseal, SealedType: typeA, Tag: true}
	if _, err := host.Unseal(sealed, forged); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("forged key struct: want ErrWrongKey, got %v", err)
	}

	// Re-seal and use the correct key: range, cursor and generation survive.
	reSealed, err := host.Seal(root, typeA)
	if err != nil {
		t.Fatalf("re-seal: %v", err)
	}
	restored, err := host.Unseal(reSealed, keyA)
	if err != nil {
		t.Fatalf("correct Unseal: %v", err)
	}
	if restored.Sealed || restored.SealedType != 0 {
		t.Fatalf("restored cap still sealed: %+v", restored)
	}
	if restored.Base != root.Base || restored.Length != root.Length ||
		restored.Cursor != root.Cursor || restored.Generation != root.Generation {
		t.Fatalf("unseal must preserve range/cursor/generation: %+v vs %+v", restored, root)
	}

	// Unsealing an unsealed capability, or with the Unseal bit stripped, fails.
	if _, err := host.Unseal(restored, keyA); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("unseal unsealed: want ErrNotSealed, got %v", err)
	}
	stripped := keyA
	stripped.Perms = 0
	if _, err := host.Unseal(reSealed, stripped); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("key without Unseal: want ErrWrongKey, got %v", err)
	}

	// The host never lets a request construct an Unseal-bearing capability.
	if _, _, err := host.FromRequest(0, 8, 0, Unseal, MinPrecision, false); !errors.Is(err, ErrUnsealForbidden) {
		t.Fatalf("forged Unseal request: want ErrUnsealForbidden, got %v", err)
	}
}

// TestExplicitInexactWithWideParent validates the explicit AllowInexact path:
// default derivation fails on rounding, but a parent whose original range
// fully contains the rounded range accepts it and reports the extra bytes.
func TestExplicitInexactWithWideParent(t *testing.T) {
	host := NewHost()

	// Wide, already-rounded parent [0,64) contains any rounding below.
	wide, _, err := host.FromRequest(0, 64, 0, ObjectPerms, MinPrecision, true)
	if err != nil {
		t.Fatalf("wide parent: %v", err)
	}
	req := DeriveRequest{
		Base: 3, Length: 31, Cursor: 4,
		Perms: Load | Derive, Precision: MinPrecision,
	}

	// Default: inexact encoding is refused.
	if _, err := host.Derive(wide, req); !errors.Is(err, ErrInexactDenied) {
		t.Fatalf("default inexact: want ErrInexactDenied, got %v", err)
	}

	// Explicit opt-in succeeds because the parent fully contains [0,36).
	req.AllowInexact = true
	res, err := host.Derive(wide, req)
	if err != nil {
		t.Fatalf("explicit inexact inside wide parent: %v", err)
	}
	// length 31 -> granule 2; [3,34) rounds to [2,34), pulling in byte 2.
	if res.Encoding.Exact || res.Cap.Base != 2 || res.Cap.Top() != 34 ||
		res.Encoding.Granule != 2 {
		t.Fatalf("rounded child wrong: %+v", res)
	}
	if res.Encoding.Extra.Left != (Interval{Low: 2, High: 2}) ||
		res.Encoding.Extra.Right != (Interval{}) {
		t.Fatalf("extra coverage not reported: %+v", res.Encoding.Extra)
	}
	if res.Cap.Cursor != 4 || res.Cap.Generation != wide.Generation {
		t.Fatalf("cursor/generation wrong: %+v", res.Cap)
	}

	// Even with AllowInexact, rounding outside the parent is rejected.
	narrow, err := host.Derive(wide, DeriveRequest{
		Base: 0, Length: 32, Cursor: 0,
		Perms: ObjectPerms, Precision: MinPrecision,
	})
	if err != nil {
		t.Fatalf("narrow parent: %v", err)
	}
	if _, err := host.Derive(narrow.Cap, req); !errors.Is(err, ErrOutsideParent) {
		t.Fatalf("inexact escaping parent: want ErrOutsideParent, got %v", err)
	}
}

// TestPrecisionAndAccess checks precision bounds and the access predicates at
// range boundaries.
func TestPrecisionAndAccess(t *testing.T) {
	host := NewHost()
	if _, err := host.Encode(0, 4, MinPrecision-1); !errors.Is(err, ErrInvalidPrecision) {
		t.Fatalf("precision below range: want ErrInvalidPrecision, got %v", err)
	}
	if _, err := host.Encode(0, 4, MaxPrecision+1); !errors.Is(err, ErrInvalidPrecision) {
		t.Fatalf("precision above range: want ErrInvalidPrecision, got %v", err)
	}

	cap_, _, err := host.FromRequest(8, 16, 8, Load|Store, MinPrecision, true)
	if err != nil {
		t.Fatalf("cap: %v", err)
	}
	if !cap_.CanLoad(8) || !cap_.CanStore(23) || cap_.CanLoad(24) || cap_.CanLoad(7) {
		t.Fatalf("boundary access wrong: %+v", cap_)
	}
	if cap_.CanExecute(8) {
		t.Fatalf("Execute bit must not be implied: %+v", cap_)
	}
	cleared := cap_
	cleared.Tag = false
	if cleared.CanLoad(8) {
		t.Fatalf("untagged capability must deny access")
	}
}

// TestFullSpaceEncoding validates boundary arithmetic over the real 32-bit
// address space, including the largest representable range.
func TestFullSpaceEncoding(t *testing.T) {
	host := NewHost()
	const top32 = uint64(1) << 32

	// Full address space at p=16: capacity 65536, granule 65536, e=16.
	full, err := host.Encode(0, top32, MaxPrecision)
	if err != nil {
		t.Fatalf("full-space encode: %v", err)
	}
	if full.Granule != 1<<16 || full.E != 16 || full.Base != 0 ||
		full.Top != top32 || !full.Exact || full.Extra != (ExtraCoverage{}) {
		t.Fatalf("full-space encoding wrong: %+v", full)
	}

	// A single unaligned byte keeps granule 1; only the base is rounded.
	one, err := host.Encode(1, 1, MinPrecision)
	if err != nil {
		t.Fatalf("single-byte encode: %v", err)
	}
	// With granule 1 every address is aligned, so a single byte is exact and
	// covers no extra bytes.
	if one.Granule != 1 || one.Base != 1 || one.Top != 2 || !one.Exact ||
		one.Extra != (ExtraCoverage{}) {
		t.Fatalf("single-byte rounding wrong: %+v", one)
	}

	// A 17-byte range at p=4 needs granule 2 and rounds the odd base down.
	two, err := host.Encode(1, 17, MinPrecision)
	if err != nil {
		t.Fatalf("two-granule encode: %v", err)
	}
	// [1,18) rounds down to base 0 (byte 0 added); top 18 is already even.
	if two.Granule != 2 || two.E != 1 || two.Base != 0 || two.Top != 18 ||
		two.Extra.Left != (Interval{Low: 0, High: 0}) ||
		two.Extra.Right != (Interval{}) {
		t.Fatalf("two-granule rounding wrong: %+v", two)
	}

	// Operations on an untagged capability must fail closed.
	var zero Cap
	if _, err := host.Derive(zero, DeriveRequest{Precision: MinPrecision}); !errors.Is(err, ErrTagCleared) {
		t.Fatalf("derive untagged: want ErrTagCleared, got %v", err)
	}
	if _, err := host.Seal(zero, 1); !errors.Is(err, ErrTagCleared) {
		t.Fatalf("seal untagged: want ErrTagCleared, got %v", err)
	}
	if zero.Accessible(0) {
		t.Fatalf("zero capability must not be accessible")
	}
}
