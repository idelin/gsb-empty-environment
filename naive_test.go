package arc

import "fmt"

// naiveCache is a deliberately simple, independent ARC implementation
// used as a reference model. Each queue is a plain slice of keys (MRU
// first) maintained with linear scans instead of maps and linked lists.
type naiveCache struct {
	capacity int
	p        int
	t1       []string
	t2       []string
	b1       []string
	b2       []string
	vals     map[string]interface{}
}

func newNaive(capacity int) *naiveCache {
	return &naiveCache{capacity: capacity, vals: make(map[string]interface{})}
}

func inQueue(q []string, key string) bool {
	for _, k := range q {
		if k == key {
			return true
		}
	}
	return false
}

func dropKey(q []string, key string) []string {
	out := q[:0]
	for _, k := range q {
		if k != key {
			out = append(out, k)
		}
	}
	return out
}

func pushMRU(q []string, key string) []string {
	return append([]string{key}, q...)
}

func (n *naiveCache) len() int { return len(n.t1) + len(n.t2) }

func (n *naiveCache) get(key string) (interface{}, bool) {
	switch {
	case inQueue(n.t1, key):
		n.t1 = dropKey(n.t1, key)
		n.t2 = pushMRU(n.t2, key)
		return n.vals[key], true
	case inQueue(n.t2, key):
		n.t2 = pushMRU(dropKey(n.t2, key), key)
		return n.vals[key], true
	}
	return nil, false
}

func (n *naiveCache) replace(inB2 bool) {
	if len(n.t1) == 0 && len(n.t2) == 0 {
		return
	}
	if len(n.t1) >= 1 && ((inB2 && len(n.t1) == n.p) || len(n.t1) > n.p) {
		n.b1 = pushMRU(n.b1, n.t1[len(n.t1)-1])
		n.t1 = n.t1[:len(n.t1)-1]
		return
	}
	if len(n.t2) >= 1 {
		n.b2 = pushMRU(n.b2, n.t2[len(n.t2)-1])
		n.t2 = n.t2[:len(n.t2)-1]
		return
	}
	n.b1 = pushMRU(n.b1, n.t1[len(n.t1)-1])
	n.t1 = n.t1[:len(n.t1)-1]
}

func (n *naiveCache) set(key string, value interface{}) {
	switch {
	case inQueue(n.t1, key) || inQueue(n.t2, key):
		n.vals[key] = value
		n.get(key)
	case inQueue(n.b1, key):
		if d := len(n.b2) / len(n.b1); d > 1 {
			n.p += d
		} else {
			n.p++
		}
		if n.p > n.capacity {
			n.p = n.capacity
		}
		n.replace(false)
		n.b1 = dropKey(n.b1, key)
		n.t2 = pushMRU(n.t2, key)
		n.vals[key] = value
	case inQueue(n.b2, key):
		if d := len(n.b1) / len(n.b2); d > 1 {
			n.p -= d
		} else {
			n.p--
		}
		if n.p < 0 {
			n.p = 0
		}
		n.replace(true)
		n.b2 = dropKey(n.b2, key)
		n.t2 = pushMRU(n.t2, key)
		n.vals[key] = value
	default:
		l1 := len(n.t1) + len(n.b1)
		if l1 == n.capacity {
			if len(n.t1) < n.capacity {
				n.b1 = n.b1[:len(n.b1)-1]
				n.replace(false)
			} else {
				n.t1 = n.t1[:len(n.t1)-1]
			}
		} else if l1 < n.capacity {
			if total := l1 + len(n.t2) + len(n.b2); total >= n.capacity {
				if total == 2*n.capacity {
					n.b2 = n.b2[:len(n.b2)-1]
				}
				n.replace(false)
			}
		}
		n.t1 = pushMRU(n.t1, key)
		n.vals[key] = value
	}
}

func (n *naiveCache) delete(key string) bool {
	hadValue := inQueue(n.t1, key) || inQueue(n.t2, key)
	n.t1 = dropKey(n.t1, key)
	n.t2 = dropKey(n.t2, key)
	n.b1 = dropKey(n.b1, key)
	n.b2 = dropKey(n.b2, key)
	delete(n.vals, key)
	return hadValue
}

func (n *naiveCache) snapshot() string {
	return fmt.Sprintf("p=%d T1=%v T2=%v B1=%v B2=%v", n.p, n.t1, n.t2, n.b1, n.b2)
}
