package main

const blockSize = 16

type fragment struct {
	ID             string       `json:"id"`
	KeyID          string       `json:"key_id"`
	Nonce          string       `json:"nonce"`
	StartBlock     uint64       `json:"start_block"`
	BlockOffset    int          `json:"block_offset"`
	Ciphertext     string       `json:"ciphertext"`
	KnownPlaintext []knownPlain `json:"known_plaintext,omitempty"`
}

type knownPlain struct {
	Offset  int    `json:"offset"`
	Data    string `json:"data"`
	decoded []byte
}

type decodedFragment struct {
	frag     fragment
	keyHex   string
	nonceHex string
	ks       []byte
	absStart uint64
	absEnd   uint64
	known    map[int]bool
}

type evidence struct {
	FragmentID string `json:"fragment_id"`
	Offset     int    `json:"ciphertext_offset"`
}

type positionState struct {
	candidates map[byte]evidence
	order      []byte
}

func (s *positionState) add(candidate byte, ev evidence) {
	if s.candidates == nil {
		s.candidates = make(map[byte]evidence)
	}
	if _, exists := s.candidates[candidate]; !exists {
		s.candidates[candidate] = ev
		s.order = append(s.order, candidate)
	}
}
