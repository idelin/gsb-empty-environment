package parity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
)

// QueryOutcome is the recorded observable outcome of one callback invocation.
type QueryOutcome string

const (
	OutcomeEven            QueryOutcome = "even"
	OutcomeOdd             QueryOutcome = "odd"
	OutcomeUnavailable     QueryOutcome = "unavailable"
	OutcomeVersionMismatch QueryOutcome = "version_mismatch"
	OutcomeInvalidQuery    QueryOutcome = "invalid_query"
	OutcomeOracleError     QueryOutcome = "oracle_error"
)

// QueryRecord is one observable query and its observable outcome.
//
// It intentionally has no field for callback-internal plaintext. The package
// never receives such a value.
type QueryRecord struct {
	// Seq is the monotonic callback invocation number (0 based).
	Seq int `json:"seq"`
	// Step is the logical power-of-two contraction step currently pending.
	Step int `json:"step"`
	// Kind is "power2" for q*2^e queries or "blind" for q*r^e queries.
	Kind string `json:"kind"`
	// X is the exact 0 <= x < N value sent to the callback, as hex.
	X string `json:"x"`
	// Multiplier is the exact integer T for which the oracle reveals
	// parity((T*m) mod N), as hex. It is derivation material.
	Multiplier string `json:"multiplier"`
	// R is the blinding factor used, when Kind == "blind", as hex.
	R string `json:"r,omitempty"`
	// RFactors are the known exact factors of R (with multiplicity), as hex.
	RFactors []string `json:"r_factors,omitempty"`
	// Outcome is the classified callback outcome.
	Outcome QueryOutcome `json:"outcome"`
	// Parity is 0/1 when Outcome is even/odd, otherwise omitted.
	Parity *int `json:"parity,omitempty"`
}

// digestTuple is the canonical, ordered, deterministic preimage for the
// history digest. Struct field order is part of the digest contract.
type digestTuple struct {
	Seq        int      `json:"seq"`
	Step       int      `json:"step"`
	Kind       string   `json:"kind"`
	X          string   `json:"x"`
	Multiplier string   `json:"multiplier"`
	R          string   `json:"r,omitempty"`
	RFactors   []string `json:"r_factors,omitempty"`
	Outcome    string   `json:"outcome"`
	Parity     *int     `json:"parity,omitempty"`
}

// DigestHistory computes the stable SHA-256 digest of the COMPLETE ordered
// query history. It hex-encodes the 32-byte hash.
func DigestHistory(history []QueryRecord) string {
	tuples := make([]digestTuple, len(history))
	for i, rec := range history {
		tuples[i] = digestTuple{
			Seq:        rec.Seq,
			Step:       rec.Step,
			Kind:       rec.Kind,
			X:          normalizeHex(rec.X),
			Multiplier: normalizeHex(rec.Multiplier),
			R:          normalizeHex(rec.R),
			RFactors:   normalizeHexSlice(rec.RFactors),
			Outcome:    string(rec.Outcome),
			Parity:     rec.Parity,
		}
	}
	buf, err := json.Marshal(tuples)
	if err != nil {
		// digestTuple is always JSON-safe; a failure here is a programming bug.
		panic(err)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// DigestBinding computes the stable digest of the public binding.
func DigestBinding(b Binding) string {
	payload := struct {
		N   string `json:"n"`
		E   string `json:"e"`
		C   string `json:"c"`
		Ver string `json:"key_version"`
	}{
		N:   bigHex(b.N),
		E:   bigHex(b.E),
		C:   bigHex(b.C),
		Ver: b.KeyVersion,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func bigHex(v *big.Int) string {
	if v == nil {
		return ""
	}
	return normalizeHex(v.Text(16))
}

// normalizeHex canonicalizes big.Int hex strings to a non-empty lowercase form
// without leading zeros (except "0").
func normalizeHex(s string) string {
	if s == "" {
		return ""
	}
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	// Strip leading zeros.
	i := 0
	for i < len(s)-1 && s[i] == '0' {
		i++
	}
	s = s[i:]
	if s == "" {
		s = "0"
	}
	if neg && s != "0" {
		return "-" + s
	}
	return s
}

func normalizeHexSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = normalizeHex(s)
	}
	return out
}

func hexBig(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return nil
	}
	return v
}
