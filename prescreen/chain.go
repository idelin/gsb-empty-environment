package prescreen

import "crypto/ed25519"

// resolvedChain is the authenticated result of walking a request chain.
type resolvedChain struct {
	composed AffineMap
	terminal ObjectRef
}

// resolveChain authenticates every named edge, enforces the exact parent
// version binding, rejects cycles and composes the affine maps in order.
func resolveChain(p Policy, start ObjectRef, names []string) (resolvedChain, error) {
	idx := buildEdgeIndex(p)
	visited := map[string]bool{
		manifestKey(start.Tenant, start.Object): true,
	}
	current := start
	composed := IdentityMap()

	for _, name := range names {
		manifest, ok := idx.names[manifestKey(current.Tenant, name)]
		if !ok {
			return resolvedChain{}, ErrViewNotFound
		}
		if !trusted(p.Roots, manifest.Signer, manifestSigningBytes(manifest), manifest.Signature) {
			return resolvedChain{}, ErrUntrustedRoot
		}
		edge := manifest.Edge
		if edge.From != current {
			return resolvedChain{}, ErrObjectMismatch
		}
		if edge.ParentVersion != current.Version {
			return resolvedChain{}, ErrVersionMismatch
		}
		if edge.From.Tenant != start.Tenant || edge.To.Tenant != start.Tenant {
			return resolvedChain{}, ErrTenantMismatch
		}
		nextKey := manifestKey(edge.To.Tenant, edge.To.Object)
		if visited[nextKey] {
			return resolvedChain{}, ErrViewCycle
		}
		visited[nextKey] = true
		composed = Compose(edge.Map, composed)
		current = edge.To
	}
	return resolvedChain{composed: composed, terminal: current}, nil
}

// flatCap is a capability slice after signature, binding and subject filtering.
type flatCap struct{ slice Affine }

func verifyCapability(p Policy, c Capability) error {
	if len(c.Slices) > MaxSlices {
		return ErrTooManySlices
	}
	if !trusted(p.Roots, c.Signer, capabilitySigningBytes(c), c.Signature) {
		return ErrUntrustedRoot
	}
	return nil
}

// flattenCaps returns the authenticated slices of one kind bound to the given
// subject, tenant, terminal object and action. Bad or unrelated capabilities
// never contribute; a bad signature on a relevant capability is fatal.
func flattenCaps(p Policy, caps []Capability, kind CapKind, subject ed25519.PublicKey,
	tenant string, obj ObjectRef, action Action) ([]flatCap, error) {
	out := make([]flatCap, 0, len(caps))
	for _, c := range caps {
		if c.Kind != kind || !equalKey(c.Subject, subject) {
			continue
		}
		if c.Tenant != tenant || c.Object != obj {
			continue
		}
		if kind == CapGrant && c.Action != action {
			continue
		}
		if err := verifyCapability(p, c); err != nil {
			return nil, err
		}
		for _, af := range c.Slices {
			out = append(out, flatCap{slice: af})
		}
	}
	return out, nil
}

func equalKey(a, b ed25519.PublicKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
