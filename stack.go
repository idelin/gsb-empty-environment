package netsec

// StackFrame is one entry in a call stack. Frames are ordered from the
// permission checkpoint outward: frames[0] is the current frame and later
// entries are its callers.
type StackFrame struct {
	Domain     ProtectionDomain
	Privileged bool

	// LimitedDomain is only consulted when Privileged is true. When present,
	// it is intersected with the domains up to and including this frame.
	LimitedDomain *ProtectionDomain
}

// Frame constructs an ordinary, non-privileged stack frame.
func Frame(domain ProtectionDomain) StackFrame {
	return StackFrame{Domain: domain}
}

// DoPrivileged constructs a doPrivileged frame. Domains below this frame
// (older callers) do not participate in the permission check.
func DoPrivileged(domain ProtectionDomain) StackFrame {
	return StackFrame{Domain: domain, Privileged: true}
}

// DoPrivilegedLimited constructs a doPrivileged frame with an additional
// limiting domain. The limited domain is intersected with the domains up to
// and including this frame.
func DoPrivilegedLimited(domain, limited ProtectionDomain) StackFrame {
	return StackFrame{
		Domain:        domain,
		Privileged:    true,
		LimitedDomain: &limited,
	}
}

// Check reports whether the call stack grants request. The effective
// permission is the intersection of the protection domains from frames[0]
// through the first doPrivileged frame, inclusive. Callers below that frame
// are ignored. A limited doPrivileged domain is also intersected.
//
// Check fails closed: an empty stack, malformed request, empty domain, or
// malformed grant never grants access.
func Check(frames []StackFrame, request FilePermission) bool {
	normalized, err := normalizeRequest(request)
	if err != nil {
		return false
	}

	domains := effectiveDomains(frames)
	if len(domains) == 0 {
		return false
	}
	for i := range domains {
		if !domains[i].allowsNormalized(normalized) {
			return false
		}
	}
	return true
}

// CheckPath is a convenience wrapper for a permission checkpoint.
func CheckPath(frames []StackFrame, filePath string, actions ...Action) bool {
	return Check(frames, FilePermission{Path: filePath, Actions: actions})
}

// CanRead reports whether the call stack grants read access to filePath.
func CanRead(frames []StackFrame, filePath string) bool {
	return CheckPath(frames, filePath, Read)
}

// CanWrite reports whether the call stack grants write access to filePath.
func CanWrite(frames []StackFrame, filePath string) bool {
	return CheckPath(frames, filePath, Write)
}

func effectiveDomains(frames []StackFrame) []*ProtectionDomain {
	boundary := len(frames)
	for i := range frames {
		if frames[i].Privileged {
			boundary = i + 1
			break
		}
	}
	if boundary == 0 {
		return nil
	}

	domains := make([]*ProtectionDomain, 0, boundary+1)
	for i := 0; i < boundary; i++ {
		domains = append(domains, &frames[i].Domain)
	}

	last := boundary - 1
	if frames[last].Privileged && frames[last].LimitedDomain != nil {
		domains = append(domains, frames[last].LimitedDomain)
	}
	return domains
}
