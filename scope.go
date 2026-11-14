package netsec

import (
	"bytes"
	"encoding/json"
	"sort"
)

// canonicalScope serializes filters deterministically: keys are sorted so the
// same logical condition always yields the same bytes for HMAC binding.
func canonicalScope(f Filters) string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(f[k])
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.String()
}

// findPermission locates the grant for a dataset. Cross-tenant grants are
// never returned: a permission can only apply inside its own tenant.
func findPermission(perms []Permission, tenantID, dataset string) (*Permission, bool) {
	for i := range perms {
		perm := &perms[i]
		if perm.TenantID == tenantID && perm.Dataset == dataset {
			return perm, true
		}
	}
	return nil, false
}

// reconcileScope recomputes the effective scope for requested filters against
// a permission. Security rules:

//   - Every constrained key is mandatory. If the request supplies a different
//     value, ErrConditionRewritten is returned: the caller tried to widen or
//     alter an authorized condition.
//   - Missing constrained keys are injected, narrowing the scope down to the
//     permission instead of trusting the caller.
//   - Extra non-constrained keys are retained verbatim.
func reconcileScope(requested Filters, perm *Permission) (Filters, error) {
	effective := requested.Clone()
	if effective == nil {
		effective = make(Filters)
	}

	for key, allowed := range perm.Constraints {
		if got, ok := effective[key]; ok && got != allowed {
			return nil, ErrConditionRewritten
		}
		effective[key] = allowed
	}
	return effective, nil
}

// filtersEqual reports whether two filter sets contain identical pairs.
func filtersEqual(a, b Filters) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
