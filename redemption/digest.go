package redemption

import (
	"crypto/sha256"
	"encoding/hex"
)

// digestVersion is bumped whenever the canonical encoding changes.
const digestVersion = "redemption-cert-v1"

// CanonicalDigest hashes every semantic field of the problem in a
// deterministic, JSON-import-independent encoding. The optimality certificate
// binds this digest so it cannot be replayed against a different instance.
//
// The encoding is length-prefixed (big-endian uint32 lengths) and therefore
// unambiguous for nested strings and byte slices.
func CanonicalDigest(p Problem) string {
	h := sha256.New()
	writeStr := func(s string) {
		var b [4]byte
		b[0] = byte(len(s) >> 24)
		b[1] = byte(len(s) >> 16)
		b[2] = byte(len(s) >> 8)
		b[3] = byte(len(s))
		h.Write(b[:])
		h.Write([]byte(s))
	}
	writeBytes := func(b []byte) {
		var l [4]byte
		l[0] = byte(len(b) >> 24)
		l[1] = byte(len(b) >> 16)
		l[2] = byte(len(b) >> 8)
		l[3] = byte(len(b))
		h.Write(l[:])
		h.Write(b)
	}
	writeInt := func(v int64) {
		var b [8]byte
		for k := 0; k < 8; k++ {
			b[7-k] = byte(v >> (8 * k))
		}
		h.Write(b[:])
	}
	writeStr(digestVersion)

	writeStr("issuers")
	writeInt(int64(len(p.Issuers)))
	for _, id := range p.Issuers {
		writeStr(string(id))
	}

	writeStr("quotas")
	writeInt(int64(len(p.Quotas)))
	for _, q := range p.Quotas {
		writeStr(string(q.Issuer))
		writeStr(string(q.Activity))
		writeInt(q.Limit)
	}

	var writeTx func(tx Transaction)
	writeTx = func(tx Transaction) {
		writeStr(string(tx.ID))
		writeInt(int64(len(tx.Inputs)))
		for _, in := range tx.Inputs {
			if in.Source.Origin != nil {
				writeStr("origin")
				o := in.Source.Origin
				writeStr(string(o.Issuer))
				writeStr(string(o.Activity))
				writeStr(string(o.Commitment))
				writeInt(o.Denomination)
				writeStr(string(o.Nullifier))
				writeBytes(o.Signature)
			} else {
				writeStr("outpoint")
				op := in.Source.Outpoint
				writeStr(string(op.TxID))
				writeInt(int64(op.OutIndex))
			}
			writeStr(string(in.Issuer))
			writeStr(string(in.Activity))
			writeStr(string(in.Commitment))
			writeInt(in.Amount)
			writeStr(string(in.Nullifier))
		}
		writeInt(int64(len(tx.Outputs)))
		for _, out := range tx.Outputs {
			writeStr(string(out.Issuer))
			writeStr(string(out.Activity))
			writeStr(string(out.Commitment))
			writeInt(out.Amount)
		}
		writeInt(int64(len(tx.Spends)))
		for _, s := range tx.Spends {
			writeStr(string(s.Service))
			writeStr(string(s.Issuer))
			writeStr(string(s.Activity))
			writeInt(s.Units)
			writeInt(s.Cost)
		}
		writeBytes(tx.Proof)
	}

	writeStr("confirmed")
	writeInt(int64(len(p.Confirmed)))
	for _, tx := range p.Confirmed {
		writeTx(tx)
	}
	writeStr("candidates")
	writeInt(int64(len(p.Candidates)))
	for _, tx := range p.Candidates {
		writeTx(tx)
	}
	return hex.EncodeToString(h.Sum(nil))
}
