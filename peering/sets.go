package peering

// Small unexported OSD set / slice helpers. Deterministic ordering matters:
// every externally visible OSD list is rendered in ascending OSDID order or
// in the explicit up-set preference order, never map iteration order.

func osdSet(in []OSDID) map[OSDID]struct{} {
	out := make(map[OSDID]struct{}, len(in))
	for _, id := range in {
		out[id] = struct{}{}
	}
	return out
}

func sortedOSDs(in map[OSDID]struct{}) []OSDID {
	out := make([]OSDID, 0, len(in))
	for id := range in {
		out = append(out, id)
	}
	sortOSDIDs(out)
	return out
}

func sortOSDIDs(ids []OSDID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}

func copyIDs(in []OSDID) []OSDID {
	if len(in) == 0 {
		return nil
	}
	out := make([]OSDID, len(in))
	copy(out, in)
	return out
}

func containsID(list []OSDID, id OSDID) bool {
	for _, candidate := range list {
		if candidate == id {
			return true
		}
	}
	return false
}

// setsEqual compares two OSD lists as order-independent sets.
func setsEqual(a, b []OSDID) bool {
	if len(a) != len(b) {
		return false
	}
	have := osdSet(a)
	for _, id := range b {
		if _, ok := have[id]; !ok {
			return false
		}
	}
	return true
}

// intersects reports whether any OSD in list belongs to set.
func intersects(list []OSDID, set map[OSDID]struct{}) bool {
	for _, id := range list {
		if _, ok := set[id]; ok {
			return true
		}
	}
	return false
}
