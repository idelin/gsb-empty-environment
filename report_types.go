package main

type report struct {
	GroupCount    int           `json:"group_count"`
	FragmentCount int           `json:"fragment_count"`
	Groups        []groupReport `json:"groups"`
}

type groupReport struct {
	KeyID              string            `json:"key_id"`
	Nonce              string            `json:"nonce"`
	RecoveredByteCount int               `json:"recovered_byte_count"`
	ConflictCount      int               `json:"conflict_count"`
	Overlaps           []overlapReport   `json:"overlaps"`
	Keystream          []keystreamReport `json:"keystream"`
	Fragments          []fragmentReport  `json:"fragments"`
}

type overlapReport struct {
	FragmentA     string `json:"fragment_a"`
	FragmentB     string `json:"fragment_b"`
	StartBlock    uint64 `json:"start_block"`
	EndBlock      uint64 `json:"end_block"`
	StartPosition uint64 `json:"start_position"`
	EndPosition   uint64 `json:"end_position"`
	OverlapBytes  int    `json:"overlap_bytes"`
}

type keystreamReport struct {
	Position      uint64               `json:"position"`
	Block         uint64               `json:"block"`
	OffsetInBlock int                  `json:"offset_in_block"`
	Conflict      bool                 `json:"conflict"`
	Candidates    []keystreamCandidate `json:"candidates"`
}

type keystreamCandidate struct {
	KeystreamByte string   `json:"keystream_byte"`
	Evidence      evidence `json:"evidence"`
}

type fragmentReport struct {
	FragmentID     string          `json:"fragment_id"`
	AbsStart       uint64          `json:"abs_start"`
	AbsEnd         uint64          `json:"abs_end"`
	KnownCount     int             `json:"known_count"`
	RecoveredCount int             `json:"recovered_count"`
	ConflictCount  int             `json:"conflict_count"`
	Recovered      []recoveredByte `json:"recovered"`
}

type recoveredByte struct {
	Position           uint64   `json:"position"`
	Offset             int      `json:"ciphertext_offset"`
	PlaintextByte      string   `json:"plaintext_byte"`
	Source             evidence `json:"source"`
	FromKnownPlaintext bool     `json:"from_known_plaintext"`
	Conflict           bool     `json:"conflict"`
}
