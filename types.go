package netsec

import (
	"fmt"
	"strings"
)

const (
	MethodCreateSandbox   = "CreateSandbox"
	MethodCreateContainer = "CreateContainer"
	MethodStartContainer  = "StartContainer"
	MethodExecProcess     = "ExecProcess"
	MethodCopyFile        = "CopyFile"
)

// Request is a single request received by the kata-agent. Payload may be a
// Payload value, a JSON object, a JSON encoded string, or any struct whose
// exported fields describe the request. Unknown payload shapes are denied.
type Request struct {
	Method      string `json:"method"`
	ContainerID string `json:"container_id,omitempty"`
	Payload     any    `json:"payload,omitempty"`
}

// AgentRequest is an alias kept for hosts that name the intercepted request
// after the ttrpc peer.
type AgentRequest = Request
type PolicyRequest = Request

// Payload describes the runtime specification fields relevant to the policy.
// Fields whose shape differs between OCI and ttrpc implementations (Args, Env,
// Mounts, Capabilities and Devices) accept any and are normalized by the
// policy. This also lets callers pass a decoded JSON request directly.
type Payload struct {
	ContainerID string `json:"container_id,omitempty"`
	ID          string `json:"id,omitempty"`

	// Root is the host path of the container root filesystem. RootPath and
	// ContainerRoot are accepted spellings used by different Kata shims.
	Root          string `json:"root,omitempty"`
	RootPath      string `json:"root_path,omitempty"`
	ContainerRoot string `json:"container_root,omitempty"`
	Rootfs        string `json:"rootfs,omitempty"`

	// Path and Destination are container-relative (or host paths below Root)
	// used by CopyFile. Source is checked as well when it is the only path
	// supplied by the request.
	Path        string `json:"path,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	Target      string `json:"target,omitempty"`
	FilePath    string `json:"file_path,omitempty"`

	Args         any  `json:"args,omitempty"`
	Arguments    any  `json:"arguments,omitempty"`
	Cmd          any  `json:"cmd,omitempty"`
	Env          any  `json:"env,omitempty"`
	Environment  any  `json:"environment,omitempty"`
	Mounts       any  `json:"mounts,omitempty"`
	Privileged   bool `json:"privileged,omitempty"`
	Capabilities any  `json:"capabilities,omitempty"`
	Caps         any  `json:"caps,omitempty"`
	Devices      any  `json:"devices,omitempty"`

	// Process and RuntimeSpec permit the common OCI nesting
	// {"process": {"args": ...}, "linux": {...}} without flattening the
	// original request.
	Process     any `json:"process,omitempty"`
	RuntimeSpec any `json:"runtime_spec,omitempty"`
}

type (
	RequestPayload          = Payload
	CreateContainerRequest  = Payload
	CreateRequest           = Payload
	RuntimeSpec             = Payload
	ContainerRuntimePayload = Payload
)

// Mount is the subset of an OCI mount used by the field policy.
type Mount struct {
	Source      string   `json:"source,omitempty"`
	Destination string   `json:"destination,omitempty"`
	Type        string   `json:"type,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// Device is the subset of an OCI device used by the field policy.
type Device struct {
	Path     string `json:"path,omitempty"`
	Type     string `json:"type,omitempty"`
	Source   string `json:"source,omitempty"`
	Major    int64  `json:"major,omitempty"`
	Minor    int64  `json:"minor,omitempty"`
	FileMode uint32 `json:"file_mode,omitempty"`
	UID      uint32 `json:"uid,omitempty"`
	GID      uint32 `json:"gid,omitempty"`
}

type (
	MountSpec  = Mount
	DeviceSpec = Device
)

// Decision is the result of evaluating one request. A zero value denies.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Code    string `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Field   string `json:"field,omitempty"`
}

type Result = Decision
type PolicyDecision = Decision

func (d Decision) IsAllowed() bool {
	return d.Allowed
}

func (d Decision) IsDenied() bool {
	return !d.Allowed
}

func (d Decision) String() string {
	if d.Allowed {
		return "allowed"
	}
	if d.Code == "" {
		return "denied: " + d.Reason
	}
	return fmt.Sprintf("denied (%s): %s", d.Code, d.Reason)
}

func allow() Decision {
	return Decision{Allowed: true}
}

func deny(code, field, reason string) Decision {
	return Decision{Code: code, Field: field, Reason: reason}
}

// PolicyError is returned by the error-returning validation helpers.
type PolicyError struct {
	Code    string
	Method  string
	Field   string
	Message string
}

func (e *PolicyError) Error() string {
	var b strings.Builder
	if e.Method != "" {
		b.WriteString(e.Method)
		b.WriteString(": ")
	}
	if e.Field != "" {
		b.WriteString(e.Field)
		b.WriteString(": ")
	}
	b.WriteString(e.Message)
	if e.Code != "" {
		b.WriteString(" [")
		b.WriteString(e.Code)
		b.WriteString("]")
	}
	return b.String()
}

func (d Decision) Err(method string) error {
	if d.Allowed {
		return nil
	}
	return &PolicyError{
		Code:    d.Code,
		Method:  method,
		Field:   d.Field,
		Message: d.Reason,
	}
}
