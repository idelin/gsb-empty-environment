package ormap

// Reference model: the direct interpretation of a fixed set of operations.
// A set op contributes its dot->value; a remove op deletes exactly the
// dots it observed when issued. Delivery order and duplication are
// irrelevant by construction, so this is the ground truth the CRDT must
// converge to under every delivery schedule.

type refOp struct {
	isSet    bool
	key      string
	dot      Dot
	value    string
	observed []Dot
}

func refSet(replica string, counter uint64, key, value string) refOp {
	return refOp{isSet: true, key: key, dot: Dot{Replica: replica, Counter: counter}, value: value}
}

func refRemove(key string, observed ...Dot) refOp {
	return refOp{key: key, observed: observed}
}

func refLive(ops []refOp, key string) map[Dot]string {
	live := make(map[Dot]string)
	for _, op := range ops {
		if op.isSet && op.key == key {
			live[op.dot] = op.value
		}
	}
	for _, op := range ops {
		if !op.isSet && op.key == key {
			for _, dot := range op.observed {
				delete(live, dot)
			}
		}
	}
	return live
}

func refDisplay(ops []refOp, key string) (string, bool) {
	live := refLive(ops, key)
	if len(live) == 0 {
		return "", false
	}
	var best Dot
	first := true
	for dot := range live {
		if first || best.Less(dot) {
			best = dot
			first = false
		}
	}
	return live[best], len(live) > 1
}

// toOps converts reference ops into CRDT delta ops.
func toOps(ops []refOp) []*Op {
	out := make([]*Op, 0, len(ops))
	for _, rop := range ops {
		if rop.isSet {
			out = append(out, NewSetOp(rop.dot.Replica, rop.dot.Counter, rop.key, rop.value))
		} else {
			out = append(out, NewRemoveOp(rop.key, rop.observed...))
		}
	}
	return out
}
