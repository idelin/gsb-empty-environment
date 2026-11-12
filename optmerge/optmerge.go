// Package optmerge merges the authorization options carried by an SSH user
// certificate with the options attached to the matching principal lines of
// the server-side authorized principals file.
//
// The merge rules implemented here deliberately follow the least-privilege
// semantics described by the gateway specification rather than the
// "first matching principal wins" behavior of stock OpenSSH:
//
//   - Every certificate principal that appears in the principal file matches
//     its own line; a certificate with several principals therefore matches
//     several lines at once.
//   - A forced command present on any matched line, or on the certificate, is
//     mandatory. Two distinct forced commands deny the login outright.
//   - source-address constraints are intersected across the certificate and
//     every matched line; an empty intersection denies the login.
//   - Principal lines may only tighten certificate permissions: a line cannot
//     grant permit-pty when the certificate lacks it.
//   - Unknown options are rejected instead of being ignored.
package optmerge

import (
	"errors"
	"net/netip"
)

// Option names recognized in the authorized principals file. Anything else is
// treated as an unknown option and rejected.
const (
	optionCommand       = "command"
	optionSourceAddress = "source-address"
	optionPermitPTY     = "permit-pty"
)

var (
	// ErrNoMatchingPrincipal means none of the certificate principals are
	// listed in the authorized principals file.
	ErrNoMatchingPrincipal = errors.New("optmerge: no certificate principal matches the principal file")

	// ErrCommandConflict means two matched lines, or a matched line and the
	// certificate, require different forced commands.
	ErrCommandConflict = errors.New("optmerge: conflicting forced commands")

	// ErrEmptySourceIntersection means the source-address constraints of the
	// certificate and the matched lines have no network in common.
	ErrEmptySourceIntersection = errors.New("optmerge: source-address intersection is empty")

	// ErrSourceAddressDenied means the client address is not permitted by the
	// merged source-address constraints.
	ErrSourceAddressDenied = errors.New("optmerge: client source address is not permitted")

	// ErrPTYElevation means a principal line attempts to grant permit-pty
	// while the certificate does not carry that extension.
	ErrPTYElevation = errors.New("optmerge: principal line cannot grant permit-pty absent from the certificate")

	// ErrUnknownOption means an option the gateway does not understand was
	// encountered on a principal line.
	ErrUnknownOption = errors.New("optmerge: unknown option")
)

// Certificate holds the authorization-relevant parts of an SSH user
// certificate. Fields are populated by the gateway from the parsed
// ssh.Certificate rather than depending on golang.org/x/crypto directly.
type Certificate struct {
	// Principals are the names embedded in the certificate.
	Principals []string

	// ForceCommand is the certificate critical option force-command. An empty
	// string means the certificate carries no forced command.
	ForceCommand string

	// HasForceCommand distinguishes "no force-command option" from an option
	// whose value happens to be the empty string.
	HasForceCommand bool

	// PermitPTY reports whether the certificate carries the permit-pty
	// extension.
	PermitPTY bool

	// SourceAddresses are the networks allowed by the certificate
	// source-address critical option. A nil slice means the certificate
	// imposes no source-address restriction.
	SourceAddresses []netip.Prefix

	// UnknownCriticalOptions lists critical option names the gateway does not
	// understand. SSH critical options must be rejected rather than ignored.
	UnknownCriticalOptions []string
}

// PrincipalLine is one parsed entry of an authorized principals file.
type PrincipalLine struct {
	// Principal is the bare name the line authorizes.
	Principal string

	// Command is the forced command configured on the line.
	Command string

	// HasCommand reports whether the line carries a command option.
	HasCommand bool

	// PermitPTY reports whether the line carries permit-pty.
	PermitPTY bool

	// SourceAddresses are the networks the line restricts connections to. A
	// nil slice means the line imposes no source-address restriction.
	SourceAddresses []netip.Prefix
}

// MergedOptions is the post-authentication authorization result handed to the
// session layer of the gateway.
type MergedOptions struct {
	// ForceCommand is the command that must be executed for the session. It is
	// only valid when HasForceCommand is true.
	ForceCommand string

	// HasForceCommand reports whether a forced command applies.
	HasForceCommand bool

	// PermitPTY reports whether PTY allocation survives the merge.
	PermitPTY bool

	// SourceAddresses is the intersection of every source-address constraint.
	// A nil slice means no source-address restriction applies.
	SourceAddresses []netip.Prefix
}

// Merge combines a certificate with the principal lines its principals hit.
//
// clientAddr is the peer address of the authenticated connection; pass the
// zero netip.Addr to skip the explicit peer check (the structural
// intersection is still validated). Every certificate principal that occurs
// in lines contributes its options; matching is not limited to the first hit.
func Merge(cert *Certificate, lines []PrincipalLine, clientAddr netip.Addr) (*MergedOptions, error) {
	if cert == nil {
		return nil, errors.New("optmerge: nil certificate")
	}
	if len(cert.UnknownCriticalOptions) > 0 {
		return nil, newUnknownCriticalOptionError(cert.UnknownCriticalOptions[0])
	}

	matched := matchLines(cert.Principals, lines)
	if len(matched) == 0 {
		return nil, ErrNoMatchingPrincipal
	}

	command, hasCommand, err := mergeCommand(cert, matched)
	if err != nil {
		return nil, err
	}

	permitPTY, err := mergePTY(cert, matched)
	if err != nil {
		return nil, err
	}

	sources, err := mergeSourceAddresses(cert, matched)
	if err != nil {
		return nil, err
	}
	if clientAddr.IsValid() && len(sources) > 0 && !addrInPrefixes(clientAddr, sources) {
		return nil, ErrSourceAddressDenied
	}

	return &MergedOptions{
		ForceCommand:    command,
		HasForceCommand: hasCommand,
		PermitPTY:       permitPTY,
		SourceAddresses: sources,
	}, nil
}

// matchLines returns every principal line whose principal is one of the
// certificate principals. Duplicate certificate principals do not duplicate a
// matched line.
func matchLines(principals []string, lines []PrincipalLine) []PrincipalLine {
	wanted := make(map[string]struct{}, len(principals))
	for _, principal := range principals {
		wanted[principal] = struct{}{}
	}

	var matched []PrincipalLine
	for _, line := range lines {
		if _, ok := wanted[line.Principal]; ok {
			matched = append(matched, line)
		}
	}
	return matched
}

// mergeCommand enforces that every forced command introduced by either the
// certificate or a matched line is identical.
func mergeCommand(cert *Certificate, matched []PrincipalLine) (string, bool, error) {
	command := ""
	hasCommand := false
	if cert.HasForceCommand {
		command = cert.ForceCommand
		hasCommand = true
	}

	for _, line := range matched {
		if !line.HasCommand {
			continue
		}
		if !hasCommand {
			command = line.Command
			hasCommand = true
			continue
		}
		if line.Command != command {
			return "", false, ErrCommandConflict
		}
	}
	return command, hasCommand, nil
}

// mergePTY enforces that principal lines can only deny, never grant, PTY
// allocation. The effective value starts from the certificate and is revoked
// as soon as any matched line omits permit-pty.
func mergePTY(cert *Certificate, matched []PrincipalLine) (bool, error) {
	permitPTY := cert.PermitPTY
	for _, line := range matched {
		if line.PermitPTY && !cert.PermitPTY {
			return false, ErrPTYElevation
		}
		if !line.PermitPTY {
			permitPTY = false
		}
	}
	return permitPTY, nil
}

// mergeSourceAddresses intersects the source-address constraints carried by
// the certificate and every matched line. Parties without a constraint do not
// narrow the intersection.
func mergeSourceAddresses(cert *Certificate, matched []PrincipalLine) ([]netip.Prefix, error) {
	constraints := make([][]netip.Prefix, 0, len(matched)+1)
	if len(cert.SourceAddresses) > 0 {
		constraints = append(constraints, cert.SourceAddresses)
	}
	for _, line := range matched {
		if len(line.SourceAddresses) > 0 {
			constraints = append(constraints, line.SourceAddresses)
		}
	}

	intersection := intersectUnions(constraints)
	if len(constraints) > 0 && len(intersection) == 0 {
		return nil, ErrEmptySourceIntersection
	}
	return intersection, nil
}

// intersectUnions computes the intersection of multiple CIDR unions. It walks
// the cross product of the per-party prefixes and keeps one common network
// per combination whose members overlap.
func intersectUnions(constraints [][]netip.Prefix) []netip.Prefix {
	if len(constraints) == 0 {
		return nil
	}

	common := dedupPrefixes(constraints[0])
	for _, constraint := range constraints[1:] {
		var next []netip.Prefix
		for _, existing := range common {
			for _, candidate := range constraint {
				if prefix, ok := commonPrefix(existing, candidate); ok {
					next = append(next, prefix)
				}
			}
		}
		common = dedupPrefixes(next)
		if len(common) == 0 {
			return nil
		}
	}
	return common
}

// commonPrefix returns the overlapping network of two prefixes. Prefixes of
// the same IP version are either nested or disjoint, so the overlap, when it
// exists, is simply the more specific (longer-prefix) one.
func commonPrefix(a, b netip.Prefix) (netip.Prefix, bool) {
	addrA, addrB := a.Addr(), b.Addr()
	if addrA.Is4() != addrB.Is4() {
		return netip.Prefix{}, false
	}
	if a.Contains(addrB) {
		return b, true
	}
	if b.Contains(addrA) {
		return a, true
	}
	return netip.Prefix{}, false
}

func dedupPrefixes(prefixes []netip.Prefix) []netip.Prefix {
	seen := make(map[netip.Prefix]struct{}, len(prefixes))
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		masked := prefix.Masked()
		if _, ok := seen[masked]; ok {
			continue
		}
		seen[masked] = struct{}{}
		out = append(out, masked)
	}
	return out
}

func addrInPrefixes(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

type unknownOptionError struct {
	name string
}

func (e unknownOptionError) Error() string { return ErrUnknownOption.Error() + ": " + e.name }
func (e unknownOptionError) Unwrap() error { return ErrUnknownOption }

func newUnknownOptionError(name string) error {
	return unknownOptionError{name: name}
}

type unknownCriticalOptionError struct{ name string }

func (e unknownCriticalOptionError) Error() string {
	return ErrUnknownOption.Error() + ": certificate critical option " + e.name
}
func (e unknownCriticalOptionError) Unwrap() error { return ErrUnknownOption }

func newUnknownCriticalOptionError(name string) error {
	return unknownCriticalOptionError{name: name}
}
