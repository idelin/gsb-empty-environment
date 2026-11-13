package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ActionFormat is the domain-separation string mixed into every canonical
// action encoding. Changing it invalidates every previously derived key.
const ActionFormat = "netsec/action-identity/v1"

// Errors returned while validating action inputs.
var (
	// ErrMissingInput means a required action input was not supplied. A
	// missing toolchain, policy, platform, or location must not produce an
	// action key.
	ErrMissingInput = errors.New("netsec: missing required action input")

	// ErrDuplicateKey means a mapping input contained the same key more than
	// once. Duplicate keys are rejected rather than silently overwritten.
	ErrDuplicateKey = errors.New("netsec: duplicate mapping key")

	// ErrEmptyMappingKey means a mapping input contained an empty key.
	ErrEmptyMappingKey = errors.New("netsec: empty mapping key")

	// ErrSecretKeyRequired means secret environment values were supplied
	// without a host HMAC key. Secret values are never encoded in plaintext.
	ErrSecretKeyRequired = errors.New("netsec: host HMAC key required for secret environment")
)

const (
	actionTagVersion        byte = 0x00
	actionTagSourceRoot     byte = 0x01
	actionTagArgs           byte = 0x02
	actionTagWorkDir        byte = 0x03
	actionTagEnvironment    byte = 0x04
	actionTagToolchain      byte = 0x05
	actionTagDependencies   byte = 0x06
	actionTagPlatform       byte = 0x07
	actionTagSecurityPolicy byte = 0x08
)

const (
	envValuePublic byte = 0
	envValueSecret byte = 1
)

// EnvVar is one environment variable entry. Secret entries keep their name in
// the canonical encoding, but their Value is replaced by a host-HMAC version
// identifier. Secret values are never written to canonical bytes.
type EnvVar struct {
	Name   string
	Value  string
	Secret bool
}

// String redacts secret values so accidental formatting or logging of an
// ActionInputs value does not expose them.
func (e EnvVar) String() string {
	if e.Secret {
		return fmt.Sprintf("EnvVar{Name:%q, Secret:true, Value:<redacted>}", e.Name)
	}
	return fmt.Sprintf("EnvVar{Name:%q, Secret:false, Value:%q}", e.Name, e.Value)
}

// GoString implements fmt.GoStringer and redacts secret values.
func (e EnvVar) GoString() string {
	return e.String()
}

// DependencyOutput identifies one dependency action and the digest of the
// output tree that action produced.
type DependencyOutput struct {
	Name   string
	Digest string
}

// Attribute is one platform attribute, for example os=linux or arch=arm64.
type Attribute struct {
	Name  string
	Value string
}

// ActionInputs is the complete input identity of one build action. Every
// scalar field is mandatory. Args, Env, and DependencyOutputs may be empty;
// Platform must contain at least one attribute so the target platform cannot
// silently fall out of the key.
type ActionInputs struct {
	// SourceRoot identifies the source tree being built. It may be an
	// absolute root path or a content-addressed source tree root supplied by
	// the build host.
	SourceRoot string

	// Args is the ordered command argument array. Order is significant.
	Args []string

	// WorkDir is the host-canonicalized working directory.
	WorkDir string

	// Env holds one entry per environment variable. Secret entries are
	// replaced by host-HMAC versions during canonical encoding.
	Env []EnvVar

	// ToolchainDigest identifies the concrete toolchain artifacts.
	ToolchainDigest string

	// DependencyOutputs maps dependency action identities to their output
	// tree digests.
	DependencyOutputs []DependencyOutput

	// Platform describes the target platform attributes.
	Platform []Attribute

	// SecurityPolicyDigest identifies the active build security policy.
	SecurityPolicyDigest string
}

// ActionKey is the SHA-256 digest of an ActionInputs canonical encoding.
type ActionKey [sha256.Size]byte

// IsZero reports whether k is the zero value. A zero action key is treated as
// a missing field by cache validation.
func (k ActionKey) IsZero() bool {
	return k == ActionKey{}
}

// Hex returns the lowercase hexadecimal representation of k.
func (k ActionKey) Hex() string {
	return hex.EncodeToString(k[:])
}

// String returns the lowercase hexadecimal representation of k.
func (k ActionKey) String() string {
	return k.Hex()
}

// ParseActionKey parses either a plain lowercase/uppercase hex SHA-256 digest
// or a string with the "sha256:" prefix.
func ParseActionKey(s string) (ActionKey, error) {
	var k ActionKey
	raw := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "sha256:")
	if len(raw) != hex.EncodedLen(sha256.Size) {
		return k, fmt.Errorf("netsec: action key must be %d hex bytes", sha256.Size)
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return k, fmt.Errorf("netsec: parse action key: %w", err)
	}
	copy(k[:], decoded)
	return k, nil
}

// MarshalText implements encoding.TextMarshaler.
func (k ActionKey) MarshalText() ([]byte, error) {
	return []byte(k.Hex()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (k *ActionKey) UnmarshalText(text []byte) error {
	parsed, err := ParseActionKey(string(text))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// MarshalJSON encodes an ActionKey as a hexadecimal string.
func (k ActionKey) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.Hex())
}

// UnmarshalJSON decodes an ActionKey from a hexadecimal string.
func (k *ActionKey) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	return k.UnmarshalText([]byte(s))
}

// Validate checks mandatory fields, mapping keys, duplicate keys, and secret
// environment requirements. It does not derive a cache key.
func (in ActionInputs) Validate(secretHMACKey []byte) error {
	if in.SourceRoot == "" {
		return fmt.Errorf("%w: source tree root", ErrMissingInput)
	}
	if in.WorkDir == "" {
		return fmt.Errorf("%w: canonical working directory", ErrMissingInput)
	}
	if in.ToolchainDigest == "" {
		return fmt.Errorf("%w: toolchain artifact digest", ErrMissingInput)
	}
	if in.SecurityPolicyDigest == "" {
		return fmt.Errorf("%w: security policy digest", ErrMissingInput)
	}
	if len(in.Platform) == 0 {
		return fmt.Errorf("%w: platform attributes", ErrMissingInput)
	}

	envNames := make(map[string]struct{}, len(in.Env))
	for _, ev := range in.Env {
		if ev.Name == "" {
			return fmt.Errorf("%w: environment variable", ErrEmptyMappingKey)
		}
		if _, exists := envNames[ev.Name]; exists {
			return fmt.Errorf("%w: environment variable %q", ErrDuplicateKey, ev.Name)
		}
		envNames[ev.Name] = struct{}{}
		if ev.Secret && len(secretHMACKey) == 0 {
			return fmt.Errorf("%w: environment variable %q", ErrSecretKeyRequired, ev.Name)
		}
	}

	depNames := make(map[string]struct{}, len(in.DependencyOutputs))
	for _, dep := range in.DependencyOutputs {
		if dep.Name == "" {
			return fmt.Errorf("%w: dependency action", ErrEmptyMappingKey)
		}
		if dep.Digest == "" {
			return fmt.Errorf("%w: dependency %q output digest", ErrMissingInput, dep.Name)
		}
		if _, exists := depNames[dep.Name]; exists {
			return fmt.Errorf("%w: dependency action %q", ErrDuplicateKey, dep.Name)
		}
		depNames[dep.Name] = struct{}{}
	}

	attrNames := make(map[string]struct{}, len(in.Platform))
	for _, attr := range in.Platform {
		if attr.Name == "" {
			return fmt.Errorf("%w: platform attribute", ErrEmptyMappingKey)
		}
		if _, exists := attrNames[attr.Name]; exists {
			return fmt.Errorf("%w: platform attribute %q", ErrDuplicateKey, attr.Name)
		}
		attrNames[attr.Name] = struct{}{}
	}

	return nil
}

// CanonicalBytes returns the deterministic encoding of the action identity.
// secretHMACKey is only used for secret environment entries; public entries do
// not depend on it. The returned bytes contain secret HMAC version identifiers
// and never contain secret values.
func (in ActionInputs) CanonicalBytes(secretHMACKey []byte) ([]byte, error) {
	if err := in.Validate(secretHMACKey); err != nil {
		return nil, err
	}

	env := append([]EnvVar(nil), in.Env...)
	sort.Slice(env, func(i, j int) bool {
		return bytes.Compare([]byte(env[i].Name), []byte(env[j].Name)) < 0
	})

	deps := append([]DependencyOutput(nil), in.DependencyOutputs...)
	sort.Slice(deps, func(i, j int) bool {
		return bytes.Compare([]byte(deps[i].Name), []byte(deps[j].Name)) < 0
	})

	attrs := append([]Attribute(nil), in.Platform...)
	sort.Slice(attrs, func(i, j int) bool {
		return bytes.Compare([]byte(attrs[i].Name), []byte(attrs[j].Name)) < 0
	})

	var b canonicalBuilder
	b.writeBytes([]byte(ActionFormat))

	b.writeTag(actionTagVersion)
	b.writeString("1")

	b.writeTag(actionTagSourceRoot)
	b.writeString(in.SourceRoot)

	b.writeTag(actionTagArgs)
	b.writeUint64(uint64(len(in.Args)))
	for _, arg := range in.Args {
		b.writeString(arg)
	}

	b.writeTag(actionTagWorkDir)
	b.writeString(in.WorkDir)

	b.writeTag(actionTagEnvironment)
	b.writeUint64(uint64(len(env)))
	for _, ev := range env {
		b.writeString(ev.Name)
		if ev.Secret {
			b.writeByte(envValueSecret)
			b.writeBytes(secretEnvVersion(secretHMACKey, ev.Name, ev.Value))
		} else {
			b.writeByte(envValuePublic)
			b.writeString(ev.Value)
		}
	}

	b.writeTag(actionTagToolchain)
	b.writeString(in.ToolchainDigest)

	b.writeTag(actionTagDependencies)
	b.writeUint64(uint64(len(deps)))
	for _, dep := range deps {
		b.writeString(dep.Name)
		b.writeString(dep.Digest)
	}

	b.writeTag(actionTagPlatform)
	b.writeUint64(uint64(len(attrs)))
	for _, attr := range attrs {
		b.writeString(attr.Name)
		b.writeString(attr.Value)
	}

	b.writeTag(actionTagSecurityPolicy)
	b.writeString(in.SecurityPolicyDigest)

	return append([]byte(nil), b.Bytes()...), nil
}

// Key returns the SHA-256 of CanonicalBytes. It fails closed if any mandatory
// input is absent or a mapping key is invalid.
func (in ActionInputs) Key(secretHMACKey []byte) (ActionKey, error) {
	canonical, err := in.CanonicalBytes(secretHMACKey)
	if err != nil {
		return ActionKey{}, err
	}
	return ActionKey(sha256.Sum256(canonical)), nil
}

// KeyHex returns the hexadecimal representation of the action key.
func (in ActionInputs) KeyHex(secretHMACKey []byte) (string, error) {
	key, err := in.Key(secretHMACKey)
	if err != nil {
		return "", err
	}
	return key.Hex(), nil
}

// Canonicalize is a package-level convenience function for CanonicalBytes.
func Canonicalize(in ActionInputs, secretHMACKey []byte) ([]byte, error) {
	return in.CanonicalBytes(secretHMACKey)
}

// ComputeActionKey is a package-level convenience function for Key.
func ComputeActionKey(in ActionInputs, secretHMACKey []byte) (ActionKey, error) {
	return in.Key(secretHMACKey)
}

// secretEnvVersion computes the host-HMAC identifier that stands in for a
// secret environment value. The variable name is included so the same secret
// value used under two names produces two distinct identifiers. Length
// prefixes avoid separator ambiguity.
func secretEnvVersion(secretHMACKey []byte, name, value string) []byte {
	mac := hmac.New(sha256.New, secretHMACKey)
	mac.Write([]byte("netsec/secret-env-version/v1"))
	writeHMACField(mac, []byte(name))
	writeHMACField(mac, []byte(value))
	return mac.Sum(nil)
}

type hmacWriter interface {
	Write([]byte) (int, error)
}

func writeHMACField(w hmacWriter, data []byte) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(data)))
	_, _ = w.Write(lenBuf[:])
	_, _ = w.Write(data)
}

type canonicalBuilder struct {
	bytes.Buffer
}

func (b *canonicalBuilder) writeTag(tag byte) {
	b.WriteByte(tag)
}

func (b *canonicalBuilder) writeByte(v byte) {
	b.WriteByte(v)
}

func (b *canonicalBuilder) writeUint64(v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	b.Write(buf[:])
}

func (b *canonicalBuilder) writeString(s string) {
	b.writeBytes([]byte(s))
}

func (b *canonicalBuilder) writeBytes(data []byte) {
	b.writeUint64(uint64(len(data)))
	b.Write(data)
}
