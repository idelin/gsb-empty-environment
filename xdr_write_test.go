package netsec

import "encoding/base64"

// Minimal XDR writer used only by tests to build well-formed envelopes.

type xdrWriter struct{ b []byte }

func (w *xdrWriter) u32(v uint32) {
	w.b = append(w.b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *xdrWriter) i32(v int32) { w.u32(uint32(v)) }
func (w *xdrWriter) u64(v uint64) {
	w.b = append(w.b,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *xdrWriter) i64(v int64) { w.u64(uint64(v)) }

func (w *xdrWriter) boolean(present bool) {
	if present {
		w.u32(1)
	} else {
		w.u32(0)
	}
}

func (w *xdrWriter) fixed(raw []byte) { w.b = append(w.b, raw...) }

func (w *xdrWriter) varOpaque(raw []byte) {
	w.u32(uint32(len(raw)))
	w.b = append(w.b, raw...)
	pad := (4 - len(raw)%4) % 4
	for i := 0; i < pad; i++ {
		w.b = append(w.b, 0)
	}
}

func (w *xdrWriter) string(s string) { w.varOpaque([]byte(s)) }

func (w *xdrWriter) bytes() []byte { return w.b }

func (w *xdrWriter) base64() string { return base64.StdEncoding.EncodeToString(w.b) }

// muxed writes a non-muxed MuxedAccount union. The
// KEY_TYPE_ED25519(0) arm is itself an AccountID union
// (PUBLIC_KEY_TYPE_ED25519(0) + 32-byte key), so two discriminants appear.
func (w *xdrWriter) muxed(ed25519Key []byte) {
	w.u32(0) // MuxedAccount discriminant: KEY_TYPE_ED25519
	w.u32(0) // AccountID discriminant: PUBLIC_KEY_TYPE_ED25519
	w.fixed(ed25519Key)
}

// writeV1Transaction builds a canonical Transaction v1:
// source (ed25519) || fee || seqNum || PRECOND_NONE || MEMO_NONE ||
// one PAYMENT op || ext=0.
func (w *xdrWriter) v1Transaction(source []byte, fee uint32, seq uint64, destination []byte, amount int64) {
	w.muxed(source)
	w.u32(fee)
	w.u64(seq)
	w.u32(0) // PRECOND_NONE
	w.u32(0) // MEMO_NONE

	// operations<max=100>
	w.u32(1)
	w.boolean(false)     // no per-op source account
	w.u32(1)             // PAYMENT
	w.muxed(destination) // destination MuxedAccount union
	w.u32(0)             // ASSET_TYPE_NATIVE
	w.i64(amount)

	w.u32(0) // ext: void
}

// v1Envelope wraps a v1 transaction and its decorated signatures.
func (w *xdrWriter) v1Envelope(tx []byte, sigs ...DecoratedSignature) {
	w.u32(EnvelopeTypeTx)
	w.fixed(tx)
	w.decoratedSigs(sigs)
}

// feeBumpEnvelope wraps a complete inner v1 envelope.
func (w *xdrWriter) feeBumpEnvelope(feeSource []byte, fee int64, innerEnvelope []byte, outerSigs []DecoratedSignature) {
	w.u32(EnvelopeTypeTxFeeBump)
	w.muxed(feeSource)
	w.i64(fee)
	w.fixed(innerEnvelope)
	w.u32(0) // ext: void
	w.decoratedSigs(outerSigs)
}

func (w *xdrWriter) decoratedSigs(sigs []DecoratedSignature) {
	w.u32(uint32(len(sigs)))
	for _, s := range sigs {
		w.fixed(s.Hint[:])
		w.varOpaque(s.Signature)
	}
}

// v0Transaction builds a legacy TransactionV0 (bare 32-byte source,
// optional TimeBounds, no Preconditions union).
func (w *xdrWriter) v0Transaction(source []byte, fee uint32, seq uint64, dest []byte) {
	w.fixed(source)
	w.u32(fee)
	w.u64(seq)
	w.boolean(false) // no TimeBounds
	w.u32(0)         // MEMO_NONE
	w.u32(1)         // one operation
	w.boolean(false) // no per-op source
	w.u32(1)         // PAYMENT
	w.muxed(dest)
	w.u32(0) // native
	w.i64(50)
	w.u32(0) // ext
}

func (w *xdrWriter) v0Envelope(tx []byte, sigs ...DecoratedSignature) {
	w.u32(EnvelopeTypeTxV0)
	w.fixed(tx)
	w.decoratedSigs(sigs)
}

func signDecorated(passphrase string, envelopeType uint32, txXDR []byte, priv ed25519PrivateKey) DecoratedSignature {
	digest := PayloadDigest(passphrase, envelopeType, txXDR)
	sig := ed25519Sign(priv, digest[:])
	pub := ed25519PublicKeyOf(priv)
	var hint [4]byte
	copy(hint[:], pub[len(pub)-4:])
	return DecoratedSignature{Hint: hint, Signature: sig}
}

// v1TransactionMulti builds a v1 transaction exercising several union arms:
// PRECOND_V2, MEMO_ID, a native payment, and a MANAGE_DATA operation.
func (w *xdrWriter) v1TransactionMulti(source []byte, seq uint64, dest []byte) {
	w.u32(0) // MuxedAccount disc
	w.u32(0) // AccountID disc
	w.fixed(source)
	w.u32(100)
	w.u64(seq)

	// PRECOND_V2 with time bounds, ledger bounds, min seq age, extra signers.
	w.u32(2)
	w.boolean(true)
	w.u64(10) // TimeBounds minTime
	w.u64(20) // maxTime
	w.boolean(true)
	w.u32(5) // LedgerBounds minLedger
	w.u32(9) // maxLedger
	w.boolean(true)
	w.u32(7) // minSeqAge
	w.u32(0) // no extra signers

	w.u32(2) // MEMO_ID
	w.u64(42)

	w.u32(2) // two operations

	// Operation 1: payment.
	w.boolean(false)
	w.u32(1)
	w.muxed(dest) // destination MuxedAccount (two nested discriminants + key)
	w.u32(0)      // native asset
	w.i64(50)

	// Operation 2: manage data with a value.
	w.boolean(false)
	w.u32(10) // MANAGE_DATA
	w.string("key")
	w.boolean(true)
	w.varOpaque([]byte("value"))

	w.u32(0) // ext
}
