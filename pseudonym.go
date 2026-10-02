package tracevault

import (
	"encoding/hex"
	"strconv"
)

func fmtEpoch(epoch int64) string {
	return strconv.FormatInt(epoch, 10)
}

// Pseudonym derives a time-varying pseudonym for entityID at the given epoch
// via a keyed PRF (HMAC-SHA256). Pseudonyms rotate every epoch, so records of
// the same entity cannot be linked across time without the master key.
func Pseudonym(masterKey []byte, entityID string, epoch int64) string {
	sum := deriveKey(masterKey, "pseudonym", entityID, fmtEpoch(epoch))
	return hex.EncodeToString(sum[:16])
}
