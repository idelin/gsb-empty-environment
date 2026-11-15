package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"io"
	"time"
)

const (
	tokenNonceSize = 16
	tokenTagSize   = sha256.Size

	// TokenSize is the encoded anti-clogging token length: a random nonce
	// followed by an HMAC-SHA256 tag.
	TokenSize = tokenNonceSize + tokenTagSize
)

var tokenLabel = []byte("WPA3-SAE anti-clogging token v1\x00")

type tokenID [TokenSize]byte

func makeTokenTag(key *[sha256.Size]byte, station [6]byte, group GroupID, nonce []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, key[:])
	mac.Write(tokenLabel)
	mac.Write(station[:])
	mac.Write([]byte{byte(group >> 8), byte(group)})
	mac.Write(nonce)

	var tag [sha256.Size]byte
	mac.Sum(tag[:0])
	return tag
}

type tokenStatus uint8

const (
	tokenOK tokenStatus = iota
	tokenInvalid
	tokenReplay
)

func (f *Filter) checkTokenLocked(station [6]byte, group GroupID, encoded []byte, now time.Time) tokenStatus {
	if len(encoded) != TokenSize || len(f.keys) == 0 {
		return tokenInvalid
	}

	nonce := encoded[:tokenNonceSize]
	tag := encoded[tokenNonceSize:]
	valid := 0
	for i := range f.keys {
		expected := makeTokenTag(&f.keys[i].key, station, group, nonce)
		valid |= subtle.ConstantTimeCompare(expected[:], tag)
	}
	if valid != 1 {
		return tokenInvalid
	}

	var id tokenID
	copy(id[:], encoded)
	if expires, ok := f.usedTokens[id]; ok && expires.After(now) {
		return tokenReplay
	}
	return tokenOK
}

func (f *Filter) issueTokenLocked(station [6]byte, group GroupID, now time.Time) ([]byte, error) {
	if len(f.keys) == 0 {
		if err := f.addKeyLocked(now); err != nil {
			return nil, err
		}
	}

	token := make([]byte, TokenSize)
	if _, err := io.ReadFull(f.rng, token[:tokenNonceSize]); err != nil {
		return nil, err
	}
	key := &f.keys[len(f.keys)-1].key
	tag := makeTokenTag(key, station, group, token[:tokenNonceSize])
	copy(token[tokenNonceSize:], tag[:])
	return token, nil
}

func (f *Filter) markTokenUsedLocked(encoded []byte, now time.Time) error {
	if len(encoded) != TokenSize {
		return ErrInvalidToken
	}
	if len(f.usedTokens) >= maxUsedTokens {
		return ErrTooManyTransactions
	}
	var id tokenID
	copy(id[:], encoded)
	f.usedTokens[id] = now.Add(f.cfg.TokenTTL)
	return nil
}
