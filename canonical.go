package netsec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"time"
)

func appendUint64(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[:]...)
}

func appendBytes(dst, value []byte) []byte {
	dst = appendUint64(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendString(dst []byte, value string) []byte {
	return appendBytes(dst, []byte(value))
}

func canonicalExtensions(exts []Extension) []byte {
	sorted := append([]Extension(nil), exts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Type != sorted[j].Type {
			return sorted[i].Type < sorted[j].Type
		}
		return bytes.Compare(sorted[i].Data, sorted[j].Data) < 0
	})
	var out []byte
	out = appendUint64(out, uint64(len(sorted)))
	for _, ext := range sorted {
		var hdr [2]byte
		binary.BigEndian.PutUint16(hdr[:], ext.Type)
		out = append(out, hdr[:]...)
		out = appendBytes(out, ext.Data)
	}
	return out
}

func extensionsEqual(a, b []Extension) bool {
	return bytes.Equal(canonicalExtensions(a), canonicalExtensions(b))
}

func treesEqual(a, b RatchetTree) bool {
	if len(a.Leaves) != len(b.Leaves) || !extensionsEqual(a.Extensions, b.Extensions) {
		return false
	}
	for i := range a.Leaves {
		if !leavesEqual(a.Leaves[i], b.Leaves[i]) {
			return false
		}
	}
	return true
}

func leavesEqual(a, b LeafNode) bool {
	return a.Index == b.Index &&
		a.Blank == b.Blank &&
		a.Credential.Equal(b.Credential) &&
		bytes.Equal(a.SignatureKey, b.SignatureKey) &&
		bytes.Equal(a.EncryptionKey, b.EncryptionKey) &&
		capabilitiesEqual(a.Capabilities, b.Capabilities) &&
		extensionsEqual(a.Extensions, b.Extensions)
}

func capabilitiesEqual(a, b Capabilities) bool {
	return uint16sEqual(a.Versions, b.Versions) &&
		cipherSuitesEqual(a.CipherSuites, b.CipherSuites) &&
		uint16sEqual(a.Extensions, b.Extensions) &&
		uint16sEqual(a.Proposals, b.Proposals) &&
		uint16sEqual(a.Credentials, b.Credentials)
}

func uint16sEqual(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cipherSuitesEqual(a, b []CipherSuite) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func canonicalPSKIDs(ids [][]byte) []byte {
	var out []byte
	out = appendUint64(out, uint64(len(ids)))
	for _, id := range ids {
		out = appendBytes(out, id)
	}
	return out
}

func normalizePSKIDs(ids [][]byte) ([][]byte, error) {
	out := make([][]byte, len(ids))
	for i, id := range ids {
		if len(id) == 0 {
			return nil, fmt.Errorf("%w: empty psk id", ErrInvalidGroupSecrets)
		}
		out[i] = cloneBytes(id)
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i], out[j]) < 0
	})
	for i := 1; i < len(out); i++ {
		if bytes.Equal(out[i-1], out[i]) {
			return nil, fmt.Errorf("%w: duplicate psk id", ErrInvalidGroupSecrets)
		}
	}
	return out, nil
}

func canonicalGroupInfo(gi GroupInfo) []byte {
	var out []byte
	out = appendString(out, "MLS-GROUP-INFO-V1")
	out = appendBytes(out, gi.GroupID)
	out = appendUint64(out, gi.Epoch)
	out = appendUint64(out, uint64(gi.CipherSuite))
	out = appendBytes(out, gi.TreeHash)
	out = appendBytes(out, gi.TranscriptHash)
	out = appendBytes(out, canonicalExtensions(gi.Extensions))
	out = appendUint64(out, uint64(len(gi.RequiredPSKIDs)))
	for _, id := range gi.RequiredPSKIDs {
		out = appendBytes(out, id)
	}
	out = appendUint64(out, uint64(gi.Signer))
	if gi.SignerCredential == nil {
		out = appendUint64(out, 0)
	} else {
		out = appendUint64(out, 1)
		out = appendUint64(out, uint64(gi.SignerCredential.Type))
		out = appendBytes(out, gi.SignerCredential.Identity)
		out = appendBytes(out, gi.SignerCredential.PublicKey)
	}
	out = appendBytes(out, gi.Signature)
	return out
}

func canonicalWelcome(w *Welcome) []byte {
	var out []byte
	out = appendString(out, "MLS-WELCOME-V1")
	out = appendBytes(out, canonicalGroupInfo(w.GroupInfo))
	out = appendUint64(out, uint64(len(w.Secrets)))
	for _, secret := range w.Secrets {
		out = appendBytes(out, secret.KeyPackageRef)
		out = appendBytes(out, secret.Ciphertext)
	}
	out = appendBytes(out, w.RatchetTree)
	if w.TreeReference == nil {
		out = appendUint64(out, 0)
	} else {
		out = appendUint64(out, 1)
		out = appendBytes(out, w.TreeReference.GroupID)
		out = appendUint64(out, w.TreeReference.Epoch)
		out = appendBytes(out, w.TreeReference.TreeHash)
		out = appendBytes(out, w.TreeReference.Binding)
	}
	out = appendBytes(out, w.PriorTranscriptHash)
	out = appendBytes(out, w.ConfirmationTag)
	return out
}

func canonicalLeaf(leaf LeafNode) []byte {
	var out []byte
	out = appendUint64(out, uint64(leaf.Index))
	if leaf.Blank {
		out = appendUint64(out, 1)
	} else {
		out = appendUint64(out, 0)
	}
	out = appendUint64(out, uint64(leaf.Credential.Type))
	out = appendBytes(out, leaf.Credential.Identity)
	out = appendBytes(out, leaf.Credential.PublicKey)
	out = appendBytes(out, leaf.SignatureKey)
	out = appendBytes(out, leaf.EncryptionKey)
	out = append(out, canonicalCapabilities(leaf.Capabilities)...)
	out = appendBytes(out, canonicalExtensions(leaf.Extensions))
	return out
}

func canonicalCapabilities(capabilities Capabilities) []byte {
	var out []byte
	out = appendUint64(out, uint64(len(capabilities.Versions)))
	for _, version := range capabilities.Versions {
		out = appendUint64(out, uint64(version))
	}
	out = appendUint64(out, uint64(len(capabilities.CipherSuites)))
	for _, suite := range capabilities.CipherSuites {
		out = appendUint64(out, uint64(suite))
	}
	out = appendUint64(out, uint64(len(capabilities.Extensions)))
	for _, extension := range capabilities.Extensions {
		out = appendUint64(out, uint64(extension))
	}
	out = appendUint64(out, uint64(len(capabilities.Proposals)))
	for _, proposal := range capabilities.Proposals {
		out = appendUint64(out, uint64(proposal))
	}
	out = appendUint64(out, uint64(len(capabilities.Credentials)))
	for _, credential := range capabilities.Credentials {
		out = appendUint64(out, uint64(credential))
	}
	return out
}

func canonicalKeyPackage(kp KeyPackage) []byte {
	var out []byte
	out = appendString(out, "MLS-KEY-PACKAGE-V1")
	out = appendBytes(out, kp.GroupID)
	out = appendUint64(out, uint64(kp.CipherSuite))
	out = appendUint64(out, uint64(kp.LeafIndex))
	out = appendUint64(out, canonicalTime(kp.NotBefore))
	out = appendUint64(out, canonicalTime(kp.NotAfter))
	out = appendBytes(out, canonicalLeaf(kp.Leaf))
	return out
}

func canonicalTime(value time.Time) uint64 {
	if value.IsZero() {
		return 0
	}
	return uint64(value.UnixNano())
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func cloneStrings(values [][]byte) [][]byte {
	if values == nil {
		return nil
	}
	out := make([][]byte, len(values))
	for i := range values {
		out[i] = cloneBytes(values[i])
	}
	return out
}

func cloneExtensions(exts []Extension) []Extension {
	if exts == nil {
		return nil
	}
	out := make([]Extension, len(exts))
	for i, ext := range exts {
		out[i] = Extension{Type: ext.Type, Data: cloneBytes(ext.Data)}
	}
	return out
}

func cloneCredential(c *Credential) *Credential {
	if c == nil {
		return nil
	}
	return &Credential{Type: c.Type, Identity: cloneBytes(c.Identity), PublicKey: cloneBytes(c.PublicKey)}
}

func cloneLeaf(leaf LeafNode) LeafNode {
	leaf.Credential.Identity = cloneBytes(leaf.Credential.Identity)
	leaf.Credential.PublicKey = cloneBytes(leaf.Credential.PublicKey)
	leaf.SignatureKey = cloneBytes(leaf.SignatureKey)
	leaf.EncryptionKey = cloneBytes(leaf.EncryptionKey)
	leaf.Extensions = cloneExtensions(leaf.Extensions)
	return leaf
}

func cloneTree(tree RatchetTree) RatchetTree {
	out := RatchetTree{
		Leaves:     make([]LeafNode, len(tree.Leaves)),
		Extensions: cloneExtensions(tree.Extensions),
	}
	for i := range tree.Leaves {
		out.Leaves[i] = cloneLeaf(tree.Leaves[i])
	}
	return out
}

func cloneGroupInfo(gi GroupInfo) GroupInfo {
	gi.GroupID = cloneBytes(gi.GroupID)
	gi.TreeHash = cloneBytes(gi.TreeHash)
	gi.TranscriptHash = cloneBytes(gi.TranscriptHash)
	gi.Extensions = cloneExtensions(gi.Extensions)
	gi.RequiredPSKIDs = cloneStrings(gi.RequiredPSKIDs)
	gi.SignerCredential = cloneCredential(gi.SignerCredential)
	gi.Signature = cloneBytes(gi.Signature)
	return gi
}

func cloneWelcome(w *Welcome) *Welcome {
	if w == nil {
		return nil
	}
	out := &Welcome{
		GroupInfo:           cloneGroupInfo(w.GroupInfo),
		Secrets:             make([]EncryptedGroupSecrets, len(w.Secrets)),
		RatchetTree:         cloneBytes(w.RatchetTree),
		PriorTranscriptHash: cloneBytes(w.PriorTranscriptHash),
		ConfirmationTag:     cloneBytes(w.ConfirmationTag),
	}
	for i, secret := range w.Secrets {
		out.Secrets[i] = EncryptedGroupSecrets{
			KeyPackageRef: cloneBytes(secret.KeyPackageRef),
			Ciphertext:    cloneBytes(secret.Ciphertext),
		}
	}
	if w.Tree != nil {
		tree := cloneTree(*w.Tree)
		out.Tree = &tree
	}
	if w.TreeReference != nil {
		out.TreeReference = &RatchetTreeReference{
			GroupID:  cloneBytes(w.TreeReference.GroupID),
			Epoch:    w.TreeReference.Epoch,
			TreeHash: cloneBytes(w.TreeReference.TreeHash),
			Binding:  cloneBytes(w.TreeReference.Binding),
		}
	}
	return out
}

func cloneGroupState(state GroupState) GroupState {
	state.GroupID = cloneBytes(state.GroupID)
	state.TreeHash = cloneBytes(state.TreeHash)
	state.TranscriptHash = cloneBytes(state.TranscriptHash)
	state.Extensions = cloneExtensions(state.Extensions)
	state.RatchetTree = cloneBytes(state.RatchetTree)
	return state
}
