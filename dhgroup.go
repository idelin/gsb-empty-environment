package netsec

import (
	"crypto/rand"
	"errors"
	"math/big"
)

// GroupParams holds the MODP parameters (prime p and generator g) for one
// FC-SW DH group.
type GroupParams struct {
	ID DHGroupID
	P  *big.Int
	G  *big.Int
}

// BitLen reports the prime size in bits.
func (g GroupParams) BitLen() int {
	return g.P.BitLen()
}

// modp1024Prime is the 1024-bit MODP prime from RFC 2409 (Oakley group 2).
// Each source line holds two 32-bit words (32 hex chars); 16 words total.
const modp1024Prime = "FFFFFFFFFFFFFFFFC90FDAA22168C234" +
	"C4C6628B80DC1CD129024E088A67CC74" +
	"020BBEA63B139B22514A08798E3404DD" +
	"EF9519B3CD3A431B302B0A6DF25F1437" +
	"4FE1356D6D51C245E485B576625E7EC6" +
	"F44C42E9A637ED6B0BFF5CB6F406B7ED" +
	"EE386BFB5A899FA5AE9F24117C4B1FE6" +
	"49286651ECE65381FFFFFFFFFFFFFFFF"

// modp2048Prime is the 2048-bit MODP prime from RFC 3526 section 3. Each
// source line holds two 32-bit words (32 hex chars); 32 words total.
const modp2048Prime = "FFFFFFFFFFFFFFFFC90FDAA22168C234" +
	"C4C6628B80DC1CD129024E088A67CC74" +
	"020BBEA63B139B22514A08798E3404DD" +
	"EF9519B3CD3A431B302B0A6DF25F1437" +
	"4FE1356D6D51C245E485B576625E7EC6" +
	"F44C42E9A637ED6B0BFF5CB6F406B7ED" +
	"EE386BFB5A899FA5AE9F24117C4B1FE6" +
	"49286651ECE45B3DC2007CB8A163BF05" +
	"98DA48361C55D39A69163FA8FD24CF5F" +
	"83655D23DCA3AD961C62F356208552BB" +
	"9ED529077096966D670C354E4ABC9804" +
	"F1746C08CA18217C32905E462E36CE3B" +
	"E39E772C180E86039B2783A2EC07A28F" +
	"B5C55DF06F4C52C9DE2BCBF695581718" +
	"3995497CEA956AE515D2261898FA0510" +
	"15728E5A8AACAA68FFFFFFFFFFFFFFFF"

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)
	bigTwo  = big.NewInt(2)
)

// groupTable is the provisioned MODP group registry. Every group uses the
// safe-prime generator g=2 mandated by the referenced RFCs.
var groupTable = func() map[DHGroupID]GroupParams {
	table := map[DHGroupID]GroupParams{}
	register := func(id DHGroupID, hexPrime string) {
		prime, ok := new(big.Int).SetString(hexPrime, 16)
		if !ok {
			panic("dhchap: invalid hard-coded MODP prime")
		}
		table[id] = GroupParams{ID: id, P: prime, G: big.NewInt(2)}
	}
	register(DHGroup1024, modp1024Prime)
	register(DHGroup2048, modp2048Prime)
	return table
}()

// Group returns the provisioned parameters for a DH group. The null group and
// any unprovisioned identifier produce an error.
func Group(id DHGroupID) (GroupParams, error) {
	params, ok := groupTable[id]
	if !ok {
		return GroupParams{}, errors.New("dhchap: DH group not provisioned")
	}
	return params, nil
}

func groupProvisioned(id DHGroupID) bool {
	_, ok := groupTable[id]
	return ok
}

// KeyPair is a local Diffie-Hellman private exponent and its public value.
type KeyPair struct {
	Private *big.Int
	Public  *big.Int
}

// GenerateKeyPair draws a private exponent x from [2, p-2] using a
// cryptographically secure source and returns y = g^x mod p. The private
// range matches the acceptance range enforced on peer values.
func (g GroupParams) GenerateKeyPair() (KeyPair, error) {
	// max = p - 3 bounds the random draw so that x+2 <= p-2.
	max := new(big.Int).Sub(g.P, big.NewInt(3))
	if max.Sign() <= 0 {
		return KeyPair{}, errors.New("dhchap: degenerate DH modulus")
	}
	x, err := rand.Int(rand.Reader, max)
	if err != nil {
		return KeyPair{}, err
	}
	x.Add(x, bigTwo) // shift into [2, p-2]

	y := new(big.Int).Exp(g.G, x, g.P)
	if err := g.ValidatePublicValue(y); err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Private: x, Public: y}, nil
}

// ValidatePublicValue enforces the DH-CHAP anti-small-subgroup check: the
// peer value must satisfy 2 <= y <= p-2. The trivial subgroup elements
// 0, 1 and p-1 (and anything outside the modulus) are rejected.
func (g GroupParams) ValidatePublicValue(y *big.Int) error {
	if y == nil {
		return ErrInvalidDHPublicValue
	}
	upper := new(big.Int).Sub(g.P, bigTwo) // p - 2
	if y.Cmp(bigTwo) < 0 || y.Cmp(upper) > 0 {
		return ErrInvalidDHPublicValue
	}
	return nil
}

// ComputeSharedSecret validates the peer public value and returns
// z = peerY^x mod p. A zero result (which cannot occur for a valid MODP
// exchange) is treated as a protocol failure rather than returned as a key.
func (g GroupParams) ComputeSharedSecret(priv *big.Int, peerY *big.Int) (*big.Int, error) {
	if priv == nil {
		return nil, errors.New("dhchap: missing private exponent")
	}
	if err := g.ValidatePublicValue(peerY); err != nil {
		return nil, err
	}
	z := new(big.Int).Exp(peerY, priv, g.P)
	if z.Cmp(bigZero) == 0 {
		return nil, ErrInvalidDHPublicValue
	}
	return z, nil
}
