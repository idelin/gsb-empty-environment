package netsec

import (
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
)

// Name is the canonical TPM Name of a command handle. For permanent handles
// this is the big-endian handle. For objects and NV indexes it is normally
// nameAlg || H(publicArea), so callers must supply the resolved Name rather
// than letting the transaction layer guess from the numeric handle.
type Name []byte

// NameFromHandle returns the numeric-handle Name used by permanent HR handles
// and other TPM handles whose Name is defined as the handle itself. Object
// handles and NV indexes generally require NameFromPublic instead.
func NameFromHandle(h Handle) Name {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(h))
	return append(Name(nil), b[:]...)
}

// NameFromPublic computes nameAlg || H(publicArea).
func NameFromPublic(alg Algorithm, publicArea []byte) (Name, error) {
	digest, err := hashParts(alg, publicArea)
	if err != nil {
		return nil, err
	}
	var algBytes [2]byte
	binary.BigEndian.PutUint16(algBytes[:], uint16(alg))
	name := make(Name, 0, 2+len(digest))
	name = append(name, algBytes[:]...)
	name = append(name, digest...)
	return name, nil
}

// NVWriteParameters encodes the TPM2_NV_Write parameter area:
// TPM2B_MAX_NV_BUFFER data followed by the 16-bit offset.
func NVWriteParameters(data []byte, offset uint16) ([]byte, error) {
	if len(data) > 0xffff {
		return nil, errors.New("tpm: NV write data is too large")
	}
	out := appendTPM2B(nil, data)
	return appendU16(out, offset), nil
}

// CommandTemplate is the exact command whose execution is being approved.
// Names must be in the same order as Handles and must already be canonical.
// The transaction layer takes a deep copy when authorization starts.
type CommandTemplate struct {
	Code       CommandCode
	Handles    []Handle
	Names      []Name
	Parameters []byte
}

func (t CommandTemplate) clone() CommandTemplate {
	out := CommandTemplate{
		Code:       t.Code,
		Handles:    append([]Handle(nil), t.Handles...),
		Names:      make([]Name, len(t.Names)),
		Parameters: append([]byte(nil), t.Parameters...),
	}
	for i := range t.Names {
		out.Names[i] = append(Name(nil), t.Names[i]...)
	}
	return out
}

// Validate checks the canonical handle/Name relationship and all immutable
// fields that participate in cpHash.
func (t CommandTemplate) Validate() error {
	if t.Code == 0 {
		return fmt.Errorf("%w: command code is zero", ErrInvalidCommand)
	}
	if len(t.Handles) != len(t.Names) {
		return fmt.Errorf("%w: %d handles but %d Names", ErrInvalidCommand, len(t.Handles), len(t.Names))
	}
	for i, name := range t.Names {
		if len(name) == 0 {
			return fmt.Errorf("%w: Name for handle %#08x is empty", ErrInvalidCommand, uint32(t.Handles[i]))
		}
	}
	return nil
}

// CPHash computes H(commandCode || Name1 || ... || NameN || parameters).
func (t CommandTemplate) CPHash(alg Algorithm) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	parts := make([][]byte, 0, len(t.Names)+2)
	var code [4]byte
	binary.BigEndian.PutUint32(code[:], uint32(t.Code))
	parts = append(parts, code[:])
	for _, name := range t.Names {
		parts = append(parts, name)
	}
	parts = append(parts, t.Parameters)
	return hashParts(alg, parts...)
}

// NameHash computes H(Name1 || ... || NameN).
func (t CommandTemplate) NameHash(alg Algorithm) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	parts := make([][]byte, 0, len(t.Names))
	for _, name := range t.Names {
		parts = append(parts, name)
	}
	return hashParts(alg, parts...)
}

// ExtendPolicyDigest performs the common TPM policy update:
// H(oldDigest || commandCode || input).
func ExtendPolicyDigest(alg Algorithm, oldDigest []byte, code CommandCode, input []byte) ([]byte, error) {
	size, err := alg.Size()
	if err != nil {
		return nil, err
	}
	if len(oldDigest) != size {
		return nil, fmt.Errorf("%w: policy digest is %d bytes, want %d", ErrInvalidBinding, len(oldDigest), size)
	}
	var codeBytes [4]byte
	binary.BigEndian.PutUint32(codeBytes[:], uint32(code))
	return hashParts(alg, oldDigest, codeBytes[:], input)
}

// EncodePolicyORList returns the exact TPML_DIGEST parameter passed to
// PolicyOR and hashed into the next policy digest.
func EncodePolicyORList(digests [][]byte) ([]byte, error) {
	if len(digests) == 0 {
		return nil, errors.New("tpm: PolicyOR requires at least one branch")
	}
	out := make([]byte, 0, 4+len(digests)*4)
	out = appendU32(out, uint32(len(digests)))
	for _, digest := range digests {
		if len(digest) == 0 || len(digest) > 0xffff {
			return nil, errors.New("tpm: invalid PolicyOR digest")
		}
		out = appendTPM2B(out, digest)
	}
	return out, nil
}

// NextPolicyORDigest returns H(oldDigest || TPM_CC_PolicyOR || pHashList).
func NextPolicyORDigest(alg Algorithm, oldDigest []byte, digests [][]byte) ([]byte, error) {
	payload, err := EncodePolicyORList(digests)
	if err != nil {
		return nil, err
	}
	return ExtendPolicyDigest(alg, oldDigest, CCPolicyOR, payload)
}

// FrozenCommand is an immutable command approval. Its fields can only be read
// through copying getters, so no caller can change a handle, Name, command code
// or parameter byte after PolicyCpHash succeeds.
type FrozenCommand struct {
	code       CommandCode
	handles    []Handle
	names      []Name
	parameters []byte
	cpHash     []byte
	nameHash   []byte
}

func freezeCommand(t CommandTemplate, alg Algorithm) (*FrozenCommand, error) {
	t = t.clone()
	if err := t.Validate(); err != nil {
		return nil, err
	}
	cpHash, err := t.CPHash(alg)
	if err != nil {
		return nil, err
	}
	nameHash, err := t.NameHash(alg)
	if err != nil {
		return nil, err
	}
	return &FrozenCommand{
		code:       t.Code,
		handles:    t.Handles,
		names:      t.Names,
		parameters: t.Parameters,
		cpHash:     cpHash,
		nameHash:   nameHash,
	}, nil
}

func (f *FrozenCommand) CommandCode() CommandCode {
	if f == nil {
		return 0
	}
	return f.code
}

func (f *FrozenCommand) Handles() []Handle {
	if f == nil {
		return nil
	}
	return append([]Handle(nil), f.handles...)
}

func (f *FrozenCommand) Names() []Name {
	if f == nil {
		return nil
	}
	out := make([]Name, len(f.names))
	for i := range f.names {
		out[i] = append(Name(nil), f.names[i]...)
	}
	return out
}

func (f *FrozenCommand) Parameters() []byte {
	if f == nil {
		return nil
	}
	return append([]byte(nil), f.parameters...)
}

func (f *FrozenCommand) CPHash() []byte {
	if f == nil {
		return nil
	}
	return append([]byte(nil), f.cpHash...)
}

func (f *FrozenCommand) NameHash() []byte {
	if f == nil {
		return nil
	}
	return append([]byte(nil), f.nameHash...)
}

func (f *FrozenCommand) cloneTemplate() CommandTemplate {
	return CommandTemplate{
		Code:       f.code,
		Handles:    f.Handles(),
		Names:      f.Names(),
		Parameters: f.Parameters(),
	}
}

func (f *FrozenCommand) marshal(auth AuthValues) ([]byte, error) {
	if f == nil {
		return nil, errors.New("tpm: nil frozen command")
	}
	return MarshalCommand(f.code, f.handles, f.parameters, []AuthValues{auth})
}

func equalDigest(a, b []byte) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1
}

// TemplatesEqual compares two command templates including exact parameter
// bytes. It is useful for auditing the approved object against a captured
// command.
func TemplatesEqual(a, b CommandTemplate) bool {
	if a.Code != b.Code || len(a.Handles) != len(b.Handles) || len(a.Names) != len(b.Names) {
		return false
	}
	for i := range a.Handles {
		if a.Handles[i] != b.Handles[i] {
			return false
		}
	}
	for i := range a.Names {
		if !bytes.Equal(a.Names[i], b.Names[i]) {
			return false
		}
	}
	return bytes.Equal(a.Parameters, b.Parameters)
}
