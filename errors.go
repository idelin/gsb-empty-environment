package netsec

import (
	"encoding/json"
	"fmt"
)

// Code is a stable reason code attached to events and violations.
type Code string

const (
	CodeInvalidAction        Code = "invalid-action"
	CodeInvalidPath          Code = "invalid-path"
	CodeAbsolutePath         Code = "absolute-path"
	CodeDotSegment           Code = "dot-segment"
	CodeSymlinkParent        Code = "symlink-parent"
	CodeSymlinkTarget        Code = "symlink-target"
	CodeMissingParent        Code = "missing-parent"
	CodeNotDirectory         Code = "not-directory"
	CodeNotFile              Code = "not-file"
	CodeInvalidDestination   Code = "invalid-destination"
	CodeUndeclaredAction     Code = "undeclared-action"
	CodePolicyDenied         Code = "policy-denied"
	CodeUnregisteredTool     Code = "unregistered-tool"
	CodeUnknownEndpoint      Code = "unregistered-endpoint"
	CodeUnregisteredEndpoint      = CodeUnknownEndpoint
	CodeEnvironmentAbsent    Code = "environment-unavailable"
	CodeSpawnEscalation      Code = "spawn-escalation"
	CodeExecutionAborted     Code = "execution-aborted"
	CodeCallbackFailure      Code = "callback-failure"
	CodeIOFailure            Code = "io-failure"
	CodePublishConflict      Code = "publish-conflict"
	CodeDependencyRevoked    Code = "dependency-revoked"
)

// Violation is a package-to-action security or execution finding. Process is
// the callback lineage, with the root script first. Dependency evidence for
// revoked packages is reported separately in Revocation.
type Violation struct {
	Code    Code     `json:"code"`
	Package Digest   `json:"package"`
	Script  string   `json:"script,omitempty"`
	Process []string `json:"process,omitempty"`
	Action  Action   `json:"action"`
	Message string   `json:"message,omitempty"`
}

func (v Violation) Error() string {
	if v.Action.Kind == "" {
		return fmt.Sprintf("netsec: %s in package %s script %s: %s", v.Code, v.Package, v.Script, v.Message)
	}
	return fmt.Sprintf("netsec: %s in package %s script %s at %s: %s", v.Code, v.Package, v.Script, v.Action, v.Message)
}

// ViolationError is returned by Execution methods when an action is refused.
type ViolationError struct {
	Violation Violation
}

func (e *ViolationError) Error() string { return e.Violation.Error() }

// Revocation records why a package output was withdrawn because an ancestor
// failed or was rejected during publication. Chain is dependency ordered and
// runs from the failing package to the revoked package, inclusive.
type Revocation struct {
	Code    Code     `json:"code"`
	Package Digest   `json:"package"`
	Cause   Digest   `json:"cause"`
	Chain   []Digest `json:"chain"`
}

func (r Revocation) Error() string {
	return fmt.Sprintf("netsec: package %s revoked because %s was rejected", r.Package, r.Cause)
}

// GraphError reports a structurally invalid package graph.
type GraphError struct {
	Code    string `json:"code"`
	Package Digest `json:"package,omitempty"`
	Message string `json:"message"`
}

func (e *GraphError) Error() string {
	if e.Package != "" {
		return fmt.Sprintf("netsec: graph %s in package %s: %s", e.Code, e.Package, e.Message)
	}
	return fmt.Sprintf("netsec: graph %s: %s", e.Code, e.Message)
}

// MarshalJSON supplies stable field shape for evidence consumed by tooling.
func (v Violation) MarshalJSON() ([]byte, error) {
	type alias Violation
	return json.Marshal(alias(v))
}
