package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"
)

// Environment variable names used for tunnel hook metadata.
const (
	EnvEvent            = "IPSEC_EVENT"
	EnvPeerIdentity     = "IPSEC_PEER_IDENTITY"
	EnvVirtualIP        = "IPSEC_VIRTUAL_IP"
	EnvTrafficSelectors = "IPSEC_TRAFFIC_SELECTORS"
)

// Phase identifies the tunnel lifecycle event being delivered to a hook.
type Phase string

const (
	// PhaseEstablished is delivered after a tunnel is established.
	PhaseEstablished Phase = "established"
	// PhaseTerminated is delivered after a tunnel is terminated.
	PhaseTerminated Phase = "terminated"
)

var (
	// ErrInvalidEvent indicates that an event is missing required metadata or
	// uses an unsupported lifecycle phase.
	ErrInvalidEvent = errors.New("invalid tunnel event")
	// ErrInvalidHook indicates that a hook path or argument cannot be passed
	// safely to the operating system.
	ErrInvalidHook = errors.New("invalid hook")
)

// Event contains the peer-controlled and tunnel-specific values supplied to a
// lifecycle hook.
//
// PeerIdentity may be an FQDN or an RFC 4514 distinguished name. It is never
// interpreted by a shell: InvokeHook passes the normalized value as one
// process argument and as one environment variable.
type Event struct {
	Phase            Phase
	PeerIdentity     string
	VirtualIP        string
	TrafficSelectors []string
}

// TunnelEvent is an alias retained for call sites that prefer an explicit
// type name.
type TunnelEvent = Event

// Validate checks the event without starting a hook process.
func (e Event) Validate() error {
	_, _, err := e.prepare()
	return err
}

// Args returns the event portion of the process argument vector. The hook
// path is not included.
//
// The layout is:
//   - arg 0: lifecycle phase
//   - arg 1: normalized peer identity
//   - arg 2: virtual IP
//   - arg 3 and later: traffic selectors, one argument per selector
func (e Event) Args() ([]string, error) {
	args, _, err := e.prepare()
	if err != nil {
		return nil, err
	}
	return args, nil
}

// Env returns the event's environment entries in "NAME=value" form.
// TrafficSelectors is encoded as a JSON array so selector boundaries remain
// unambiguous.
func (e Event) Env() ([]string, error) {
	_, env, err := e.prepare()
	if err != nil {
		return nil, err
	}
	return env, nil
}

func (e Event) prepare() (args, env []string, err error) {
	if e.Phase != PhaseEstablished && e.Phase != PhaseTerminated {
		return nil, nil, fmt.Errorf("%w: unsupported phase %q", ErrInvalidEvent, e.Phase)
	}

	identity, err := NormalizePeerIdentity(e.PeerIdentity)
	if err != nil {
		return nil, nil, err
	}
	if err := validateProcessValue("virtual IP", e.VirtualIP); err != nil {
		return nil, nil, err
	}
	for i, selector := range e.TrafficSelectors {
		if err := validateProcessValue(fmt.Sprintf("traffic selector %d", i), selector); err != nil {
			return nil, nil, err
		}
	}

	selectors := e.TrafficSelectors
	if selectors == nil {
		selectors = []string{}
	}
	selectorsJSON, err := json.Marshal(selectors)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: encode traffic selectors: %v", ErrInvalidEvent, err)
	}

	args = []string{string(e.Phase), identity, e.VirtualIP}
	args = append(args, selectors...)
	env = []string{
		EnvEvent + "=" + string(e.Phase),
		EnvPeerIdentity + "=" + identity,
		EnvVirtualIP + "=" + e.VirtualIP,
		EnvTrafficSelectors + "=" + string(selectorsJSON),
	}
	return args, env, nil
}

func validateProcessValue(name, value string) error {
	if strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("%w: %s contains NUL", ErrInvalidEvent, name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidEvent, name)
	}
	return nil
}

// HookError describes a hook that could not be started or exited non-zero.
type HookError struct {
	Path     string
	ExitCode int
	Output   string
	Err      error
}

func (e *HookError) Error() string {
	if e.ExitCode >= 0 {
		return fmt.Sprintf("hook %q exited with code %d: %v", e.Path, e.ExitCode, e.Err)
	}
	return fmt.Sprintf("hook %q failed: %v", e.Path, e.Err)
}

// Unwrap exposes the underlying process or context error.
func (e *HookError) Unwrap() error {
	return e.Err
}

// InvokeHook validates an event and executes hookPath with the event values as
// separate process arguments and environment variables.
//
// No shell is involved: hookPath is executed directly and peer-controlled
// values are never concatenated into a command string.
func InvokeHook(ctx context.Context, hookPath string, event Event) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidHook)
	}
	if hookPath == "" {
		return fmt.Errorf("%w: empty path", ErrInvalidHook)
	}
	if err := validateProcessValue("hook path", hookPath); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidHook, err)
	}

	args, env, err := event.prepare()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, hookPath, args...)
	cmd.Env = mergeEnvironment(env)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	exitCode := -1
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		exitCode = exitError.ExitCode()
	}
	return &HookError{
		Path:     hookPath,
		ExitCode: exitCode,
		Output:   string(output),
		Err:      err,
	}
}

// InvokeEstablished is a convenience wrapper for an establishment callback.
func InvokeEstablished(ctx context.Context, hookPath, peerIdentity, virtualIP string, trafficSelectors []string) error {
	return InvokeHook(ctx, hookPath, Event{
		Phase:            PhaseEstablished,
		PeerIdentity:     peerIdentity,
		VirtualIP:        virtualIP,
		TrafficSelectors: trafficSelectors,
	})
}

// InvokeTerminated is a convenience wrapper for a teardown callback.
func InvokeTerminated(ctx context.Context, hookPath, peerIdentity, virtualIP string, trafficSelectors []string) error {
	return InvokeHook(ctx, hookPath, Event{
		Phase:            PhaseTerminated,
		PeerIdentity:     peerIdentity,
		VirtualIP:        virtualIP,
		TrafficSelectors: trafficSelectors,
	})
}

func mergeEnvironment(eventEnv []string) []string {
	keys := make(map[string]struct{}, len(eventEnv))
	for _, entry := range eventEnv {
		key, _, _ := strings.Cut(entry, "=")
		keys[key] = struct{}{}
	}

	base := os.Environ()
	merged := make([]string, 0, len(base)+len(eventEnv))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := keys[key]; replaced {
			continue
		}
		merged = append(merged, entry)
	}
	merged = append(merged, eventEnv...)
	return merged
}
