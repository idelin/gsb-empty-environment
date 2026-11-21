package recovery

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	recordMagic   uint32 = 0x52584c31 // "RXL1"
	recordVersion byte   = 1
)

type recordKind uint8

const (
	recordHeader recordKind = iota + 1
	recordBlock
	recordPrepare
	recordDecision
	recordReceipt
)

func (k recordKind) String() string {
	switch k {
	case recordHeader:
		return "header"
	case recordBlock:
		return "block"
	case recordPrepare:
		return "prepare"
	case recordDecision:
		return "decision"
	case recordReceipt:
		return "receipt"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

type record struct {
	Kind           recordKind
	Deleted        bool
	Tenant         TenantID
	LeaseEpoch     uint64
	LeaseID        LeaseID
	Generation     uint64
	BaseGeneration uint64
	Txn            TxID
	TxnNonce       [16]byte
	TxnSignature   [32]byte
	Index          uint32
	BlockCount     uint32
	Prev           Hash
	Root           Hash
	SnapshotHash   Hash
	DecisionHash   Hash
	DecisionSig    [32]byte
	ReceiptSig     [32]byte
	Block          Block
}

func (r record) encode() ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(320 + len(r.Block.Data))
	writeU32(&buf, recordMagic)
	buf.WriteByte(recordVersion)
	buf.WriteByte(byte(r.Kind))
	if r.Deleted {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	buf.WriteByte(0)
	buf.Write(r.Tenant[:])
	writeU64(&buf, r.LeaseEpoch)
	buf.Write(r.LeaseID[:])
	writeU64(&buf, r.Generation)
	writeU64(&buf, r.BaseGeneration)
	buf.Write(r.Txn[:])
	buf.Write(r.TxnNonce[:])
	buf.Write(r.TxnSignature[:])
	writeU32(&buf, r.Index)
	writeU32(&buf, r.BlockCount)
	buf.Write(r.Prev[:])
	buf.Write(r.Root[:])
	buf.Write(r.SnapshotHash[:])
	buf.Write(r.DecisionHash[:])
	buf.Write(r.DecisionSig[:])
	buf.Write(r.ReceiptSig[:])

	if r.Kind == recordBlock {
		if len(r.Block.Data) > int(^uint32(0)) {
			return nil, fmt.Errorf("%w: block too large", ErrBlockTooLarge)
		}
		buf.Write(r.Block.Tenant[:])
		buf.Write(r.Block.ID[:])
		writeU64(&buf, r.Block.Version)
		writeU32(&buf, uint32(len(r.Block.Data)))
		buf.Write(r.Block.Data)
	}
	return buf.Bytes(), nil
}

func decodeRecord(data []byte) (record, error) {
	var rec record
	r := bytes.NewReader(data)
	magic, err := readU32(r)
	if err != nil || magic != recordMagic {
		return rec, fmt.Errorf("recovery: bad record magic")
	}
	version, err := r.ReadByte()
	if err != nil || version != recordVersion {
		return rec, fmt.Errorf("recovery: unsupported record version")
	}
	kind, err := r.ReadByte()
	if err != nil {
		return rec, err
	}
	rec.Kind = recordKind(kind)
	if rec.Kind < recordHeader || rec.Kind > recordReceipt {
		return rec, fmt.Errorf("recovery: invalid record kind %d", kind)
	}
	flags, err := r.ReadByte()
	if err != nil {
		return rec, err
	}
	if flags&^byte(1) != 0 {
		return rec, fmt.Errorf("recovery: invalid record flags")
	}
	rec.Deleted = flags&1 != 0
	if _, err := r.ReadByte(); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.Tenant[:]); err != nil {
		return rec, err
	}
	if rec.LeaseEpoch, err = readU64(r); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.LeaseID[:]); err != nil {
		return rec, err
	}
	if rec.Generation, err = readU64(r); err != nil {
		return rec, err
	}
	if rec.BaseGeneration, err = readU64(r); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.Txn[:]); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.TxnNonce[:]); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.TxnSignature[:]); err != nil {
		return rec, err
	}
	if rec.Index, err = readU32(r); err != nil {
		return rec, err
	}
	if rec.BlockCount, err = readU32(r); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.Prev[:]); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.Root[:]); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.SnapshotHash[:]); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.DecisionHash[:]); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.DecisionSig[:]); err != nil {
		return rec, err
	}
	if err := readFull(r, rec.ReceiptSig[:]); err != nil {
		return rec, err
	}

	if rec.Kind == recordBlock {
		if err := readFull(r, rec.Block.Tenant[:]); err != nil {
			return rec, err
		}
		if err := readFull(r, rec.Block.ID[:]); err != nil {
			return rec, err
		}
		if rec.Block.Version, err = readU64(r); err != nil {
			return rec, err
		}
		n, err := readU32(r)
		if err != nil {
			return rec, err
		}
		if uint64(n) > uint64(r.Len()) {
			return rec, fmt.Errorf("recovery: block data length exceeds record")
		}
		rec.Block.Data = make([]byte, n)
		if err := readFull(r, rec.Block.Data); err != nil {
			return rec, err
		}
	}
	if r.Len() != 0 {
		return rec, fmt.Errorf("recovery: trailing record bytes")
	}
	return rec, nil
}

func decisionCanonical(rec record) []byte {
	var buf bytes.Buffer
	buf.WriteString("recovery/decision/v1\x00")
	buf.Write(rec.Tenant[:])
	writeU64(&buf, rec.LeaseEpoch)
	buf.Write(rec.LeaseID[:])
	writeU64(&buf, rec.Generation)
	writeU64(&buf, rec.BaseGeneration)
	buf.Write(rec.Txn[:])
	buf.Write(rec.TxnNonce[:])
	buf.Write(rec.TxnSignature[:])
	writeU32(&buf, rec.BlockCount)
	if rec.Deleted {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	buf.Write(rec.Prev[:])
	buf.Write(rec.Root[:])
	buf.Write(rec.SnapshotHash[:])
	return buf.Bytes()
}

func receiptCanonical(rec record) []byte {
	var buf bytes.Buffer
	buf.WriteString("recovery/receipt/v1\x00")
	buf.Write(rec.Tenant[:])
	writeU64(&buf, rec.LeaseEpoch)
	buf.Write(rec.LeaseID[:])
	writeU64(&buf, rec.Generation)
	writeU64(&buf, rec.BaseGeneration)
	buf.Write(rec.Txn[:])
	buf.Write(rec.TxnNonce[:])
	buf.Write(rec.TxnSignature[:])
	writeU32(&buf, rec.BlockCount)
	if rec.Deleted {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	buf.Write(rec.Prev[:])
	buf.Write(rec.Root[:])
	buf.Write(rec.SnapshotHash[:])
	buf.Write(rec.DecisionHash[:])
	return buf.Bytes()
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeU64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func readU32(r *bytes.Reader) (uint32, error) {
	var b [4]byte
	if err := readFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

func readU64(r *bytes.Reader) (uint64, error) {
	var b [8]byte
	if err := readFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b[:]), nil
}

func readFull(r *bytes.Reader, dst []byte) error {
	n, err := r.Read(dst)
	if err != nil {
		return err
	}
	if n != len(dst) {
		return fmt.Errorf("recovery: short record")
	}
	return nil
}
