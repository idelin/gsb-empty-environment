package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	psbtGlobalUnsignedTx = 0x00
	psbtGlobalVersion    = 0xfb

	psbtInNonWitnessUTXO = 0x00
	psbtInWitnessUTXO    = 0x01
	psbtInSighashType    = 0x03
	psbtInRedeemScript   = 0x04
	psbtInWitnessScript  = 0x05
	psbtInBIP32Deriv     = 0x06

	psbtOutRedeemScript  = 0x00
	psbtOutWitnessScript = 0x01
	psbtOutBIP32Deriv    = 0x02
)

var (
	ErrMalformedPSBT   = errors.New("netsec: malformed psbt")
	ErrUnsupportedPSBT = errors.New("netsec: unsupported psbt version")
	psbtMagic          = []byte{0x70, 0x73, 0x62, 0x74, 0xff}
)

// OutPoint identifies a previous transaction output. TxID is in the byte
// order used by block explorers and RPC (the reverse of the serialized
// little-endian hash).
type OutPoint struct {
	TxID  [32]byte
	Index uint32
}

// TxIn is a transaction input. Witness is only populated for parsed
// transactions that carry witness data.
type TxIn struct {
	PreviousOutPoint OutPoint
	ScriptSig        []byte
	Sequence         uint32
	Witness          [][]byte
}

// TxOut is a transaction output.
type TxOut struct {
	ValueSat     int64
	ScriptPubKey []byte
}

// Tx is a parsed Bitcoin transaction.
type Tx struct {
	Version  uint32
	Inputs   []TxIn
	Outputs  []TxOut
	LockTime uint32

	hasWitness bool
}

// BIP32Derivation is a PSBT BIP32 derivation field. Fingerprint is stored
// in the same byte order as the PSBT field (little-endian uint32 bytes).
type BIP32Derivation struct {
	PubKey      []byte
	Fingerprint [4]byte
	Path        []uint32
}

// Input is the policy-relevant subset of a PSBT input map.
type Input struct {
	NonWitnessUTXO   []byte
	WitnessUTXO      *TxOut
	SighashType      uint32
	HasSighashType   bool
	RedeemScript     []byte
	WitnessScript    []byte
	BIP32Derivations []BIP32Derivation
}

// Output is the policy-relevant subset of a PSBT output map.
type Output struct {
	RedeemScript     []byte
	WitnessScript    []byte
	BIP32Derivations []BIP32Derivation
}

// PSBT is a parsed BIP174 v0 PSBT. The unsigned transaction is authoritative
// for the outpoints and output scripts; input/output maps supply evidence.
type PSBT struct {
	Version    uint32
	UnsignedTx *Tx
	Inputs     []Input
	Outputs    []Output
	Raw        []byte
}

// ParsePSBT parses a BIP174 v0 PSBT.
func ParsePSBT(raw []byte) (*PSBT, error) {
	if len(raw) < len(psbtMagic) || !bytes.Equal(raw[:len(psbtMagic)], psbtMagic) {
		return nil, fmt.Errorf("%w: missing psbt magic", ErrMalformedPSBT)
	}

	r := &byteReader{data: raw, pos: len(psbtMagic)}
	var unsignedRaw []byte
	var version uint32
	versionSeen := false

	err := parseMap(r, func(keyType byte, keyData, value []byte) error {
		switch keyType {
		case psbtGlobalUnsignedTx:
			if len(keyData) != 0 {
				return fmt.Errorf("%w: unsigned transaction key has key data", ErrMalformedPSBT)
			}
			if unsignedRaw != nil {
				return fmt.Errorf("%w: duplicate unsigned transaction", ErrMalformedPSBT)
			}
			unsignedRaw = cloneBytes(value)
		case psbtGlobalVersion:
			if len(keyData) != 0 || len(value) != 4 {
				return fmt.Errorf("%w: invalid global version field", ErrMalformedPSBT)
			}
			if versionSeen {
				return fmt.Errorf("%w: duplicate global version", ErrMalformedPSBT)
			}
			versionSeen = true
			version = binary.LittleEndian.Uint32(value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if versionSeen && version != 0 {
		return nil, fmt.Errorf("%w: psbt version %d", ErrUnsupportedPSBT, version)
	}
	if unsignedRaw == nil {
		return nil, fmt.Errorf("%w: missing unsigned transaction", ErrMalformedPSBT)
	}

	unsignedTx, err := parseTransaction(unsignedRaw, false)
	if err != nil {
		return nil, fmt.Errorf("%w: unsigned transaction: %v", ErrMalformedPSBT, err)
	}

	psbt := &PSBT{
		Version:    version,
		UnsignedTx: unsignedTx,
		Inputs:     make([]Input, len(unsignedTx.Inputs)),
		Outputs:    make([]Output, len(unsignedTx.Outputs)),
		Raw:        cloneBytes(raw),
	}

	for i := range psbt.Inputs {
		idx := i
		err := parseMap(r, func(keyType byte, keyData, value []byte) error {
			return parseInputField(&psbt.Inputs[idx], keyType, keyData, value)
		})
		if err != nil {
			return nil, err
		}
	}

	for i := range psbt.Outputs {
		idx := i
		err := parseMap(r, func(keyType byte, keyData, value []byte) error {
			return parseOutputField(&psbt.Outputs[idx], keyType, keyData, value)
		})
		if err != nil {
			return nil, err
		}
	}

	if r.pos != len(raw) {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrMalformedPSBT, len(raw)-r.pos)
	}
	return psbt, nil
}

// Parse is an alias for ParsePSBT.
func Parse(raw []byte) (*PSBT, error) {
	return ParsePSBT(raw)
}

func parseInputField(in *Input, keyType byte, keyData, value []byte) error {
	switch keyType {
	case psbtInNonWitnessUTXO:
		if len(keyData) != 0 {
			return fmt.Errorf("%w: non-witness utxo key has key data", ErrMalformedPSBT)
		}
		in.NonWitnessUTXO = cloneBytes(value)
	case psbtInWitnessUTXO:
		if len(keyData) != 0 {
			return fmt.Errorf("%w: witness utxo key has key data", ErrMalformedPSBT)
		}
		out, err := parseTxOut(value)
		if err != nil {
			return fmt.Errorf("%w: witness utxo: %v", ErrMalformedPSBT, err)
		}
		in.WitnessUTXO = &out
	case psbtInSighashType:
		if len(keyData) != 0 || len(value) != 4 {
			return fmt.Errorf("%w: invalid sighash type field", ErrMalformedPSBT)
		}
		in.SighashType = binary.LittleEndian.Uint32(value)
		in.HasSighashType = true
	case psbtInRedeemScript:
		if len(keyData) != 0 {
			return fmt.Errorf("%w: redeem script key has key data", ErrMalformedPSBT)
		}
		in.RedeemScript = cloneBytes(value)
	case psbtInWitnessScript:
		if len(keyData) != 0 {
			return fmt.Errorf("%w: witness script key has key data", ErrMalformedPSBT)
		}
		in.WitnessScript = cloneBytes(value)
	case psbtInBIP32Deriv:
		deriv, err := parseBIP32Derivation(keyData, value)
		if err != nil {
			return err
		}
		in.BIP32Derivations = append(in.BIP32Derivations, deriv)
	}
	return nil
}

func parseOutputField(out *Output, keyType byte, keyData, value []byte) error {
	switch keyType {
	case psbtOutRedeemScript:
		if len(keyData) != 0 {
			return fmt.Errorf("%w: redeem script key has key data", ErrMalformedPSBT)
		}
		out.RedeemScript = cloneBytes(value)
	case psbtOutWitnessScript:
		if len(keyData) != 0 {
			return fmt.Errorf("%w: witness script key has key data", ErrMalformedPSBT)
		}
		out.WitnessScript = cloneBytes(value)
	case psbtOutBIP32Deriv:
		deriv, err := parseBIP32Derivation(keyData, value)
		if err != nil {
			return err
		}
		out.BIP32Derivations = append(out.BIP32Derivations, deriv)
	}
	return nil
}

func parseBIP32Derivation(keyData, value []byte) (BIP32Derivation, error) {
	if len(keyData) != 32 && len(keyData) != 33 && len(keyData) != 65 {
		return BIP32Derivation{}, fmt.Errorf("%w: invalid bip32 pubkey length %d", ErrMalformedPSBT, len(keyData))
	}
	if len(value) < 4 || (len(value)-4)%4 != 0 {
		return BIP32Derivation{}, fmt.Errorf("%w: invalid bip32 derivation value", ErrMalformedPSBT)
	}

	d := BIP32Derivation{
		PubKey: cloneBytes(keyData),
		Path:   make([]uint32, (len(value)-4)/4),
	}
	copy(d.Fingerprint[:], value[:4])
	for i := range d.Path {
		off := 4 + i*4
		d.Path[i] = binary.LittleEndian.Uint32(value[off : off+4])
	}
	return d, nil
}

func parseTxOut(raw []byte) (TxOut, error) {
	r := &byteReader{data: raw}
	out, err := readTxOut(r)
	if err != nil {
		return TxOut{}, err
	}
	if r.pos != len(raw) {
		return TxOut{}, fmt.Errorf("trailing bytes in txout")
	}
	return out, nil
}

func parseTransaction(raw []byte, allowWitness bool) (*Tx, error) {
	r := &byteReader{data: raw}
	version, err := r.uint32()
	if err != nil {
		return nil, err
	}

	tx := &Tx{Version: version}
	vinCount, err := r.compact()
	if err != nil {
		return nil, err
	}
	hadWitness := false
	if vinCount == 0 {
		flag, err := r.byte()
		if err != nil {
			return nil, err
		}
		if flag == 0 {
			return nil, fmt.Errorf("invalid zero witness flag")
		}
		if !allowWitness {
			return nil, fmt.Errorf("witness data is not allowed")
		}
		hadWitness = true
		vinCount, err = r.compact()
		if err != nil {
			return nil, err
		}
	}
	if vinCount > uint64(len(raw)) {
		return nil, fmt.Errorf("unreasonable input count %d", vinCount)
	}

	tx.Inputs = make([]TxIn, int(vinCount))
	for i := range tx.Inputs {
		var wireHash [32]byte
		b, err := r.bytes(32)
		if err != nil {
			return nil, err
		}
		copy(wireHash[:], b)
		for j := 0; j < 32; j++ {
			tx.Inputs[i].PreviousOutPoint.TxID[j] = wireHash[31-j]
		}
		tx.Inputs[i].PreviousOutPoint.Index, err = r.uint32()
		if err != nil {
			return nil, err
		}
		tx.Inputs[i].ScriptSig, err = r.varBytes()
		if err != nil {
			return nil, err
		}
		tx.Inputs[i].Sequence, err = r.uint32()
		if err != nil {
			return nil, err
		}
	}

	voutCount, err := r.compact()
	if err != nil {
		return nil, err
	}
	if voutCount > uint64(len(raw)) {
		return nil, fmt.Errorf("unreasonable output count %d", voutCount)
	}
	tx.Outputs = make([]TxOut, int(voutCount))
	for i := range tx.Outputs {
		tx.Outputs[i], err = readTxOut(r)
		if err != nil {
			return nil, err
		}
	}

	if hadWitness {
		for i := range tx.Inputs {
			itemCount, err := r.compact()
			if err != nil {
				return nil, err
			}
			if itemCount > uint64(len(raw)) {
				return nil, fmt.Errorf("unreasonable witness item count %d", itemCount)
			}
			tx.Inputs[i].Witness = make([][]byte, int(itemCount))
			for j := range tx.Inputs[i].Witness {
				tx.Inputs[i].Witness[j], err = r.varBytes()
				if err != nil {
					return nil, err
				}
			}
		}
		tx.hasWitness = true
	}

	tx.LockTime, err = r.uint32()
	if err != nil {
		return nil, err
	}
	if r.pos != len(raw) {
		return nil, fmt.Errorf("trailing bytes in transaction")
	}
	return tx, nil
}

func readTxOut(r *byteReader) (TxOut, error) {
	value, err := r.uint64()
	if err != nil {
		return TxOut{}, err
	}
	if value > math.MaxInt64 {
		return TxOut{}, fmt.Errorf("txout value %d overflows int64", value)
	}
	script, err := r.varBytes()
	if err != nil {
		return TxOut{}, err
	}
	return TxOut{ValueSat: int64(value), ScriptPubKey: script}, nil
}

// TxID returns the display-order transaction id (double-SHA256 of the
// transaction serialization with witness data removed).
func (tx *Tx) TxID() [32]byte {
	digest := doubleSHA256(tx.serialize(false))
	var id [32]byte
	for i := 0; i < 32; i++ {
		id[i] = digest[31-i]
	}
	return id
}

func (tx *Tx) serialize(withWitness bool) []byte {
	var buf bytes.Buffer
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], tx.Version)
	buf.Write(b[:])

	writeWitness := withWitness && (tx.hasWitness || txHasWitnessData(tx))
	if writeWitness {
		buf.WriteByte(0)
		buf.WriteByte(1)
	}

	writeCompact(&buf, uint64(len(tx.Inputs)))
	for _, in := range tx.Inputs {
		for i := 31; i >= 0; i-- {
			buf.WriteByte(in.PreviousOutPoint.TxID[i])
		}
		binary.LittleEndian.PutUint32(b[:], in.PreviousOutPoint.Index)
		buf.Write(b[:])
		writeVarBytes(&buf, in.ScriptSig)
		binary.LittleEndian.PutUint32(b[:], in.Sequence)
		buf.Write(b[:])
	}

	writeCompact(&buf, uint64(len(tx.Outputs)))
	for _, out := range tx.Outputs {
		var value [8]byte
		binary.LittleEndian.PutUint64(value[:], uint64(out.ValueSat))
		buf.Write(value[:])
		writeVarBytes(&buf, out.ScriptPubKey)
	}

	if writeWitness {
		for _, in := range tx.Inputs {
			writeCompact(&buf, uint64(len(in.Witness)))
			for _, item := range in.Witness {
				writeVarBytes(&buf, item)
			}
		}
	}

	binary.LittleEndian.PutUint32(b[:], tx.LockTime)
	buf.Write(b[:])
	return buf.Bytes()
}

// Bytes returns the transaction serialization. Witness data is included only
// when withWitness is true and the parsed transaction originally had witness
// data.
func (tx *Tx) Bytes(withWitness bool) []byte {
	return tx.serialize(withWitness)
}

func doubleSHA256(data []byte) [32]byte {
	first := sha256.Sum256(data)
	return sha256.Sum256(first[:])
}

func txHasWitnessData(tx *Tx) bool {
	for _, in := range tx.Inputs {
		if len(in.Witness) != 0 {
			return true
		}
	}
	return false
}

type byteReader struct {
	data []byte
	pos  int
}

func (r *byteReader) remaining() int {
	return len(r.data) - r.pos
}

func (r *byteReader) byte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, fmt.Errorf("%w: unexpected end of data", ErrMalformedPSBT)
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *byteReader) bytes(n int) ([]byte, error) {
	if n < 0 || n > r.remaining() {
		return nil, fmt.Errorf("%w: unexpected end of data", ErrMalformedPSBT)
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

func (r *byteReader) uint32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (r *byteReader) uint64() (uint64, error) {
	b, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

func (r *byteReader) compact() (uint64, error) {
	b, err := r.byte()
	if err != nil {
		return 0, err
	}
	switch b {
	case 0xff:
		v, err := r.uint64()
		return v, err
	case 0xfe:
		raw, err := r.bytes(4)
		if err != nil {
			return 0, err
		}
		return uint64(binary.LittleEndian.Uint32(raw)), nil
	case 0xfd:
		raw, err := r.bytes(2)
		if err != nil {
			return 0, err
		}
		return uint64(binary.LittleEndian.Uint16(raw)), nil
	default:
		return uint64(b), nil
	}
}

func (r *byteReader) varBytes() ([]byte, error) {
	n, err := r.compact()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.remaining()) {
		return nil, fmt.Errorf("%w: variable byte length %d exceeds remaining data", ErrMalformedPSBT, n)
	}
	b, err := r.bytes(int(n))
	if err != nil {
		return nil, err
	}
	return cloneBytes(b), nil
}

func parseMap(r *byteReader, visit func(keyType byte, keyData, value []byte) error) error {
	seen := make(map[string]struct{})
	for {
		keyLen, err := r.compact()
		if err != nil {
			return err
		}
		if keyLen == 0 {
			return nil
		}
		if keyLen > uint64(r.remaining()) {
			return fmt.Errorf("%w: map key length exceeds remaining data", ErrMalformedPSBT)
		}
		key, err := r.bytes(int(keyLen))
		if err != nil {
			return err
		}
		valueLen, err := r.compact()
		if err != nil {
			return err
		}
		if valueLen > uint64(r.remaining()) {
			return fmt.Errorf("%w: map value length exceeds remaining data", ErrMalformedPSBT)
		}
		value, err := r.bytes(int(valueLen))
		if err != nil {
			return err
		}
		if len(key) == 0 {
			return fmt.Errorf("%w: empty map key", ErrMalformedPSBT)
		}
		keyString := string(key)
		if _, ok := seen[keyString]; ok {
			return fmt.Errorf("%w: duplicate map key type 0x%02x", ErrMalformedPSBT, key[0])
		}
		seen[keyString] = struct{}{}
		if err := visit(key[0], key[1:], value); err != nil {
			return err
		}
	}
}

func writeCompact(buf *bytes.Buffer, n uint64) {
	switch {
	case n < 0xfd:
		buf.WriteByte(byte(n))
	case n <= 0xffff:
		buf.WriteByte(0xfd)
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], uint16(n))
		buf.Write(b[:])
	case n <= 0xffffffff:
		buf.WriteByte(0xfe)
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(n))
		buf.Write(b[:])
	default:
		buf.WriteByte(0xff)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], n)
		buf.Write(b[:])
	}
}

func writeVarBytes(buf *bytes.Buffer, data []byte) {
	writeCompact(buf, uint64(len(data)))
	buf.Write(data)
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}
