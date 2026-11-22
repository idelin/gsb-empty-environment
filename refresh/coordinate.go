package refresh

import (
	"crypto/sha256"
	"errors"
	"math/big"
)

// RecipientCoordinate derives the evaluation coordinate x for a canonical
// participant ID inside the field. The mapping is deterministic so every host
// evaluates commitments at the same point without carrying extra configuration.
func RecipientCoordinate(f *Field, canonicalID string) (*big.Int, error) {
	if f == nil {
		return nil, errors.New("refresh: nil field")
	}
	h := sha256.Sum256([]byte("refresh-coordinate-v1|" + canonicalID))
	x := new(big.Int).SetBytes(h[:])
	x.Mod(x, f.P())
	if x.Sign() == 0 {
		return nil, errors.New("refresh: participant hashes to the zero coordinate")
	}
	return x, nil
}
