package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math/big"
	"strings"
)

const (
	checkpointVersion = 1
	digestDomain      = "example.org/netsec/q0017/parity-oracle/checkpoint/v1"
)

// QueryRecord is one observable callback attempt. It never contains a
// decrypted plaintext.
type QueryRecord struct {
	Attempt int    `json:"attempt"`
	Step    int    `json:"step"`
	Query   string `json:"query"`
	Result  string `json:"result"`
}

// Checkpoint is a serializable recovery position. HistoryDigest binds N, E, C,
// and the complete attempt history. History is retained so a resume can verify
// the digest and replay all interval transitions exactly.
type Checkpoint struct {
	Version       int           `json:"version"`
	N             string        `json:"n"`
	E             string        `json:"e"`
	C             string        `json:"c"`
	Budget        int           `json:"budget"`
	QueriesUsed   int           `json:"queries_used"`
	Step          int           `json:"step"`
	Lo            string        `json:"lo"`
	Hi            string        `json:"hi"`
	PendingQuery  string        `json:"pending_query"`
	HistoryDigest string        `json:"history_digest"`
	History       []QueryRecord `json:"history"`
}

// Checkpoint snapshots the current state. The returned value is JSON-ready and
// contains no private or callback-internal values.
func (s *Session) Checkpoint() *Checkpoint {
	return &Checkpoint{
		Version:       checkpointVersion,
		N:             s.n.Text(16),
		E:             s.e.Text(16),
		C:             s.c.Text(16),
		Budget:        s.max,
		QueriesUsed:   s.used,
		Step:          s.step,
		Lo:            s.lo.Text(16),
		Hi:            s.hi.Text(16),
		PendingQuery:  s.pending.Text(16),
		HistoryDigest: s.digestHex(),
		History:       cloneHistory(s.history),
	}
}

// EncodeCheckpoint serializes a checkpoint as JSON.
func EncodeCheckpoint(cp *Checkpoint) ([]byte, error) {
	if cp == nil {
		return nil, fmt.Errorf("%w: nil checkpoint", ErrCheckpointInvalid)
	}
	return json.Marshal(cp)
}

// DecodeCheckpoint parses a checkpoint JSON document. Unknown fields and
// trailing data are rejected.
func DecodeCheckpoint(data []byte) (*Checkpoint, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cp Checkpoint
	if err := dec.Decode(&cp); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCheckpointInvalid, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing JSON data", ErrCheckpointInvalid)
	}
	return &cp, nil
}

// ResumeSession validates a checkpoint against the caller's public parameters
// and a (possibly larger) total query budget. It replays the complete history,
// checking the digest, every query, and every interval transition.
func ResumeSession(cfg Config, cp *Checkpoint) (*Session, error) {
	n, e, c, err := validateConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cp == nil {
		return nil, fmt.Errorf("%w: nil checkpoint", ErrCheckpointInvalid)
	}
	if cp.Version != checkpointVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrCheckpointInvalid, cp.Version)
	}

	cpN, err := parseCanonicalHex("n", cp.N)
	if err != nil {
		return nil, err
	}
	cpE, err := parseCanonicalHex("e", cp.E)
	if err != nil {
		return nil, err
	}
	cpC, err := parseCanonicalHex("c", cp.C)
	if err != nil {
		return nil, err
	}
	if cpN.Cmp(n) != 0 || cpE.Cmp(e) != 0 || cpC.Cmp(c) != 0 {
		return nil, fmt.Errorf("%w: N, E, or C differs from the checkpoint", ErrCheckpointMismatch)
	}

	if cp.QueriesUsed < 0 || cp.QueriesUsed != len(cp.History) {
		return nil, fmt.Errorf("%w: query count does not match history", ErrCheckpointInvalid)
	}
	if cp.Budget <= 0 || cp.Budget < cp.QueriesUsed {
		return nil, fmt.Errorf("%w: invalid recorded budget", ErrCheckpointInvalid)
	}
	if cp.QueriesUsed > cfg.MaxQueries {
		return nil, fmt.Errorf("%w: quota already exceeds the new budget", ErrCheckpointInvalid)
	}
	if cp.Step < 0 || cp.Step > cp.QueriesUsed {
		return nil, fmt.Errorf("%w: invalid interval step", ErrCheckpointInvalid)
	}

	lo, err := parseCanonicalHex("lo", cp.Lo)
	if err != nil {
		return nil, err
	}
	hi, err := parseCanonicalHex("hi", cp.Hi)
	if err != nil {
		return nil, err
	}
	if lo.Cmp(hi) > 0 {
		return nil, fmt.Errorf("%w: lower endpoint exceeds upper endpoint", ErrCheckpointInvalid)
	}
	width := new(big.Int).Sub(hi, lo)
	if width.Cmp(n) != 0 {
		return nil, fmt.Errorf("%w: interval width is not N", ErrCheckpointInvalid)
	}
	if new(big.Int).Mod(new(big.Int).Set(lo), n).Sign() != 0 {
		return nil, fmt.Errorf("%w: interval endpoints are not N-aligned", ErrCheckpointInvalid)
	}

	pending, err := parseCanonicalHex("pending_query", cp.PendingQuery)
	if err != nil {
		return nil, err
	}
	if pending.Cmp(n) >= 0 {
		return nil, fmt.Errorf("%w: pending query outside the callback domain", ErrCheckpointInvalid)
	}

	digestBytes, err := hex.DecodeString(cp.HistoryDigest)
	if err != nil || len(digestBytes) != sha256.Size || cp.HistoryDigest != strings.ToLower(cp.HistoryDigest) {
		return nil, fmt.Errorf("%w: malformed history digest", ErrCheckpointInvalid)
	}

	h := newTranscriptDigest(cpN, cpE, cpC)
	for i, rec := range cp.History {
		if err := validateRecord(rec, i+1); err != nil {
			return nil, err
		}
		writeQueryRecord(h, rec)
	}
	if hex.EncodeToString(h.Sum(nil)) != cp.HistoryDigest {
		return nil, fmt.Errorf("%w: history digest mismatch", ErrCheckpointInvalid)
	}

	replayLo, replayHi, replayStep, replayPending, usable, err := replayHistory(cpN, cpE, cpC, cp.History)
	if err != nil {
		return nil, err
	}
	if replayLo.Cmp(lo) != 0 || replayHi.Cmp(hi) != 0 || replayStep != cp.Step {
		return nil, fmt.Errorf("%w: interval does not match replayed history", ErrCheckpointInvalid)
	}
	if replayPending.Cmp(pending) != 0 {
		return nil, fmt.Errorf("%w: pending query does not match replayed history", ErrCheckpointInvalid)
	}

	return &Session{
		n:       n,
		e:       e,
		c:       c,
		pow2e:   new(big.Int).Exp(big.NewInt(2), e, n),
		lo:      replayLo,
		hi:      replayHi,
		step:    replayStep,
		pending: replayPending,
		used:    cp.QueriesUsed,
		max:     cfg.MaxQueries,
		usable:  usable,
		history: cloneHistory(cp.History),
		digest:  h,
		logger:  cfg.Logger,
	}, nil
}

func replayHistory(n, e, c *big.Int, history []QueryRecord) (*big.Int, *big.Int, int, *big.Int, int, error) {
	tmp := &Session{
		n:    n,
		e:    e,
		c:    c,
		lo:   new(big.Int),
		hi:   new(big.Int).Set(n),
		step: 0,
	}
	tmp.pow2e = new(big.Int).Exp(big.NewInt(2), e, n)
	tmp.pending = new(big.Int).Mul(c, tmp.pow2e)
	tmp.pending.Mod(tmp.pending, n)

	usable := 0
	for i, rec := range history {
		if err := validateRecord(rec, i+1); err != nil {
			return nil, nil, 0, nil, 0, err
		}
		if rec.Step != tmp.step {
			return nil, nil, 0, nil, 0, fmt.Errorf("%w: attempt %d has inconsistent step", ErrCheckpointInvalid, rec.Attempt)
		}
		expected := tmp.pending.Text(16)
		if rec.Query != expected {
			return nil, nil, 0, nil, 0, fmt.Errorf("%w: attempt %d query does not match deterministic sequence", ErrCheckpointInvalid, rec.Attempt)
		}
		result, err := parseResult(rec.Result)
		if err != nil {
			return nil, nil, 0, nil, 0, fmt.Errorf("%w: attempt %d: %v", ErrCheckpointInvalid, rec.Attempt, err)
		}
		switch result {
		case ResultEven:
			tmp.applyBit(false)
			tmp.advanceQuery()
			usable++
		case ResultOdd:
			tmp.applyBit(true)
			tmp.advanceQuery()
			usable++
		case ResultUnavailable, ResultRejected:
		}
	}
	return tmp.lo, tmp.hi, tmp.step, tmp.pending, usable, nil
}

func validateRecord(rec QueryRecord, wantAttempt int) error {
	if rec.Attempt != wantAttempt {
		return fmt.Errorf("%w: attempt %d is out of sequence (want %d)", ErrCheckpointInvalid, rec.Attempt, wantAttempt)
	}
	if rec.Step < 0 {
		return fmt.Errorf("%w: negative step", ErrCheckpointInvalid)
	}
	if _, err := parseCanonicalHex("query", rec.Query); err != nil {
		return err
	}
	if _, err := parseResult(rec.Result); err != nil {
		return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
	}
	return nil
}

func parseResult(s string) (OracleResult, error) {
	switch s {
	case ResultUnavailable.String():
		return ResultUnavailable, nil
	case ResultEven.String():
		return ResultEven, nil
	case ResultOdd.String():
		return ResultOdd, nil
	case ResultRejected.String():
		return ResultRejected, nil
	default:
		return ResultUnavailable, fmt.Errorf("unknown result %q", s)
	}
}

func parseCanonicalHex(name, s string) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("%w: empty %s", ErrCheckpointInvalid, name)
	}
	x, ok := new(big.Int).SetString(s, 16)
	if !ok || x.Sign() < 0 || x.Text(16) != s {
		return nil, fmt.Errorf("%w: non-canonical %s", ErrCheckpointInvalid, name)
	}
	return x, nil
}

func (s *Session) digestHex() string {
	return hex.EncodeToString(s.digest.Sum(nil))
}

func newTranscriptDigest(n, e, c *big.Int) hash.Hash {
	h := sha256.New()
	writeString(h, digestDomain)
	writeBig(h, n)
	writeBig(h, e)
	writeBig(h, c)
	return h
}

func writeQueryRecord(h hash.Hash, rec QueryRecord) {
	writeUint64(h, uint64(rec.Attempt))
	writeUint64(h, uint64(rec.Step))
	writeString(h, rec.Query)
	writeString(h, rec.Result)
}

func writeBig(h hash.Hash, x *big.Int) {
	writeBytes(h, x.Bytes())
}

func writeString(h hash.Hash, s string) {
	writeBytes(h, []byte(s))
}

func writeBytes(h hash.Hash, b []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(b)
}

func writeUint64(h hash.Hash, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	_, _ = h.Write(buf[:])
}
