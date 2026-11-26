package crlstate

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

const canonicalVersion = 1

type canonicalWriter struct {
	buf       []byte
	includeR  bool
	rootValue RootID
}

func (w *canonicalWriter) u8(v byte) { w.buf = append(w.buf, v) }

func (w *canonicalWriter) u64(v uint64) {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	w.buf = append(w.buf, tmp[:]...)
}

func (w *canonicalWriter) i64(v int64) { w.u64(uint64(v)) }

func (w *canonicalWriter) boolean(v bool) {
	if v {
		w.u8(1)
	} else {
		w.u8(0)
	}
}

func (w *canonicalWriter) bytes(v []byte) {
	w.u64(uint64(len(v)))
	w.buf = append(w.buf, v...)
}

func (w *canonicalWriter) text(v string) { w.bytes([]byte(v)) }

func (w *canonicalWriter) serial(v Serial) {
	w.bytes(v.n.Bytes())
}

func (w *canonicalWriter) unix(t interface{ UnixNano() int64 }) {
	w.i64(t.UnixNano())
}

func (w *canonicalWriter) root(id RootID) {
	if w.includeR {
		w.text(string(id))
	} else {
		w.text("")
	}
}

func (w *canonicalWriter) delegationLinkRoot(id RootID) {
	// Cross-sign copies may legitimately have different trust roots while
	// carrying the same logical delegation and scope.
	w.root(id)
}

func canonicalManifest(m *Manifest, includeRoot bool) []byte {
	w := canonicalWriter{includeR: includeRoot}
	w.u8(canonicalVersion)
	w.text("crlstate-manifest")
	w.text(m.PartitionID)
	w.text(string(m.IssuerID))
	w.text(string(m.SignerID))
	w.root(m.RootID)
	w.u64(m.Epoch)
	w.serial(m.SerialLo)
	w.serial(m.SerialHi)
	w.u64(uint64(m.Reasons))
	w.unix(m.ValidFrom)
	w.unix(m.ValidUntil)
	w.u64(uint64(len(m.Indirect)))
	for _, d := range m.Indirect {
		w.text(string(d.Parent))
		w.text(string(d.Child))
		w.delegationLinkRoot(d.Root)
		w.serial(d.SerialLo)
		w.serial(d.SerialHi)
		w.u64(uint64(d.Reasons))
		w.unix(d.ValidFrom)
		w.unix(d.ValidUntil)
	}
	w.u64(m.CRLNumber)
	w.boolean(m.IsDelta)
	w.u64(m.BaseDelta)

	entries := append([]Entry(nil), m.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if c := entries[i].Serial.Cmp(entries[j].Serial); c != 0 {
			return c < 0
		}
		if !entries[i].RevokedAt.Equal(entries[j].RevokedAt) {
			return entries[i].RevokedAt.Before(entries[j].RevokedAt)
		}
		if entries[i].Reason != entries[j].Reason {
			return entries[i].Reason < entries[j].Reason
		}
		return entries[i].HoldID < entries[j].HoldID
	})
	w.u64(uint64(len(entries)))
	for _, e := range entries {
		w.serial(e.Serial)
		w.unix(e.RevokedAt)
		w.u8(byte(e.Reason))
		w.text(e.HoldID)
	}
	return w.buf
}

func canonicalMigration(m *Migration) []byte {
	w := canonicalWriter{includeR: true}
	w.u8(canonicalVersion)
	w.text("crlstate-migration")
	w.text(string(m.Kind))
	w.text(string(m.IssuerID))
	w.u64(m.FromEpoch)
	w.u64(m.ToEpoch)
	w.text(string(m.OldRoot))
	w.text(string(m.NewRoot))
	w.unix(m.EffectiveAt)
	writeSpans := func(spans []PartitionSpan) {
		spans = append([]PartitionSpan(nil), spans...)
		sortSpans(spans)
		w.u64(uint64(len(spans)))
		for _, s := range spans {
			w.text(s.PartitionID)
			w.serial(s.SerialLo)
			w.serial(s.SerialHi)
			w.u64(uint64(s.Reasons))
		}
	}
	writeSpans(m.Old)
	writeSpans(m.New)
	return w.buf
}

// SigningDigest returns the SHA-256 digest covered by the manifest signature.
// It includes the trust root and every logical field except Signature.
func (m *Manifest) SigningDigest() [32]byte {
	return sha256.Sum256(canonicalManifest(m, true))
}

// ContentDigest returns a root-independent digest used to decide whether two
// cross-signed manifests carry the same logical partition content.
func (m *Manifest) ContentDigest() [32]byte {
	return sha256.Sum256(canonicalManifest(m, false))
}

// Digest is an alias for SigningDigest.
func (m *Manifest) Digest() [32]byte { return m.SigningDigest() }

// SigningDigest returns the SHA-256 digest covered by both migration
// signatures.
func (m *Migration) SigningDigest() [32]byte {
	return sha256.Sum256(canonicalMigration(m))
}

func digestString(d [32]byte) string { return fmt.Sprintf("%x", d[:]) }
