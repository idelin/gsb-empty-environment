package pagestore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// DictKey pins one exact dictionary version: a logical dictionary ID plus
// the SHA-256 digest of its content. Two dictionaries may share an ID but
// never a key unless their content is byte-identical, so a page bound to a
// DictKey can never be silently decoded with a "latest" dictionary.
type DictKey struct {
	ID     uint64
	Digest [sha256.Size]byte
}

// MakeDictKey derives the version key for (id, content).
func MakeDictKey(id uint64, content []byte) DictKey {
	return DictKey{ID: id, Digest: sha256.Sum256(content)}
}

func (k DictKey) String() string {
	return fmt.Sprintf("dict:%d:%s", k.ID, hex.EncodeToString(k.Digest[:8]))
}

func (k DictKey) fileName() string {
	return fmt.Sprintf("dict_%d_%s.dict", k.ID, hex.EncodeToString(k.Digest[:]))
}

func parseDictFileName(name string) (DictKey, bool) {
	if !strings.HasPrefix(name, "dict_") || !strings.HasSuffix(name, ".dict") {
		return DictKey{}, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(name, "dict_"), ".dict")
	idx := strings.IndexByte(body, '_')
	if idx <= 0 {
		return DictKey{}, false
	}
	id, err := strconv.ParseUint(body[:idx], 10, 64)
	if err != nil {
		return DictKey{}, false
	}
	raw, err := hex.DecodeString(body[idx+1:])
	if err != nil || len(raw) != sha256.Size {
		return DictKey{}, false
	}
	key := DictKey{ID: id}
	copy(key.Digest[:], raw)
	return key, true
}
