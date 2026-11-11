package policypub

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
)

// Action is the only two outcomes a packet can receive.
type Action int

const (
	// ActionDrop discards the packet.
	ActionDrop Action = iota
	// ActionEncrypt emits the packet under the referenced SA.
	ActionEncrypt
)

// Decision never contains a plaintext/bypass outcome.
type Decision struct {
	Action   Action
	SAHandle string
	Reason   string
}

// Snapshot is one immutable, fully installed policy generation.
type Snapshot struct {
	generation uint64
	table      *Table
	handles    map[string]struct{}
}

// Generation is the monotonically increasing policy generation number.
func (s *Snapshot) Generation() uint64 { return s.generation }

// Table returns the compiled selector table for this generation.
func (s *Snapshot) Table() *Table { return s.table }

// Handles returns a sorted copy of the SA handles this generation uses.
func (s *Snapshot) Handles() []string {
	out := make([]string, 0, len(s.handles))
	for handle := range s.handles {
		out = append(out, handle)
	}
	sort.Strings(out)
	return out
}

// Policy is a full policy generation: selectors plus every SA they reference.
type Policy struct {
	Rules []Rule
	SAs   []SA
}

// Publisher serializes policy publication and holds the active generation.
// Readers are lock-free: they load an atomic snapshot pointer.
type Publisher struct {
	installer Installer
	publishMu sync.Mutex
	active    atomic.Pointer[Snapshot]
	gen       atomic.Uint64
}

// New creates a publisher backed by the given SA database.
func New(installer Installer) *Publisher {
	if installer == nil {
		panic("policypub: installer is nil")
	}
	return &Publisher{installer: installer}
}

// Active returns the current policy generation, or nil before the first
// successful publication.
func (p *Publisher) Active() *Snapshot { return p.active.Load() }

// Decide resolves a packet flow against the active generation. It returns
// ActionEncrypt only when the backing SA is confirmed installed; every other
// situation (no policy, no selector, missing SA) returns ActionDrop, so a
// packet is always either encrypted or discarded.
func (p *Publisher) Decide(dir Direction, src, dst netip.Addr) Decision {
	snap := p.active.Load()
	if snap == nil {
		return Decision{Action: ActionDrop, Reason: "no-active-policy"}
	}
	handle, ok := snap.table.Lookup(dir, src, dst)
	if !ok {
		return Decision{Action: ActionDrop, Reason: "no-matching-selector"}
	}
	if !p.installer.Installed(handle) {
		return Decision{Action: ActionDrop, Reason: "sa-not-installed"}
	}
	return Decision{Action: ActionEncrypt, SAHandle: handle}
}

// Publish compiles the policy, installs every required SA in both directions,
// and only then atomically swaps the active generation. If any install fails
// in either direction, every SA already installed for this attempt is
// uninstalled in reverse order and the previously active generation remains
// untouched.
func (p *Publisher) Publish(ctx context.Context, policy Policy) (*Snapshot, error) {
	p.publishMu.Lock()
	defer p.publishMu.Unlock()

	table, err := Compile(policy.Rules)
	if err != nil {
		return nil, err
	}
	plan, err := buildInstallPlan(policy)
	if err != nil {
		return nil, err
	}

	installed := make([]string, 0, len(plan))
	rollback := func(cause error) (*Snapshot, error) {
		for i := len(installed) - 1; i >= 0; i-- {
			if uErr := p.installer.Uninstall(context.WithoutCancel(ctx), installed[i]); uErr != nil {
				cause = errors.Join(cause, fmt.Errorf("rollback %s: %w", installed[i], uErr))
			}
		}
		return nil, cause
	}

	for _, sa := range plan {
		if err := p.installer.Install(ctx, sa); err != nil {
			return rollback(fmt.Errorf("install %s (%s): %w",
				sa.Handle, sa.Direction, err))
		}
		installed = append(installed, sa.Handle)
	}

	generation := p.gen.Add(1)
	if barrier, ok := p.installer.(SwapBarrier); ok {
		barrier.BeforeSwap(generation)
	}

	snapshot := &Snapshot{
		generation: generation,
		table:      table,
		handles:    make(map[string]struct{}, len(plan)),
	}
	for _, sa := range plan {
		snapshot.handles[sa.Handle] = struct{}{}
	}

	previous := p.active.Swap(snapshot)
	if previous != nil {
		// Retire the previous generation. Its readers have already loaded a
		// pointer and keep using the immutable old snapshot; new readers see
		// the new one. Cleanup failures do not invalidate the swap.
		for _, handle := range previous.Handles() {
			if _, keep := snapshot.handles[handle]; keep {
				continue
			}
			_ = p.installer.Uninstall(context.WithoutCancel(ctx), handle)
		}
	}
	return snapshot, nil
}

// buildInstallPlan validates SA/selector cross-references and returns the
// deterministic install order: all outbound SAs, then all inbound SAs, each
// group sorted by handle. A copy of each SA (including its key material) is
// handed to the installer, leaving the caller's Policy untouched.
func buildInstallPlan(policy Policy) ([]SA, error) {
	byHandle := make(map[string]SA, len(policy.SAs))
	for _, sa := range policy.SAs {
		if sa.Handle == "" || sa.Direction == 0 {
			return nil, errors.New("policypub: SA missing handle or direction")
		}
		if _, dup := byHandle[sa.Handle]; dup {
			return nil, fmt.Errorf("policypub: duplicate SA handle %q", sa.Handle)
		}
		byHandle[sa.Handle] = sa
	}

	outbound := make(map[string]struct{})
	inbound := make(map[string]struct{})
	for _, rule := range policy.Rules {
		out, ok := byHandle[rule.OutboundSA]
		if !ok {
			return nil, fmt.Errorf("policypub: rule %q references unknown SA %q", rule.Name, rule.OutboundSA)
		}
		if out.Direction != Outbound {
			return nil, fmt.Errorf("policypub: SA %q used outbound but direction is %s", out.Handle, out.Direction)
		}
		in, ok := byHandle[rule.InboundSA]
		if !ok {
			return nil, fmt.Errorf("policypub: rule %q references unknown SA %q", rule.Name, rule.InboundSA)
		}
		if in.Direction != Inbound {
			return nil, fmt.Errorf("policypub: SA %q used inbound but direction is %s", in.Handle, in.Direction)
		}
		outbound[out.Handle] = struct{}{}
		inbound[in.Handle] = struct{}{}
	}

	plan := make([]SA, 0, len(outbound)+len(inbound))
	add := func(handles map[string]struct{}, dir Direction) {
		sorted := make([]string, 0, len(handles))
		for handle := range handles {
			sorted = append(sorted, handle)
		}
		sort.Strings(sorted)
		for _, handle := range sorted {
			sa := byHandle[handle]
			sa.Key = append([]byte(nil), sa.Key...)
			plan = append(plan, sa)
		}
	}
	add(outbound, Outbound)
	add(inbound, Inbound)
	return plan, nil
}
