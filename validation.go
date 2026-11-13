// Package netsec provides admission checks for build requests that may execute
// untrusted build content.
package netsec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Isolation identifies the isolation mode used for a build step.
type Isolation string

const (
	// IsolationOCI runs the build step with the configured OCI runtime.
	IsolationOCI Isolation = "oci"
	// IsolationRootless runs the build step without host root privileges.
	IsolationRootless Isolation = "rootless"
	// IsolationChroot uses a chroot without an OCI runtime boundary.
	IsolationChroot Isolation = "chroot"
)

// NamespaceOptions contains the requested namespace modes. Empty values
// represent the runtime default and are accepted.
type NamespaceOptions struct {
	Network string
	PID     string
	IPC     string
	UTS     string
	User    string
}

// Mount describes a host bind mount requested by a build.
//
// Destination is the preferred field. Target is an equivalent alias for
// callers that use Buildah's terminology; setting both to different paths is
// rejected.
type Mount struct {
	Source      string
	Destination string
	Target      string
	Options     []string
}

// UserMapping maps a container ID range to a host ID range. Size is the number
// of consecutive IDs in the mapping.
type UserMapping struct {
	ContainerID int64
	HostID      int64
	Size        int64
}

// BuildRequest is the runtime configuration presented to admission. Trusted
// must be set by the admission caller after authenticating and authorizing the
// requester; it must not be copied from client-controlled request data.
type BuildRequest struct {
	Trusted           bool
	Isolation         Isolation
	Namespaces        NamespaceOptions
	AddedCapabilities []string
	Mounts            []Mount
	Devices           []string
	UserMappings      []UserMapping
}

// Request is an alias for BuildRequest.
type Request = BuildRequest

// Namespaces is an alias for NamespaceOptions.
type Namespaces = NamespaceOptions

// IDMap is an alias for UserMapping.
type IDMap = UserMapping

// Policy contains the caller-registered values that a build request is allowed
// to use.
type Policy struct {
	// AllowedCapabilities is the small set of capabilities the caller permits.
	// Privileged capabilities are always rejected even if listed here.
	AllowedCapabilities []string

	// HostMountRoots lists the directories from which host bind mount sources
	// may be selected. Every source and its symlink-resolved path must remain
	// beneath one of these directories.
	HostMountRoots []string

	// AllowedDevices lists the host device paths the caller permits.
	AllowedDevices []string
}

// Violation describes one reason a build request was rejected.
type Violation struct {
	Field   string
	Code    string
	Message string
}

// ValidationError contains all violations found in a build request.
type ValidationError struct {
	Violations []Violation
}

// Error renders the violations in a stable, human-readable form.
func (e *ValidationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "build request rejected"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "build request rejected (%d violation(s))", len(e.Violations))
	for _, violation := range e.Violations {
		fmt.Fprintf(&b, "\n- %s [%s]: %s", violation.Field, violation.Code, violation.Message)
	}
	return b.String()
}

// HasCode reports whether the error contains a violation with code.
func (e *ValidationError) HasCode(code string) bool {
	if e == nil {
		return false
	}
	for _, violation := range e.Violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}

// Stable violation codes for admission logs and tests.
const (
	CodeIsolationNotAllowed     = "isolation_not_allowed"
	CodeIsolationUnknown        = "isolation_unknown"
	CodeNamespaceHost           = "namespace_host"
	CodeNamespaceInvalid        = "namespace_invalid"
	CodeCapabilityInvalid       = "capability_invalid"
	CodeCapabilityPrivileged    = "capability_privileged"
	CodeCapabilityNotAllowed    = "capability_not_allowed"
	CodeMountSourceRequired     = "mount_source_required"
	CodeMountSourceInvalid      = "mount_source_invalid"
	CodeMountSourceOutside      = "mount_source_outside"
	CodeMountTargetInvalid      = "mount_target_invalid"
	CodeMountTargetConflict     = "mount_target_conflict"
	CodeMountOptionsInvalid     = "mount_options_invalid"
	CodeMountRestrictions       = "mount_restrictions_missing"
	CodeMountOptionConflict     = "mount_option_conflict"
	CodeMountPropagationUnsafe  = "mount_propagation_unsafe"
	CodeDeviceInvalid           = "device_invalid"
	CodeDeviceNotAllowed        = "device_not_allowed"
	CodeUserMappingInvalid      = "user_mapping_invalid"
	CodeUserMappingMapsHostRoot = "user_mapping_maps_host_root"
	CodePolicyInvalid           = "policy_invalid"
)

var privilegedCapabilities = map[string]struct{}{
	"ALL":        {},
	"SYS_ADMIN":  {},
	"SYS_PTRACE": {},
	"SYS_MODULE": {},
}

var unsafeMountPropagation = map[string]struct{}{
	"shared":      {},
	"rshared":     {},
	"slave":       {},
	"rslave":      {},
	"unbindable":  {},
	"runbindable": {},
}

// Validate checks req against policy. It treats an unset Trusted field as an
// untrusted build, so callers fail closed unless they explicitly establish
// trust before calling admission.
func Validate(req BuildRequest, policy Policy) error {
	return policy.Validate(req)
}

// ValidateRequest is an alias for Validate.
func ValidateRequest(req BuildRequest, policy Policy) error {
	return Validate(req, policy)
}

// ValidateUntrusted validates req as an untrusted build regardless of the
// Trusted field.
func ValidateUntrusted(req BuildRequest, policy Policy) error {
	req.Trusted = false
	return Validate(req, policy)
}

// Validate checks req against p and returns all detected violations.
func (p Policy) Validate(req BuildRequest) error {
	var violations []Violation

	violations = append(violations, validateIsolation(req)...)
	violations = append(violations, validateNamespaces(req.Namespaces)...)
	violations = append(violations, validateCapabilities(req.AddedCapabilities, p)...)
	violations = append(violations, validateMounts(req.Mounts, p)...)
	violations = append(violations, validateDevices(req.Devices, p)...)
	violations = append(violations, validateUserMappings(req.UserMappings)...)

	if len(violations) == 0 {
		return nil
	}
	return &ValidationError{Violations: violations}
}

func validateIsolation(req BuildRequest) []Violation {
	mode := Isolation(strings.ToLower(strings.TrimSpace(string(req.Isolation))))

	switch mode {
	case IsolationOCI, IsolationRootless:
		return nil
	case IsolationChroot:
		if req.Trusted {
			return nil
		}
		return []Violation{{
			Field:   "isolation",
			Code:    CodeIsolationNotAllowed,
			Message: "untrusted builds may only use oci or rootless isolation",
		}}
	default:
		return []Violation{{
			Field:   "isolation",
			Code:    CodeIsolationUnknown,
			Message: fmt.Sprintf("unsupported isolation mode %q", req.Isolation),
		}}
	}
}

func validateNamespaces(namespaces NamespaceOptions) []Violation {
	var violations []Violation

	for _, item := range []struct {
		field string
		value string
	}{
		{field: "namespaces.network", value: namespaces.Network},
		{field: "namespaces.pid", value: namespaces.PID},
		{field: "namespaces.ipc", value: namespaces.IPC},
	} {
		mode := strings.ToLower(strings.TrimSpace(item.value))
		switch mode {
		case "", "host", "private", "none", "shareable":
		default:
			if isExternalNamespace(mode) {
				violations = append(violations, Violation{
					Field:   item.field,
					Code:    CodeNamespaceInvalid,
					Message: fmt.Sprintf("external namespace %q cannot be proven not to share the host namespace", item.value),
				})
				continue
			}
		}

		if mode == "host" {
			violations = append(violations, Violation{
				Field:   item.field,
				Code:    CodeNamespaceHost,
				Message: "sharing the host namespace is not allowed",
			})
		}
	}

	return violations
}

func isExternalNamespace(mode string) bool {
	return strings.HasPrefix(mode, "ns:") ||
		strings.HasPrefix(mode, "container:") ||
		strings.HasPrefix(mode, "path:")
}

func validateCapabilities(requested []string, policy Policy) []Violation {
	allowed := make(map[string]struct{}, len(policy.AllowedCapabilities))
	for _, capability := range policy.AllowedCapabilities {
		normalized := normalizeCapability(capability)
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}

	var violations []Violation
	for _, capability := range requested {
		normalized := normalizeCapability(capability)
		if normalized == "" {
			violations = append(violations, Violation{
				Field:   "added_capabilities",
				Code:    CodeCapabilityInvalid,
				Message: "capability name must not be empty",
			})
			continue
		}

		if _, privileged := privilegedCapabilities[normalized]; privileged {
			violations = append(violations, Violation{
				Field:   "added_capabilities",
				Code:    CodeCapabilityPrivileged,
				Message: fmt.Sprintf("privileged capability %q is never allowed", capability),
			})
			continue
		}

		if _, ok := allowed[normalized]; !ok {
			violations = append(violations, Violation{
				Field:   "added_capabilities",
				Code:    CodeCapabilityNotAllowed,
				Message: fmt.Sprintf("capability %q is not registered by the admission policy", capability),
			})
		}
	}
	return violations
}

func normalizeCapability(capability string) string {
	normalized := strings.ToUpper(strings.TrimSpace(capability))
	return strings.TrimPrefix(normalized, "CAP_")
}

type mountRoot struct {
	requested string
	resolved  string
}

func validateMounts(mounts []Mount, policy Policy) []Violation {
	if len(mounts) == 0 {
		return nil
	}

	roots, violations := resolveMountRoots(policy.HostMountRoots)
	for i, mount := range mounts {
		violations = append(violations, validateMount(fmt.Sprintf("mounts[%d]", i), mount, roots)...)
	}
	return violations
}

func resolveMountRoots(configured []string) ([]mountRoot, []Violation) {
	var (
		roots      []mountRoot
		violations []Violation
	)

	for i, configuredRoot := range configured {
		field := fmt.Sprintf("policy.host_mount_roots[%d]", i)
		if strings.TrimSpace(configuredRoot) == "" {
			violations = append(violations, policyViolation(field, "mount root must not be empty"))
			continue
		}
		if strings.ContainsRune(configuredRoot, 0) {
			violations = append(violations, policyViolation(field, "mount root contains a NUL byte"))
			continue
		}
		if !filepath.IsAbs(configuredRoot) {
			violations = append(violations, policyViolation(field, "mount root must be an absolute path"))
			continue
		}

		requested := filepath.Clean(configuredRoot)
		resolved, err := filepath.EvalSymlinks(requested)
		if err != nil {
			violations = append(violations, policyViolation(field, fmt.Sprintf("cannot resolve mount root: %v", err)))
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			violations = append(violations, policyViolation(field, fmt.Sprintf("cannot stat mount root: %v", err)))
			continue
		}
		if !info.IsDir() {
			violations = append(violations, policyViolation(field, "mount root is not a directory"))
			continue
		}

		roots = append(roots, mountRoot{
			requested: requested,
			resolved:  filepath.Clean(resolved),
		})
	}
	return roots, violations
}

func validateMount(field string, mount Mount, roots []mountRoot) []Violation {
	var violations []Violation

	target, okay := effectiveMountTarget(mount)
	if !okay {
		violations = append(violations, Violation{
			Field:   field + ".target",
			Code:    CodeMountTargetConflict,
			Message: "destination and target must refer to the same path when both are set",
		})
	}
	if target == "" {
		violations = append(violations, Violation{
			Field:   field + ".target",
			Code:    CodeMountTargetInvalid,
			Message: "mount target must not be empty",
		})
	} else if strings.ContainsRune(target, 0) {
		violations = append(violations, Violation{
			Field:   field + ".target",
			Code:    CodeMountTargetInvalid,
			Message: "mount target contains a NUL byte",
		})
	} else if !filepath.IsAbs(target) {
		violations = append(violations, Violation{
			Field:   field + ".target",
			Code:    CodeMountTargetInvalid,
			Message: "mount target must be an absolute container path",
		})
	} else if hasParentTraversal(target) {
		violations = append(violations, Violation{
			Field:   field + ".target",
			Code:    CodeMountTargetInvalid,
			Message: "mount target must not contain parent-directory traversal",
		})
	}

	violations = append(violations, validateMountSource(field+".source", mount.Source, roots)...)
	violations = append(violations, validateMountOptions(field+".options", mount.Options)...)

	return violations
}

func effectiveMountTarget(mount Mount) (string, bool) {
	switch {
	case mount.Destination != "" && mount.Target != "":
		return mount.Destination, mount.Destination == mount.Target
	case mount.Destination != "":
		return mount.Destination, true
	default:
		return mount.Target, true
	}
}

func hasParentTraversal(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func validateMountSource(field, source string, roots []mountRoot) []Violation {
	if source == "" {
		return []Violation{{
			Field:   field,
			Code:    CodeMountSourceRequired,
			Message: "host mount source must not be empty",
		}}
	}
	if strings.ContainsRune(source, 0) {
		return []Violation{{
			Field:   field,
			Code:    CodeMountSourceInvalid,
			Message: "host mount source contains a NUL byte",
		}}
	}
	if !filepath.IsAbs(source) {
		return []Violation{{
			Field:   field,
			Code:    CodeMountSourceInvalid,
			Message: "host mount source must be an absolute path",
		}}
	}

	requested := filepath.Clean(source)
	requestedAllowed := false
	for _, root := range roots {
		if isWithin(root.requested, requested) {
			requestedAllowed = true
			break
		}
	}
	if !requestedAllowed {
		return []Violation{{
			Field:   field,
			Code:    CodeMountSourceOutside,
			Message: "host mount source is outside the registered mount roots",
		}}
	}

	resolved, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return []Violation{{
			Field:   field,
			Code:    CodeMountSourceInvalid,
			Message: fmt.Sprintf("cannot resolve host mount source: %v", err),
		}}
	}
	resolved = filepath.Clean(resolved)

	for _, root := range roots {
		if isWithin(root.resolved, resolved) {
			return nil
		}
	}
	return []Violation{{
		Field:   field,
		Code:    CodeMountSourceOutside,
		Message: "host mount source resolves outside the registered mount roots",
	}}
}

func validateMountOptions(field string, options []string) []Violation {
	var (
		violations []Violation
		readOnly   bool
		noSUID     bool
		noDev      bool
	)

	for _, group := range options {
		if group == "" {
			violations = append(violations, Violation{
				Field:   field,
				Code:    CodeMountOptionsInvalid,
				Message: "mount options must not contain an empty option",
			})
			continue
		}

		for _, rawOption := range strings.Split(group, ",") {
			option := strings.ToLower(strings.TrimSpace(rawOption))
			if option == "" {
				violations = append(violations, Violation{
					Field:   field,
					Code:    CodeMountOptionsInvalid,
					Message: "mount options must not contain an empty option",
				})
				continue
			}

			switch option {
			case "ro":
				readOnly = true
			case "rw":
				violations = append(violations, Violation{
					Field:   field,
					Code:    CodeMountOptionConflict,
					Message: "read-write mount option conflicts with the required ro option",
				})
			case "nosuid":
				noSUID = true
			case "suid":
				violations = append(violations, Violation{
					Field:   field,
					Code:    CodeMountOptionConflict,
					Message: "suid mount option conflicts with the required nosuid option",
				})
			case "nodev":
				noDev = true
			case "dev":
				violations = append(violations, Violation{
					Field:   field,
					Code:    CodeMountOptionConflict,
					Message: "dev mount option conflicts with the required nodev option",
				})
			default:
				if _, unsafe := unsafeMountPropagation[option]; unsafe {
					violations = append(violations, Violation{
						Field:   field,
						Code:    CodeMountPropagationUnsafe,
						Message: fmt.Sprintf("mount propagation option %q is not allowed", option),
					})
				}
			}
		}
	}

	if !readOnly || !noSUID || !noDev {
		violations = append(violations, Violation{
			Field:   field,
			Code:    CodeMountRestrictions,
			Message: "host mounts must include ro, nosuid and nodev options",
		})
	}
	return violations
}

func validateDevices(requested []string, policy Policy) []Violation {
	if len(requested) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(policy.AllowedDevices))
	var violations []Violation
	for i, device := range policy.AllowedDevices {
		normalized, err := normalizeDevicePath(device)
		if err != nil {
			violations = append(violations, policyViolation(
				fmt.Sprintf("policy.allowed_devices[%d]", i),
				err.Error(),
			))
			continue
		}
		allowed[normalized] = struct{}{}
	}

	for i, device := range requested {
		field := fmt.Sprintf("devices[%d]", i)
		normalized, err := normalizeDevicePath(device)
		if err != nil {
			violations = append(violations, Violation{
				Field:   field,
				Code:    CodeDeviceInvalid,
				Message: err.Error(),
			})
			continue
		}

		if _, ok := allowed[normalized]; !ok {
			violations = append(violations, Violation{
				Field:   field,
				Code:    CodeDeviceNotAllowed,
				Message: fmt.Sprintf("device %q is not registered by the admission policy", device),
			})
		}
	}
	return violations
}

func normalizeDevicePath(device string) (string, error) {
	if strings.TrimSpace(device) == "" {
		return "", fmt.Errorf("device path must not be empty")
	}
	if strings.ContainsRune(device, 0) {
		return "", fmt.Errorf("device path contains a NUL byte")
	}
	if !filepath.IsAbs(device) {
		return "", fmt.Errorf("device path %q must be absolute", device)
	}
	return filepath.Clean(device), nil
}

func validateUserMappings(mappings []UserMapping) []Violation {
	var violations []Violation
	for i, mapping := range mappings {
		field := fmt.Sprintf("user_mappings[%d]", i)

		if mapping.Size <= 0 {
			violations = append(violations, Violation{
				Field:   field,
				Code:    CodeUserMappingInvalid,
				Message: "user mapping size must be greater than zero",
			})
			continue
		}
		if !validIDRange(mapping.ContainerID, mapping.Size) {
			violations = append(violations, Violation{
				Field:   field,
				Code:    CodeUserMappingInvalid,
				Message: "container ID range is invalid",
			})
		}
		if !validIDRange(mapping.HostID, mapping.Size) {
			violations = append(violations, Violation{
				Field:   field,
				Code:    CodeUserMappingInvalid,
				Message: "host ID range is invalid",
			})
		}
		if mapping.HostID == 0 {
			violations = append(violations, Violation{
				Field:   field,
				Code:    CodeUserMappingMapsHostRoot,
				Message: "host root (ID 0) must not be mapped into the build",
			})
		}
	}
	return violations
}

func validIDRange(start, size int64) bool {
	const maxID = int64(1<<32 - 1)

	if start < 0 || size <= 0 || size > maxID+1 {
		return false
	}
	return start <= maxID && size-1 <= maxID-start
}

func isWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	if relative == "." {
		return true
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func policyViolation(field, message string) Violation {
	return Violation{
		Field:   field,
		Code:    CodePolicyInvalid,
		Message: message,
	}
}
