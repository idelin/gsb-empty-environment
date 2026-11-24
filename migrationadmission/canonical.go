package migrationadmission

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// Wire format constants. The serialization is a length-prefixed canonical TLV
// encoding: every variable-length field is prefixed by its byte length and all
// maps/sets are emitted in a fixed sorted order.
const (
	wireMagic   = "MADM"
	wireVersion = uint64(1)
)

// Signature domains separate the three signed message classes.
const (
	domainAlias      = "migrationadmission/alias/v1"
	domainLateness   = "migrationadmission/lateness/v1"
	domainCredential = "migrationadmission/credential/v1"
)

type encoder struct {
	buf bytes.Buffer
}

func (e *encoder) bytes(v []byte) {
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(v)))
	e.buf.Write(lenBuf[:n])
	e.buf.Write(v)
}

func (e *encoder) str(v string) {
	e.bytes([]byte(v))
}

func (e *encoder) uvarint(v uint64) {
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], v)
	e.buf.Write(lenBuf[:n])
}

func (e *encoder) rawByte(v byte) {
	e.buf.WriteByte(v)
}

func (e *encoder) symbols(v []Symbol) {
	e.uvarint(uint64(len(v)))
	for _, symbol := range v {
		e.uvarint(uint64(symbol))
	}
}

type decoder struct {
	buf []byte
	pos int
}

func (d *decoder) uvarint() (uint64, error) {
	v, n := binary.Uvarint(d.buf[d.pos:])
	if n <= 0 {
		return 0, ErrShortEncoding
	}
	d.pos += n
	return v, nil
}

func (d *decoder) rawByte() (byte, error) {
	if d.pos >= len(d.buf) {
		return 0, ErrShortEncoding
	}
	v := d.buf[d.pos]
	d.pos++
	return v, nil
}

func (d *decoder) bytesField() ([]byte, error) {
	n, err := d.uvarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(d.buf)-d.pos) {
		return nil, ErrShortEncoding
	}
	v := append([]byte(nil), d.buf[d.pos:d.pos+int(n)]...)
	d.pos += int(n)
	return v, nil
}

func (d *decoder) strField() (string, error) {
	v, err := d.bytesField()
	return string(v), err
}

func (d *decoder) symbolsField() ([]Symbol, error) {
	n, err := d.uvarint()
	if err != nil {
		return nil, err
	}
	out := make([]Symbol, n)
	for i := range out {
		v, err := d.uvarint()
		if err != nil {
			return nil, err
		}
		out[i] = Symbol(v)
	}
	return out, nil
}

func encodeGrammar(g Grammar) []byte {
	e := &encoder{}
	e.str(g.Name)
	e.uvarint(uint64(g.Start))
	accepts := append([]int(nil), g.Accepts...)
	sort.Ints(accepts)
	e.uvarint(uint64(len(accepts)))
	for _, state := range accepts {
		e.uvarint(uint64(state))
	}
	rules := append([]Rule(nil), g.Transitions...)
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].From != rules[j].From {
			return rules[i].From < rules[j].From
		}
		return rules[i].On < rules[j].On
	})
	e.uvarint(uint64(len(rules)))
	for _, rule := range rules {
		e.uvarint(uint64(rule.From))
		e.rawByte(rule.On)
		e.uvarint(uint64(rule.To))
	}
	return e.buf.Bytes()
}

func decodeGrammar(d *decoder) (Grammar, error) {
	var g Grammar
	var err error
	if g.Name, err = d.strField(); err != nil {
		return Grammar{}, err
	}
	start, err := d.uvarint()
	if err != nil {
		return Grammar{}, err
	}
	g.Start = int(start)
	acceptCount, err := d.uvarint()
	if err != nil {
		return Grammar{}, err
	}
	g.Accepts = make([]int, acceptCount)
	for i := range g.Accepts {
		v, err := d.uvarint()
		if err != nil {
			return Grammar{}, err
		}
		g.Accepts[i] = int(v)
	}
	ruleCount, err := d.uvarint()
	if err != nil {
		return Grammar{}, err
	}
	g.Transitions = make([]Rule, ruleCount)
	for i := range g.Transitions {
		from, err := d.uvarint()
		if err != nil {
			return Grammar{}, err
		}
		on, err := d.rawByte()
		if err != nil {
			return Grammar{}, err
		}
		to, err := d.uvarint()
		if err != nil {
			return Grammar{}, err
		}
		g.Transitions[i] = Rule{From: int(from), On: on, To: int(to)}
	}
	return g, nil
}

func encodeTable(t TranscriptionTable) []byte {
	e := &encoder{}
	e.str(t.Name)
	e.symbols(t.Tail)
	rules := append([]TranscriptionRule(nil), t.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].On < rules[j].On })
	e.uvarint(uint64(len(rules)))
	for _, rule := range rules {
		e.rawByte(rule.On)
		e.symbols(rule.Emit)
	}
	return e.buf.Bytes()
}

func decodeTable(d *decoder) (TranscriptionTable, error) {
	var t TranscriptionTable
	var err error
	if t.Name, err = d.strField(); err != nil {
		return TranscriptionTable{}, err
	}
	if t.Tail, err = d.symbolsField(); err != nil {
		return TranscriptionTable{}, err
	}
	ruleCount, err := d.uvarint()
	if err != nil {
		return TranscriptionTable{}, err
	}
	t.Rules = make([]TranscriptionRule, ruleCount)
	for i := range t.Rules {
		on, err := d.rawByte()
		if err != nil {
			return TranscriptionTable{}, err
		}
		emit, err := d.symbolsField()
		if err != nil {
			return TranscriptionTable{}, err
		}
		t.Rules[i] = TranscriptionRule{On: on, Emit: emit}
	}
	return t, nil
}

func encodePolicy(p Policy) []byte {
	e := &encoder{}
	e.str(p.Version)
	e.bytes(encodeGrammar(p.Grammar))
	e.bytes(encodeTable(p.Transcription))
	return e.buf.Bytes()
}

func decodePolicy(d *decoder) (Policy, error) {
	version, err := d.strField()
	if err != nil {
		return Policy{}, err
	}
	grammarBytes, err := d.bytesField()
	if err != nil {
		return Policy{}, err
	}
	g, err := decodeGrammar(&decoder{buf: grammarBytes})
	if err != nil {
		return Policy{}, err
	}
	tableBytes, err := d.bytesField()
	if err != nil {
		return Policy{}, err
	}
	t, err := decodeTable(&decoder{buf: tableBytes})
	if err != nil {
		return Policy{}, err
	}
	return Policy{Version: version, Grammar: g, Transcription: t}, nil
}

// PolicyHash identifies one exact grammar/table version.
func PolicyHash(p Policy) [32]byte {
	return sha256.Sum256(encodePolicy(p))
}
