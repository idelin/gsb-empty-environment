package krb5policy

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	domainTicket = "krb5policy/ticket/v1\x00"
	domainPAC    = "krb5policy/pac/v1\x00"
)

// Key is one HMAC-SHA256 keytab entry.  Version is the Kerberos key version
// number; zero selects the highest available version at lookup time.
type Key struct {
	Principal string
	Version   int
	Bytes     []byte
}

// Keytab is an immutable-in-practice set of key material held by the host.
// Add copies the key bytes.
type Keytab struct {
	keys map[string]map[int][]byte
}

// NewKeytab builds a keytab.  An empty keytab is legal structurally, but the
// high-level Verifier rejects it because it cannot verify anything.
func NewKeytab(keys ...Key) (*Keytab, error) {
	kt := &Keytab{keys: make(map[string]map[int][]byte)}
	for i, key := range keys {
		if err := kt.Add(key); err != nil {
			return nil, fmt.Errorf("key %d: %w", i, err)
		}
	}
	return kt, nil
}

// Add inserts one key.  A duplicate principal/version pair is rejected rather
// than silently choosing one key.
func (kt *Keytab) Add(key Key) error {
	if kt == nil {
		return fmt.Errorf("%w: nil keytab", ErrInvalidConfig)
	}
	if strings.TrimSpace(key.Principal) == "" {
		return fmt.Errorf("%w: empty key principal", ErrInvalidConfig)
	}
	if len(key.Bytes) == 0 {
		return fmt.Errorf("%w: empty key material for %q", ErrInvalidConfig, key.Principal)
	}
	if key.Version < 0 {
		return fmt.Errorf("%w: negative key version", ErrInvalidConfig)
	}
	principal := canonicalPrincipalString(key.Principal)
	if kt.keys[principal] == nil {
		kt.keys[principal] = make(map[int][]byte)
	}
	if _, exists := kt.keys[principal][key.Version]; exists {
		return fmt.Errorf("%w: duplicate key %s kvno %d", ErrInvalidConfig, key.Principal, key.Version)
	}
	kt.keys[principal][key.Version] = append([]byte(nil), key.Bytes...)
	return nil
}

// Lookup returns a copy of the key for principal/version.  Version zero
// selects the highest stored key version.
func (kt *Keytab) Lookup(principal string, version int) ([]byte, error) {
	if kt == nil {
		return nil, fmt.Errorf("%w: nil keytab", ErrUnknownKey)
	}
	if version < 0 {
		return nil, fmt.Errorf("%w: negative key version", ErrUnknownKey)
	}
	versions := kt.keys[canonicalPrincipalString(principal)]
	if len(versions) == 0 {
		return nil, fmt.Errorf("%w: principal %q", ErrUnknownKey, principal)
	}
	if version > 0 {
		if key, ok := versions[version]; ok {
			return append([]byte(nil), key...), nil
		}
		return nil, fmt.Errorf("%w: principal %q kvno %d", ErrUnknownKey, principal, version)
	}
	best := 0
	for v := range versions {
		if v > best {
			best = v
		}
	}
	return append([]byte(nil), versions[best]...), nil
}

// Versions returns stored versions for a principal.
func (kt *Keytab) Versions(principal string) []int {
	if kt == nil {
		return nil
	}
	versions := kt.keys[canonicalPrincipalString(principal)]
	out := make([]int, 0, len(versions))
	for v := range versions {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// SealTicket computes and installs a real HMAC-SHA256 ticket seal.  If the key
// has no principal, the ticket server principal is used, because that is the
// principal whose long-term key protects a service ticket in Kerberos.
func SealTicket(t *Ticket, key Key) error {
	if t == nil {
		return fmt.Errorf("%w: nil ticket", ErrIntegrity)
	}
	if len(key.Bytes) == 0 {
		return fmt.Errorf("%w: empty key material", ErrInvalidConfig)
	}
	principal := strings.TrimSpace(key.Principal)
	if principal == "" {
		principal = t.Server.String()
	}
	if principal == "" {
		return fmt.Errorf("%w: no key principal or ticket server", ErrInvalidConfig)
	}
	if key.Version < 0 {
		return fmt.Errorf("%w: negative key version", ErrInvalidConfig)
	}
	value := ticketHMAC(t, key.Bytes)
	t.Seal = TicketSeal{
		Algorithm:    MACAlgorithmHMACSHA256,
		KeyPrincipal: principal,
		KeyVersion:   key.Version,
		Value:        value,
	}
	return nil
}

// SealPAC appends a PAC HMAC-SHA256 signature.  Multiple signatures are
// supported so KDC and service signatures can coexist.
func SealPAC(p *PAC, key Key) error {
	if p == nil {
		return fmt.Errorf("%w: nil PAC", ErrPACIntegrity)
	}
	if len(key.Bytes) == 0 {
		return fmt.Errorf("%w: empty key material", ErrInvalidConfig)
	}
	if strings.TrimSpace(key.Principal) == "" {
		return fmt.Errorf("%w: PAC signer principal is required", ErrInvalidConfig)
	}
	if key.Version < 0 {
		return fmt.Errorf("%w: negative key version", ErrInvalidConfig)
	}
	p.Signatures = append(p.Signatures, PACSignature{
		Algorithm:    MACAlgorithmHMACSHA256,
		KeyPrincipal: key.Principal,
		KeyVersion:   key.Version,
		Value:        pacHMAC(p, key.Bytes),
	})
	return nil
}

// ReplacePACSignature replaces the first signature made by key.Principal and
// key.Version, or appends one if none exists.
func ReplacePACSignature(p *PAC, key Key) error {
	if p == nil {
		return fmt.Errorf("%w: nil PAC", ErrPACIntegrity)
	}
	if len(key.Bytes) == 0 {
		return fmt.Errorf("%w: empty key material", ErrInvalidConfig)
	}
	if strings.TrimSpace(key.Principal) == "" {
		return fmt.Errorf("%w: PAC signer principal is required", ErrInvalidConfig)
	}
	if key.Version < 0 {
		return fmt.Errorf("%w: negative key version", ErrInvalidConfig)
	}
	value := pacHMAC(p, key.Bytes)
	sig := PACSignature{
		Algorithm:    MACAlgorithmHMACSHA256,
		KeyPrincipal: key.Principal,
		KeyVersion:   key.Version,
		Value:        value,
	}
	canonical := canonicalPrincipalString(key.Principal)
	for i := range p.Signatures {
		if canonicalPrincipalString(p.Signatures[i].KeyPrincipal) == canonical && p.Signatures[i].KeyVersion == key.Version {
			p.Signatures[i] = sig
			return nil
		}
	}
	p.Signatures = append(p.Signatures, sig)
	return nil
}

// VerifyTicketMAC verifies a ticket's HMAC-SHA256 seal using keytab.  It is
// deliberately possible to call this without the stricter server-key binding
// check; cryptographic verification alone is still real HMAC verification.
func VerifyTicketMAC(t *Ticket, kt *Keytab) error {
	if t == nil {
		return fmt.Errorf("%w: nil ticket", ErrIntegrity)
	}
	if kt == nil {
		return fmt.Errorf("%w: nil keytab", ErrIntegrity)
	}
	if err := checkMACAlgorithm(t.Seal.Algorithm); err != nil {
		return err
	}
	principal := t.Seal.KeyPrincipal
	if strings.TrimSpace(principal) == "" {
		principal = t.Server.String()
	}
	key, err := kt.Lookup(principal, t.Seal.KeyVersion)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIntegrity, err)
	}
	want := ticketHMAC(t, key)
	if !hmac.Equal(want, t.Seal.Value) {
		return fmt.Errorf("%w: ticket seal mismatch for %s", ErrIntegrity, principal)
	}
	return nil
}

// VerifyTicketIntegrity verifies the MAC and, by default, binds the seal key
// to the ticket's server principal.  A ticket whose server is
// krbtgt/DEST@ISSUER is protected by that krbtgt principal's key, exactly as
// in Kerberos.
func VerifyTicketIntegrity(t *Ticket, kt *Keytab, requireServerKey bool) error {
	if err := VerifyTicketMAC(t, kt); err != nil {
		return err
	}
	if !requireServerKey {
		return nil
	}
	expected := t.Server.String()
	if expected == "" {
		return fmt.Errorf("%w: ticket has no server principal", ErrIntegrity)
	}
	got := t.Seal.KeyPrincipal
	if got == "" {
		got = expected
	}
	if canonicalPrincipalString(got) != canonicalPrincipalString(expected) {
		return fmt.Errorf("%w: seal key %q does not match ticket server %q", ErrIntegrity, got, expected)
	}
	return nil
}

// VerifyPAC verifies the PAC body with real HMAC-SHA256 keys.
//
// trustedSigners is a list of principal names permitted to sign a PAC.  A "*"
// entry permits any principal present in the keytab.  An empty list also
// permits any keytab principal.  If a trusted signature is present but
// invalid, verification fails even when another signature happens to verify.
func VerifyPAC(p *PAC, kt *Keytab, trustedSigners []string) error {
	if p == nil {
		return fmt.Errorf("%w: nil PAC", ErrPACIntegrity)
	}
	if kt == nil {
		return fmt.Errorf("%w: nil keytab", ErrPACIntegrity)
	}
	if len(p.Signatures) == 0 {
		return fmt.Errorf("%w: PAC has no signatures", ErrPACIntegrity)
	}
	trust := make(map[string]struct{}, len(trustedSigners))
	anySigner := len(trustedSigners) == 0
	for _, s := range trustedSigners {
		if strings.TrimSpace(s) == "*" {
			anySigner = true
			continue
		}
		trust[canonicalPrincipalString(s)] = struct{}{}
	}
	valid := 0
	for i, sig := range p.Signatures {
		if err := checkMACAlgorithm(sig.Algorithm); err != nil {
			return fmt.Errorf("%w: signature %d: %v", ErrPACIntegrity, i+1, err)
		}
		if strings.TrimSpace(sig.KeyPrincipal) == "" {
			return fmt.Errorf("%w: signature %d has no key principal", ErrPACIntegrity, i+1)
		}
		canonical := canonicalPrincipalString(sig.KeyPrincipal)
		_, trusted := trust[canonical]
		if !anySigner && !trusted {
			continue
		}
		key, err := kt.Lookup(sig.KeyPrincipal, sig.KeyVersion)
		if err != nil {
			continue
		}
		want := pacHMAC(p, key)
		if !hmac.Equal(want, sig.Value) {
			return fmt.Errorf("%w: signature %d by %s is invalid", ErrPACIntegrity, i+1, sig.KeyPrincipal)
		}
		valid++
	}
	if valid == 0 {
		return fmt.Errorf("%w: no trusted valid PAC signature", ErrPACIntegrity)
	}
	return nil
}

func checkMACAlgorithm(algorithm string) error {
	switch strings.ToUpper(strings.TrimSpace(algorithm)) {
	case "", MACAlgorithmHMACSHA256, "HMAC-SHA-256":
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedMAC, algorithm)
	}
}

func ticketHMAC(t *Ticket, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(domainTicket))
	mac.Write(canonicalTicketBytes(t))
	return mac.Sum(nil)
}

func pacHMAC(p *PAC, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(domainPAC))
	mac.Write(canonicalPACBytes(p))
	return mac.Sum(nil)
}

func canonicalTicketBytes(t *Ticket) []byte {
	var b bytes.Buffer
	writeString(&b, "ticket-v1")
	writePrincipal(&b, t.Client)
	writePrincipal(&b, t.Server)
	writeString(&b, canonicalRealm(t.EffectiveIssuerRealm()))
	writeU32(&b, uint32(t.Flags))
	writeTime(&b, t.AuthTime)
	writeTime(&b, t.StartTime)
	writeTime(&b, t.EndTime)
	writeTime(&b, t.RenewTill)
	writeI32(&b, t.Transited.Type)
	writeBytes(&b, t.Transited.Contents)
	b.WriteByte(0)
	if t.PAC != nil {
		b.WriteByte(1)
		b.Write(canonicalPACBytes(t.PAC))
	}
	return b.Bytes()
}

func canonicalPACBytes(p *PAC) []byte {
	var b bytes.Buffer
	writeString(&b, "pac-v1")
	writePrincipal(&b, p.User)
	writeU32(&b, uint32(p.UserFlags))
	writeU32(&b, uint32(len(p.Groups)))
	for _, group := range p.Groups {
		writeString(&b, group)
	}
	writeTime(&b, p.LogonTime)
	return b.Bytes()
}

func writePrincipal(b *bytes.Buffer, p Principal) {
	writeU32(b, uint32(len(p.Name)))
	for _, part := range p.Name {
		writeString(b, part)
	}
	writeString(b, canonicalRealm(p.Realm))
}

func writeString(b *bytes.Buffer, s string) {
	writeU32(b, uint32(len(s)))
	b.WriteString(s)
}

func writeBytes(b *bytes.Buffer, p []byte) {
	writeU32(b, uint32(len(p)))
	b.Write(p)
}

func writeU32(b *bytes.Buffer, v uint32) {
	_ = binary.Write(b, binary.BigEndian, v)
}

func writeI32(b *bytes.Buffer, v int32) {
	_ = binary.Write(b, binary.BigEndian, v)
}

func writeTime(b *bytes.Buffer, t time.Time) {
	if t.IsZero() {
		b.WriteByte(0)
		_ = binary.Write(b, binary.BigEndian, int64(0))
		return
	}
	b.WriteByte(1)
	_ = binary.Write(b, binary.BigEndian, t.UTC().UnixNano())
}
