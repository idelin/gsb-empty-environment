package netsec

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type containerState struct {
	id   string
	args []string
	caps []string
	root string
}

// Policy evaluates kata-agent requests and retains the runtime facts that a
// successful CreateContainer established. It is safe for concurrent use.
type Policy struct {
	mu         sync.RWMutex
	rules      Rules
	containers map[string]*containerState
}

func NewPolicy(rules Rules) *Policy {
	return &Policy{
		rules:      cloneRules(rules),
		containers: make(map[string]*containerState),
	}
}

func New(rules Rules) *Policy {
	return NewPolicy(rules)
}

func NewPolicyWithRules(rules Rules) *Policy {
	return NewPolicy(rules)
}

func (p *Policy) Rules() Rules {
	return cloneRules(p.rules)
}

func (p *Policy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.containers = make(map[string]*containerState)
}

// RegisterContainer preloads a container that was created before this policy
// instance was constructed, for example after a shim restart.
func (p *Policy) RegisterContainer(id string, args, capabilities []string, root string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.containers == nil {
		p.containers = make(map[string]*containerState)
	}
	p.containers[id] = &containerState{
		id:   id,
		args: append([]string{}, args...),
		caps: normalizeCapabilities(capabilities),
		root: root,
	}
}

func (p *Policy) Register(id string, args, capabilities []string, root string) {
	p.RegisterContainer(id, args, capabilities, root)
}

func (p *Policy) Evaluate(req Request) Decision {
	return p.evaluate(req.Method, req.ContainerID, req.Payload)
}

func (p *Policy) Check(method string, payload any) Decision {
	return p.evaluate(method, "", payload)
}

func (p *Policy) CheckRequest(req Request) Decision {
	return p.Evaluate(req)
}

func (p *Policy) Handle(method string, payload any) Decision {
	return p.Check(method, payload)
}

func (p *Policy) Authorize(method string, payload any) Decision {
	return p.Check(method, payload)
}

func (p *Policy) Allow(method string, payload any) bool {
	return p.Check(method, payload).Allowed
}

func (p *Policy) Validate(method string, payload any) error {
	return p.Check(method, payload).Err(method)
}

func (p *Policy) Decide(method string, payload any) (bool, string) {
	decision := p.Check(method, payload)
	return decision.Allowed, decision.Reason
}

func (p *Policy) DecideRequest(req Request) (bool, string) {
	decision := p.Evaluate(req)
	return decision.Allowed, decision.Reason
}

func (p *Policy) ValidateRequest(req Request) error {
	return p.Evaluate(req).Err(req.Method)
}

func (p *Policy) evaluate(method, containerID string, payload any) Decision {
	method = strings.TrimSpace(method)
	if method == "" {
		return deny("method_denied", "method", "request method is empty")
	}
	_, registered := p.rules.methodRule(method)
	if !registered {
		return deny("method_denied", "method", fmt.Sprintf("method %q is not approved", method))
	}

	normalized, err := normalizePayload(payload)
	if err != nil {
		return deny("payload_invalid", "payload", err.Error())
	}
	if containerID != "" && normalized.containerID == "" {
		normalized.containerID = containerID
	}

	if normalized.privileged {
		return deny("privileged_denied", "privileged", "privileged execution is not permitted")
	}
	forbiddenAny := append(append([]string{}, p.rules.ForbiddenKeysAny...), p.rules.Forbidden...)
	if len(forbiddenAny) > 0 && scanObjectKeys(normalized.raw, forbiddenAny, 0) {
		return deny("forbidden_key", "payload", "payload contains a forbidden key")
	}
	if decision := p.checkFields(method, normalized); !decision.Allowed {
		return decision
	}

	switch methodKey(method) {
	case methodKey(MethodCreateContainer):
		p.registerContainer(normalized)
		return allow()
	case methodKey(MethodExecProcess):
		return p.checkExecProcess(normalized)
	case methodKey(MethodCopyFile):
		return p.checkCopyFile(normalized)
	default:
		return allow()
	}
}

func (p *Policy) registerContainer(payload *normalizedPayload) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.containers == nil {
		p.containers = make(map[string]*containerState)
	}
	id := payload.containerID
	p.containers[id] = &containerState{
		id:   id,
		args: append([]string{}, payload.args...),
		caps: normalizeCapabilities(payload.capabilities),
		root: payload.root,
	}
}

func (p *Policy) stateFor(id string) (*containerState, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if state, ok := p.containers[id]; ok {
		return cloneState(state), true
	}
	if id == "" && len(p.containers) == 1 {
		for _, state := range p.containers {
			return cloneState(state), true
		}
	}
	return nil, false
}

func cloneState(state *containerState) *containerState {
	return &containerState{
		id:   state.id,
		args: append([]string{}, state.args...),
		caps: append([]string{}, state.caps...),
		root: state.root,
	}
}

func (p *Policy) checkExecProcess(payload *normalizedPayload) Decision {
	state, ok := p.stateFor(payload.containerID)
	if !ok {
		return deny("container_unknown", "container_id", "no approved CreateContainer registration exists")
	}
	if len(payload.args) == 0 {
		return deny("exec_args_denied", "args", "ExecProcess requires explicit arguments")
	}

	if !slices.Equal(payload.args, state.args) {
		return deny("exec_args_denied", "args", "ExecProcess arguments do not match any approved creation argument list")
	}

	if !capabilitySubset(state.caps, payload.capabilities) {
		return deny("capability_denied", "capabilities", "execution capabilities exceed the capabilities declared at creation")
	}
	return allow()
}

func (p *Policy) checkCopyFile(payload *normalizedPayload) Decision {
	if len(payload.paths) == 0 {
		return deny("copy_path_denied", "path", "CopyFile requires a path")
	}

	root := payload.root
	if state, ok := p.stateFor(payload.containerID); ok && state.root != "" {
		root = state.root
	}
	if root == "" {
		return deny("copy_path_denied", "root", "CopyFile requires an approved container root")
	}

	for _, claim := range payload.paths {
		rule := p.rules.fieldRule(MethodCopyFile, claim.field, "path", "copy_file_path")
		if rule.constrained() && !rule.Matches(claim.value) {
			return deny("field_denied", claim.field, "path does not match the approved field rule")
		}
		for _, forbidden := range rule.forbiddenKeys() {
			if keyMatches(forbidden, claim.field) || keyMatches(forbidden, filepath.Base(claim.value)) {
				return deny("forbidden_key", claim.field, "path field is forbidden")
			}
		}
		if _, err := ResolveWithinRoot(root, claim.value); err != nil {
			return deny("copy_path_denied", claim.field, err.Error())
		}
	}
	return allow()
}

func (p *Policy) checkFields(method string, payload *normalizedPayload) Decision {
	if decision := checkValues(p.rules.fieldRule(method, "args", "arguments", "process.args", "process.arguments"), "args", payload.args, false); !decision.Allowed {
		return decision
	}
	if decision := p.checkEnvField(method, payload); !decision.Allowed {
		return decision
	}
	if decision := p.checkMountFields(method, payload); !decision.Allowed {
		return decision
	}
	if decision := p.checkDeviceFields(method, payload); !decision.Allowed {
		return decision
	}
	if decision := checkValues(p.rules.fieldRule(method, "capabilities", "caps", "process.capabilities", "linux.capabilities"), "capabilities", payload.capabilities, false); !decision.Allowed {
		return decision
	}
	if decision := checkValues(p.rules.fieldRule(method, "container_id", "id"), "container_id", nonEmpty(payload.containerID), false); !decision.Allowed {
		return decision
	}
	if decision := checkValues(p.rules.fieldRule(method, "root", "root_path", "container_root"), "root", nonEmpty(payload.root), false); !decision.Allowed {
		return decision
	}
	if payload.privilegedSeen {
		if decision := checkValues(p.rules.fieldRule(method, "privileged", "process.privileged"), "privileged", []string{boolString(payload.privileged)}, false); !decision.Allowed {
			return decision
		}
	}
	return allow()
}

func (p *Policy) checkEnvField(method string, payload *normalizedPayload) Decision {
	rule := p.rules.fieldRule(method, "env", "environment", "process.env", "process.environment")
	if rule.constrained() && len(payload.env) == 0 {
		return deny("field_required", "env", "environment is not present")
	}
	for _, variable := range payload.env {
		if matchesAnyKey(append(append([]string{}, p.rules.ForbiddenKeysAny...), p.rules.Forbidden...), variable.key) {
			return deny("forbidden_key", "env", fmt.Sprintf("environment key %q is forbidden", variable.key))
		}
		if rule.keywordForbidden(variable.key) {
			return deny("forbidden_key", "env", fmt.Sprintf("environment key %q is forbidden", variable.key))
		}
		if rule.constrained() && !rule.Matches(variable.full) && !rule.Matches(variable.key) {
			return deny("field_denied", "env", fmt.Sprintf("environment key %q is not approved", variable.key))
		}
	}

	keyRule := p.rules.fieldRule(method, "env.key", "environment.key")
	if decision := checkValues(keyRule, "env.key", envKeys(payload.env), false); !decision.Allowed {
		return decision
	}
	valueRule := p.rules.fieldRule(method, "env.value", "environment.value")
	if decision := checkValues(valueRule, "env.value", envValues(payload.env), false); !decision.Allowed {
		return decision
	}
	return allow()
}

func (p *Policy) checkMountFields(method string, payload *normalizedPayload) Decision {
	rule := p.rules.fieldRule(method, "mounts", "rootfs.mounts", "spec.mounts")
	if rule.constrained() && len(payload.mounts) == 0 {
		return deny("field_required", "mounts", "mounts are not present")
	}
	for _, mount := range payload.mounts {
		if rule.constrained() && !mountMatches(rule, mount) {
			return deny("field_denied", "mounts", "mount does not match the approved field rule")
		}
		for _, forbidden := range rule.forbiddenKeys() {
			if scanObjectKeys(mount.raw, []string{forbidden}, 0) {
				return deny("forbidden_key", "mounts", "mount contains a forbidden key")
			}
		}
	}
	for _, field := range []string{"source", "destination", "type", "options"} {
		subRule := p.rules.fieldRule(method, "mounts."+field, "mount."+field)
		if decision := checkValues(subRule, "mounts."+field, mountFieldValues(payload.mounts, field), false); !decision.Allowed {
			return decision
		}
	}
	return allow()
}

func (p *Policy) checkDeviceFields(method string, payload *normalizedPayload) Decision {
	rule := p.rules.fieldRule(method, "devices", "linux.devices", "resources.devices")
	if rule.constrained() && len(payload.devices) == 0 {
		return deny("field_required", "devices", "devices are not present")
	}
	for _, device := range payload.devices {
		if rule.constrained() && !deviceMatches(rule, device) {
			return deny("field_denied", "devices", "device does not match the approved field rule")
		}
		for _, forbidden := range rule.forbiddenKeys() {
			if scanObjectKeys(device.raw, []string{forbidden}, 0) {
				return deny("forbidden_key", "devices", "device contains a forbidden key")
			}
		}
	}
	for _, field := range []string{"path", "type", "source"} {
		subRule := p.rules.fieldRule(method, "devices."+field, "device."+field)
		if decision := checkValues(subRule, "devices."+field, deviceFieldValues(payload.devices, field), false); !decision.Allowed {
			return decision
		}
	}
	return allow()
}

func checkValues(rule FieldRule, field string, values []string, allowEmpty bool) Decision {
	if rule.constrained() && len(values) == 0 && !allowEmpty {
		return deny("field_required", field, "required field is not present")
	}
	for _, value := range values {
		for _, forbidden := range rule.forbiddenKeys() {
			if keyMatches(forbidden, value) || keyMatches(forbidden, filepath.Base(value)) {
				return deny("forbidden_key", field, fmt.Sprintf("value %q is forbidden", value))
			}
		}
		if !rule.Matches(value) {
			return deny("field_denied", field, fmt.Sprintf("value %q is not approved", value))
		}
	}
	return allow()
}

func mountMatches(rule FieldRule, mount mountValue) bool {
	values := mount.values()
	if len(values) == 0 {
		return false
	}
	if rule.Matches(strings.Join(values, "\x00")) {
		return true
	}
	for _, value := range values {
		if !rule.Matches(value) {
			if mount.raw != nil {
				if encoded, err := json.Marshal(mount.raw); err == nil && rule.Matches(string(encoded)) {
					return true
				}
			}
			return false
		}
	}
	return true
}

func deviceMatches(rule FieldRule, device deviceValue) bool {
	values := device.values()
	if len(values) == 0 {
		return false
	}
	if rule.Matches(strings.Join(values, "\x00")) {
		return true
	}
	for _, value := range values {
		if !rule.Matches(value) {
			if device.raw != nil {
				if encoded, err := json.Marshal(device.raw); err == nil && rule.Matches(string(encoded)) {
					return true
				}
			}
			return false
		}
	}
	return true
}

func mountFieldValues(mounts []mountValue, field string) []string {
	var out []string
	for _, mount := range mounts {
		switch field {
		case "source":
			out = appendNonEmpty(out, mount.source)
		case "destination":
			out = appendNonEmpty(out, mount.destination)
		case "type":
			out = appendNonEmpty(out, mount.mountType)
		case "options":
			out = append(out, mount.options...)
		}
	}
	return out
}

func deviceFieldValues(devices []deviceValue, field string) []string {
	var out []string
	for _, device := range devices {
		switch field {
		case "path":
			out = appendNonEmpty(out, device.path)
		case "type":
			out = appendNonEmpty(out, device.deviceType)
		case "source":
			out = appendNonEmpty(out, device.source)
		}
	}
	return out
}

func appendNonEmpty(values []string, value string) []string {
	if value != "" {
		return append(values, value)
	}
	return values
}

func envKeys(env []envVar) []string {
	out := make([]string, 0, len(env))
	for _, variable := range env {
		out = append(out, variable.key)
	}
	return out
}

func envValues(env []envVar) []string {
	out := make([]string, 0, len(env))
	for _, variable := range env {
		out = append(out, variable.value)
	}
	return out
}

func nonEmpty(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func normalizeCapabilities(capabilities []string) []string {
	out := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		canonical := strings.ToUpper(strings.TrimSpace(capability))
		canonical = strings.TrimPrefix(canonical, "CAP_")
		if canonical == "" {
			continue
		}
		if !slices.Contains(out, canonical) {
			out = append(out, canonical)
		}
	}
	slices.Sort(out)
	return out
}

func capabilitySubset(declared, requested []string) bool {
	declared = normalizeCapabilities(declared)
	requested = normalizeCapabilities(requested)
	if len(requested) == 0 {
		return true
	}
	if slices.Contains(declared, "ALL") {
		return true
	}
	for _, capability := range requested {
		if capability == "ALL" || !slices.Contains(declared, capability) {
			return false
		}
	}
	return true
}

func Evaluate(method string, payload any, rules Rules) Decision {
	return NewPolicy(rules).Check(method, payload)
}

func EvaluateRequest(req Request, rules Rules) Decision {
	return NewPolicy(rules).Evaluate(req)
}

func CheckRequest(req Request, rules Rules) Decision {
	return EvaluateRequest(req, rules)
}

func Check(method string, payload any, rules Rules) Decision {
	return Evaluate(method, payload, rules)
}

func Authorize(method string, payload any, rules Rules) Decision {
	return Evaluate(method, payload, rules)
}

func Allow(method string, payload any, rules Rules) bool {
	return Evaluate(method, payload, rules).Allowed
}

func Validate(method string, payload any, rules Rules) error {
	return Evaluate(method, payload, rules).Err(method)
}

func Decide(method string, payload any, rules Rules) (bool, string) {
	decision := Evaluate(method, payload, rules)
	return decision.Allowed, decision.Reason
}

func ValidateRequest(req Request, rules Rules) error {
	return EvaluateRequest(req, rules).Err(req.Method)
}
