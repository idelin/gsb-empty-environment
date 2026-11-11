package wasmcap

import "fmt"

// FuncBody is the host implementation behind a captured function reference.
// Arguments and results are raw Wasm values.
type FuncBody func(args []uint64) ([]uint64, error)

// Func is a function reference captured from a specific instance version.
// It carries the capability it was authorized under; every call re-checks
// liveness, version, and current authorization.
type Func struct {
	inst    *Instance
	version uint64
	sig     Signature
	cap     Capability
	body    FuncBody
}

// CaptureFunc creates a callable reference to body, attributed to
// capability cap. The capability must be granted to the instance's module
// at capture time.
func (i *Instance) CaptureFunc(sig Signature, cap Capability, body FuncBody) (*Func, error) {
	if err := i.checkLive(i.version.Load()); err != nil {
		return nil, err
	}
	if !i.authorized(cap) {
		return nil, fmt.Errorf("%w: %q", ErrCapabilityNotGranted, cap)
	}
	return &Func{
		inst:    i,
		version: i.version.Load(),
		sig:     sig,
		cap:     cap,
		body:    body,
	}, nil
}

// Signature returns the function's declared signature.
func (f *Func) Signature() Signature { return f.sig }

// Capability returns the capability the function is attributed to.
func (f *Func) Capability() Capability { return f.cap }

// checkLive verifies the reference is still bound to a living instance
// version and that its capability is still granted.
func (f *Func) checkLive() error {
	if err := f.inst.checkLive(f.version); err != nil {
		return err
	}
	if !f.inst.authorized(f.cap) {
		return fmt.Errorf("%w: %q", ErrCapabilityNotGranted, f.cap)
	}
	return nil
}

// Call invokes the function directly, re-validating staleness and
// authorization before running the body.
func (f *Func) Call(args ...uint64) ([]uint64, error) {
	if err := f.checkLive(); err != nil {
		return nil, err
	}
	if len(args) != len(f.sig.Params) {
		return nil, fmt.Errorf("%w: got %d args, want %d", ErrSignatureMismatch, len(args), len(f.sig.Params))
	}
	return f.body(args)
}
