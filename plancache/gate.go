package plancache

import (
	"errors"
	"fmt"
)

func isImmediate(err error) bool {
	return errors.Is(err, ErrImmediateInvalidation)
}

func reasonOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func eventOf(err error) string {
	if isImmediate(err) {
		// Fenced error messages carry event=<type>.
		msg := err.Error()
		const marker = "event="
		for i := 0; i+len(marker) <= len(msg); i++ {
			if msg[i:i+len(marker)] == marker {
				rest := msg[i+len(marker):]
				for j, r := range rest {
					if r == ' ' || r == ':' {
						return rest[:j]
					}
				}
				return rest
			}
		}
	}
	return "unknown"
}

// checkImmediate inspects the invalidation log for immediate events after
// the given fence that touch this plan's manifest. This is the mechanism by
// which immediate REVOKE / RLS / DDL pierces a pinned transaction snapshot.
func checkImmediate(bus *InvalidationBus, fence int64, plan *Plan) error {
	events := bus.Since(fence, plan.Manifest.Caller, plan.Manifest)
	if len(events) == 0 {
		return nil
	}
	return fmt.Errorf("%w: event=%s fence=%d", ErrImmediateInvalidation, events[0].Type, events[0].Fence)
}

// revalidate runs the full pre-execution checklist against the transaction's
// pinned snapshot. Any single mismatch is a miss: the caller must recompile
// rather than execute the stale plan. No operator callback has run at this
// point.
func revalidate(cat Catalog, snap *Snapshot, plan *Plan) error {
	m := plan.Manifest

	// 1. Principal must still be active (live liveness, not just snapshot).
	if !cat.LiveActive(m.Caller) || !snap.Active {
		return fmt.Errorf("%w: principal inactive", ErrPrincipalInactive)
	}

	// 2. Role closure must be unchanged within this snapshot.
	if snap.RoleClosure != m.RoleClosure {
		return fmt.Errorf("%w: role closure drift", ErrStalePlan)
	}

	// 3. Global policy epoch must match the pinned snapshot.
	if snap.Epoch != m.PolicyEpoch {
		return fmt.Errorf("%w: policy epoch %d != %d", ErrStalePlan, m.PolicyEpoch, snap.Epoch)
	}

	// 4. Every nested view identity and definition version must still
	// match; no downgrade or rewrite is accepted.
	for _, mv := range m.Views {
		v, ok := snap.Views[mv.ViewID]
		if !ok {
			return fmt.Errorf("%w: view %s missing", ErrStalePlan, mv.ViewID)
		}
		if v.defVersion != mv.ViewDefVersion ||
			v.underlying != mv.UnderlyingTable ||
			v.parent != mv.ParentViewID {
			return fmt.Errorf("%w: view %s rewritten", ErrStalePlan, mv.ViewID)
		}
	}

	// 5. Every base object identity/schema version and RLS digest must
	// match, and the snapshot must still grant every required operation.
	for _, mt := range m.Tables {
		t, ok := snap.Tables[mt.ObjectID]
		if !ok {
			return fmt.Errorf("%w: object %s missing", ErrStalePlan, mt.ObjectID)
		}
		if t.schemaVersion != mt.SchemaVersion {
			return fmt.Errorf("%w: object %s schema version drift", ErrStalePlan, mt.ObjectID)
		}
		if t.rls != mt.RLSPolicy {
			return fmt.Errorf("%w: object %s rls digest drift", ErrStalePlan, mt.ObjectID)
		}
		for _, op := range m.RequiredOps {
			if !snap.Privileges[mt.ObjectID][m.Caller][op] {
				return fmt.Errorf("%w: missing %s on %s", ErrUnauthorized, op, mt.ObjectID)
			}
		}
	}
	return nil
}
