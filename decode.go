package main

import (
	"encoding/hex"
	"fmt"
	"math"
)

func decodeHex(value, field, id string) ([]byte, error) {
	raw, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("fragment %q: %s is not valid hex: %w", id, field, err)
	}
	return raw, nil
}

func decodeFragment(in fragment) (*decodedFragment, error) {
	if in.ID == "" {
		return nil, fmt.Errorf("fragment with empty id")
	}
	if in.KeyID == "" {
		return nil, fmt.Errorf("fragment %q: key_id is required", in.ID)
	}
	if in.Nonce == "" {
		return nil, fmt.Errorf("fragment %q: nonce is required", in.ID)
	}
	if in.BlockOffset < 0 || in.BlockOffset >= blockSize {
		return nil, fmt.Errorf("fragment %q: block_offset must be in [0,15], got %d", in.ID, in.BlockOffset)
	}
	if in.StartBlock > (math.MaxUint64-uint64(in.BlockOffset))/blockSize {
		return nil, fmt.Errorf("fragment %q: start_block overflows keystream positions", in.ID)
	}

	keyRaw, err := decodeHex(in.KeyID, "key_id", in.ID)
	if err != nil {
		return nil, err
	}
	nonceRaw, err := decodeHex(in.Nonce, "nonce", in.ID)
	if err != nil {
		return nil, err
	}
	ciphertext, err := decodeHex(in.Ciphertext, "ciphertext", in.ID)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 {
		return nil, fmt.Errorf("fragment %q: ciphertext must not be empty", in.ID)
	}

	absStart := in.StartBlock*blockSize + uint64(in.BlockOffset)
	if uint64(len(ciphertext)) > math.MaxUint64-absStart {
		return nil, fmt.Errorf("fragment %q: ciphertext range overflows keystream positions", in.ID)
	}

	df := &decodedFragment{
		frag:     in,
		keyHex:   hex.EncodeToString(keyRaw),
		nonceHex: hex.EncodeToString(nonceRaw),
		ks:       ciphertext,
		absStart: absStart,
		absEnd:   absStart + uint64(len(ciphertext)),
		known:    make(map[int]bool),
	}

	for kpIdx := range in.KnownPlaintext {
		kp := &in.KnownPlaintext[kpIdx]
		if kp.Offset < 0 {
			return nil, fmt.Errorf("fragment %q: known_plaintext offset must be non-negative", in.ID)
		}
		data, err := decodeHex(kp.Data, "known_plaintext", in.ID)
		if err != nil {
			return nil, err
		}
		if kp.Offset+len(data) > len(ciphertext) {
			return nil, fmt.Errorf("fragment %q: known_plaintext at offset %d exceeds ciphertext length %d", in.ID, kp.Offset, len(ciphertext))
		}
		kp.decoded = data
		for i := range data {
			df.known[kp.Offset+i] = true
		}
	}

	return df, nil
}
