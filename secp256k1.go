package netsec

import (
	"errors"
	"math/big"
)

// secp256k1 is needed to recover the payee key from a BOLT 11 signature.
// Only public operations live here; the package never signs invoices.
var (
	secpP, _  = new(big.Int).SetString("fffffffffffffffffffffffffffffffffffffffffffffffffffffffefffffc2f", 16)
	secpN, _  = new(big.Int).SetString("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141", 16)
	secpGx, _ = new(big.Int).SetString("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798", 16)
	secpGy, _ = new(big.Int).SetString("483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8", 16)
	secp7     = big.NewInt(7)
)

var (
	errInvalidPublicKey = errors.New("invalid secp256k1 public key")
	errInvalidSignature = errors.New("invalid secp256k1 signature")
)

type curvePoint struct {
	x, y *big.Int
}

type jacobianPoint struct {
	x, y, z *big.Int
}

func fieldAdd(a, b *big.Int) *big.Int {
	z := new(big.Int).Add(a, b)
	return z.Mod(z, secpP)
}

func fieldSub(a, b *big.Int) *big.Int {
	z := new(big.Int).Sub(a, b)
	return z.Mod(z, secpP)
}

func fieldMul(a, b *big.Int) *big.Int {
	z := new(big.Int).Mul(a, b)
	return z.Mod(z, secpP)
}

func fieldSquare(a *big.Int) *big.Int {
	z := new(big.Int).Mul(a, a)
	return z.Mod(z, secpP)
}

func fieldDouble(a *big.Int) *big.Int {
	z := new(big.Int).Lsh(a, 1)
	return z.Mod(z, secpP)
}

func fieldTriple(a *big.Int) *big.Int {
	z := new(big.Int).Lsh(a, 1)
	z.Add(z, a)
	return z.Mod(z, secpP)
}

func infinityJacobian() jacobianPoint {
	return jacobianPoint{
		x: new(big.Int),
		y: new(big.Int),
		z: new(big.Int),
	}
}

func jacobianFromAffine(p *curvePoint) jacobianPoint {
	if p == nil {
		return infinityJacobian()
	}
	return jacobianPoint{
		x: new(big.Int).Set(p.x),
		y: new(big.Int).Set(p.y),
		z: big.NewInt(1),
	}
}

func affineFromJacobian(p jacobianPoint) *curvePoint {
	if p.z.Sign() == 0 {
		return nil
	}
	zinv := new(big.Int).ModInverse(p.z, secpP)
	if zinv == nil {
		return nil
	}
	zinv2 := fieldSquare(zinv)
	zinv3 := fieldMul(zinv2, zinv)
	return &curvePoint{
		x: fieldMul(p.x, zinv2),
		y: fieldMul(p.y, zinv3),
	}
}

func doubleJacobian(p jacobianPoint) jacobianPoint {
	if p.z.Sign() == 0 || p.y.Sign() == 0 {
		return infinityJacobian()
	}

	a := fieldSquare(p.x)
	b := fieldSquare(p.y)
	c := fieldSquare(b)

	xb := fieldAdd(p.x, b)
	d := fieldDouble(fieldSub(fieldSub(fieldSquare(xb), a), c))
	e := fieldTriple(a)
	f := fieldSquare(e)
	x3 := fieldSub(f, fieldDouble(d))
	y3 := fieldSub(fieldMul(e, fieldSub(d, x3)), new(big.Int).Lsh(c, 3))
	y3.Mod(y3, secpP)
	z3 := fieldDouble(fieldMul(p.y, p.z))

	return jacobianPoint{x: x3, y: y3, z: z3}
}

func addJacobian(p, q jacobianPoint) jacobianPoint {
	if p.z.Sign() == 0 {
		return jacobianPoint{
			x: new(big.Int).Set(q.x),
			y: new(big.Int).Set(q.y),
			z: new(big.Int).Set(q.z),
		}
	}
	if q.z.Sign() == 0 {
		return jacobianPoint{
			x: new(big.Int).Set(p.x),
			y: new(big.Int).Set(p.y),
			z: new(big.Int).Set(p.z),
		}
	}

	z1z1 := fieldSquare(p.z)
	z2z2 := fieldSquare(q.z)
	u1 := fieldMul(p.x, z2z2)
	u2 := fieldMul(q.x, z1z1)
	s1 := fieldMul(p.y, fieldMul(q.z, z2z2))
	s2 := fieldMul(q.y, fieldMul(p.z, z1z1))

	if u1.Cmp(u2) == 0 {
		if s1.Cmp(s2) == 0 {
			return doubleJacobian(p)
		}
		return infinityJacobian()
	}

	h := fieldSub(u2, u1)
	i := fieldSquare(fieldDouble(h))
	j := fieldMul(h, i)
	r := fieldDouble(fieldSub(s2, s1))
	v := fieldMul(u1, i)
	x3 := fieldSub(fieldSub(fieldSquare(r), j), fieldDouble(v))
	y3 := fieldSub(fieldMul(r, fieldSub(v, x3)), fieldDouble(fieldMul(s1, j)))
	z3 := fieldMul(fieldSub(fieldSub(fieldSquare(fieldAdd(p.z, q.z)), z1z1), z2z2), h)

	return jacobianPoint{x: x3, y: y3, z: z3}
}

func scalarMult(p *curvePoint, k *big.Int) *curvePoint {
	if p == nil || k == nil || k.Sign() == 0 {
		return nil
	}
	kk := new(big.Int).Mod(k, secpN)
	if kk.Sign() == 0 {
		return nil
	}

	base := jacobianFromAffine(p)
	result := infinityJacobian()
	for i := kk.BitLen() - 1; i >= 0; i-- {
		result = doubleJacobian(result)
		if kk.Bit(i) == 1 {
			result = addJacobian(result, base)
		}
	}
	return affineFromJacobian(result)
}

func scalarBaseMult(k *big.Int) *curvePoint {
	return scalarMult(&curvePoint{x: secpGx, y: secpGy}, k)
}

func addPoints(a, b *curvePoint) *curvePoint {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return affineFromJacobian(addJacobian(jacobianFromAffine(a), jacobianFromAffine(b)))
}

func negatePoint(p *curvePoint) *curvePoint {
	if p == nil {
		return nil
	}
	y := new(big.Int).Sub(secpP, p.y)
	y.Mod(y, secpP)
	return &curvePoint{x: new(big.Int).Set(p.x), y: y}
}

func onCurve(p *curvePoint) bool {
	if p == nil || p.x == nil || p.y == nil {
		return false
	}
	if p.x.Sign() < 0 || p.x.Cmp(secpP) >= 0 || p.y.Sign() < 0 || p.y.Cmp(secpP) >= 0 {
		return false
	}
	left := fieldSquare(p.y)
	right := fieldAdd(fieldMul(fieldSquare(p.x), p.x), secp7)
	return left.Cmp(right) == 0
}

func parsePubKey(data []byte) (*curvePoint, error) {
	if len(data) != 33 || (data[0] != 0x02 && data[0] != 0x03) {
		return nil, errInvalidPublicKey
	}
	x := new(big.Int).SetBytes(data[1:])
	if x.Cmp(secpP) >= 0 {
		return nil, errInvalidPublicKey
	}

	alpha := fieldAdd(fieldMul(fieldSquare(x), x), secp7)
	exp := new(big.Int).Add(secpP, big.NewInt(1))
	exp.Rsh(exp, 2)
	y := new(big.Int).Exp(alpha, exp, secpP)
	if fieldSquare(y).Cmp(alpha) != 0 {
		return nil, errInvalidPublicKey
	}
	if byte(y.Bit(0)) != data[0]&1 {
		y.Sub(secpP, y)
	}
	point := &curvePoint{x: x, y: y}
	if !onCurve(point) {
		return nil, errInvalidPublicKey
	}
	return point, nil
}

func compressedPubKey(p *curvePoint) []byte {
	if p == nil {
		return nil
	}
	out := make([]byte, 33)
	if p.y.Bit(0) == 1 {
		out[0] = 0x03
	} else {
		out[0] = 0x02
	}
	p.x.FillBytes(out[1:])
	return out
}

// recoverPubKey implements ECDSA public key recovery on secp256k1.
func recoverPubKey(hash []byte, r, s *big.Int, recoveryID byte) (*curvePoint, error) {
	if len(hash) == 0 || recoveryID > 3 || r == nil || s == nil {
		return nil, errInvalidSignature
	}
	if r.Sign() <= 0 || r.Cmp(secpN) >= 0 || s.Sign() <= 0 || s.Cmp(secpN) >= 0 {
		return nil, errInvalidSignature
	}

	x := new(big.Int).Set(r)
	if recoveryID&2 != 0 {
		x.Add(x, secpN)
	}
	if x.Cmp(secpP) >= 0 {
		return nil, errInvalidSignature
	}

	alpha := fieldAdd(fieldMul(fieldSquare(x), x), secp7)
	exp := new(big.Int).Add(secpP, big.NewInt(1))
	exp.Rsh(exp, 2)
	y := new(big.Int).Exp(alpha, exp, secpP)
	if fieldSquare(y).Cmp(alpha) != 0 {
		return nil, errInvalidSignature
	}
	if byte(y.Bit(0)) != recoveryID&1 {
		y.Sub(secpP, y)
	}

	R := &curvePoint{x: x, y: y}
	if !onCurve(R) || scalarMult(R, secpN) != nil {
		return nil, errInvalidSignature
	}

	e := new(big.Int).SetBytes(hash)
	e.Mod(e, secpN)
	rInv := new(big.Int).ModInverse(r, secpN)
	if rInv == nil {
		return nil, errInvalidSignature
	}

	sR := scalarMult(R, s)
	eG := scalarBaseMult(e)
	sum := addPoints(sR, negatePoint(eG))
	if sum == nil {
		return nil, errInvalidSignature
	}
	Q := scalarMult(sum, rInv)
	if Q == nil || !onCurve(Q) {
		return nil, errInvalidSignature
	}
	return Q, nil
}
