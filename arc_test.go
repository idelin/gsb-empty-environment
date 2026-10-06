package arc

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// naiveModel is a deliberately simple reference implementation of the same
// ARC semantics, built on plain slices. The randomized cross-check test
// drives both implementations with identical operation sequences and
// requires identical observable behavior at every step.
type naiveModel struct {
	capacity int
	p        int
	t1, t2   []string // MRU first
	b1, b2   []string // ghosts, MRU first
	vals     map[string]interface{}
}

func newNaiveModel(capacity int) *naiveModel {
	return &naiveModel{capacity: capacity, vals: make(map[string]interface{})}
}

func indexOf(list []string, key string) int {
	for i, k := range list {
		if k == key {
			return i
		}
	}
	return -1
}

func removeAt(list []string, i int) []string {
	return append(list[:i], list[i+1:]...)
}

func pushFront(list []string, key string) []string {
	return append([]string{key}, list...)
}

func (m *naiveModel) get(key string) (interface{}, bool) {
	if i := indexOf(m.t1, key); i >= 0 {
		m.t1 = removeAt(m.t1, i)
		m.t2 = pushFront(m.t2, key)
		return m.vals[key], true
	}
	if i := indexOf(m.t2, key); i >= 0 {
		m.t2 = pushFront(removeAt(m.t2, i), key)
		return m.vals[key], true
	}
	return nil, false
}

func (m *naiveModel) set(key string, value interface{}) {
	if i := indexOf(m.t1, key); i >= 0 {
		m.t1 = removeAt(m.t1, i)
		m.t2 = pushFront(m.t2, key)
		m.vals[key] = value
		return
	}
	if i := indexOf(m.t2, key); i >= 0 {
		m.t2 = pushFront(removeAt(m.t2, i), key)
		m.vals[key] = value
		return
	}
	if i := indexOf(m.b1, key); i >= 0 {
		m.p = minInt(m.capacity, m.p+maxInt(len(m.b2)/len(m.b1), 1))
		m.replace(false)
		m.b1 = removeAt(m.b1, indexOf(m.b1, key))
		m.t2 = pushFront(m.t2, key)
		m.vals[key] = value
		return
	}
	if i := indexOf(m.b2, key); i >= 0 {
		m.p = maxInt(0, m.p-maxInt(len(m.b1)/len(m.b2), 1))
		m.replace(true)
		m.b2 = removeAt(m.b2, indexOf(m.b2, key))
		m.t2 = pushFront(m.t2, key)
		m.vals[key] = value
		return
	}
	switch l1 := len(m.t1) + len(m.b1); {
	case l1 == m.capacity:
		if len(m.t1) < m.capacity {
			m.dropBack(&m.b1)
			m.replace(false)
		} else {
			m.dropBack(&m.t1)
		}
	case l1 < m.capacity:
		if total := l1 + len(m.t2) + len(m.b2); total >= m.capacity {
			if total == 2*m.capacity {
				m.dropBack(&m.b2)
			}
			m.replace(false)
		}
	}
	m.t1 = pushFront(m.t1, key)
	m.vals[key] = value
}

func (m *naiveModel) delete(key string) bool {
	for _, list := range []*[]string{&m.t1, &m.t2, &m.b1, &m.b2} {
		if i := indexOf(*list, key); i >= 0 {
			*list = removeAt(*list, i)
			delete(m.vals, key)
			return true
		}
	}
	return false
}

func (m *naiveModel) replace(fromB2 bool) {
	if len(m.t1) > 0 && ((fromB2 && len(m.t1) == m.p) || len(m.t1) > m.p) {
		key := m.t1[len(m.t1)-1]
		m.t1 = m.t1[:len(m.t1)-1]
		delete(m.vals, key)
		m.b1 = pushFront(m.b1, key)
		return
	}
	if len(m.t2) > 0 {
		key := m.t2[len(m.t2)-1]
		m.t2 = m.t2[:len(m.t2)-1]
		delete(m.vals, key)
		m.b2 = pushFront(m.b2, key)
	}
}

func (m *naiveModel) dropBack(list *[]string) {
	if len(*list) == 0 {
		return
	}
	delete(m.vals, (*list)[len(*list)-1])
	*list = (*list)[:len(*list)-1]
}

func (m *naiveModel) state() string {
	return fmt.Sprintf("p=%d T1=%s T2=%s B1=%s B2=%s",
		m.p, fmtKeys(m.t1), fmtKeys(m.t2), fmtKeys(m.b1), fmtKeys(m.b2))
}

func fmtKeys(list []string) string {
	return "[" + strings.Join(list, " ") + "]"
}

func TestNaiveModelCrossCheck(t *testing.T) {
	keySpace := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for capacity := 1; capacity <= 5; capacity++ {
		rng := rand.New(rand.NewSource(int64(capacity) * 7919))
		real := New(capacity)
		model := newNaiveModel(capacity)
		for step := 0; step < 3000; step++ {
			key := keySpace[rng.Intn(len(keySpace))]
			var op string
			switch rng.Intn(3) {
			case 0:
				op = fmt.Sprintf("Set %s", key)
				value := rng.Intn(100)
				real.Set(key, value)
				model.set(key, value)
			case 1:
				op = fmt.Sprintf("Get %s", key)
				realValue, realOK := real.Get(key)
				modelValue, modelOK := model.get(key)
				if realOK != modelOK || realValue != modelValue {
					t.Fatalf("capacity=%d step=%d %s: Get = (%v,%v), model = (%v,%v)",
						capacity, step, op, realValue, realOK, modelValue, modelOK)
				}
			case 2:
				op = fmt.Sprintf("Delete %s", key)
				if real.Delete(key) != model.delete(key) {
					t.Fatalf("capacity=%d step=%d %s: Delete results differ",
						capacity, step, op)
				}
			}
			if real.State() != model.state() {
				t.Fatalf("capacity=%d step=%d %s:\n real: %s\nmodel: %s",
					capacity, step, op, real.State(), model.state())
			}
			if real.Target() < 0 || real.Target() > capacity {
				t.Fatalf("capacity=%d step=%d %s: target %d out of [0,%d]",
					capacity, step, op, real.Target(), capacity)
			}
			if real.Len() > capacity {
				t.Fatalf("capacity=%d step=%d %s: %d live entries exceed capacity",
					capacity, step, op, real.Len())
			}
		}
	}
}

func TestCapacityOne(t *testing.T) {
	c := New(1)
	steps := []struct {
		op    func()
		state string
	}{
		{func() { c.Set("a", 1) }, "p=0 T1=[a] T2=[] B1=[] B2=[]"},
		{func() { c.Get("a") }, "p=0 T1=[] T2=[a] B1=[] B2=[]"},
		{func() { c.Set("b", 2) }, "p=0 T1=[b] T2=[] B1=[] B2=[a]"},
		// Ghost hit in B2: p would go negative, clamped at 0.
		{func() { c.Set("a", 10) }, "p=0 T1=[] T2=[a] B1=[b] B2=[]"},
		// Ghost hit in B1: p rises to 1, the full capacity.
		{func() { c.Set("b", 20) }, "p=1 T1=[] T2=[b] B1=[] B2=[a]"},
		// Ghost hit in B2: p falls back to 0; b is demoted T2 -> B2.
		{func() { c.Set("a", 100) }, "p=0 T1=[] T2=[a] B1=[] B2=[b]"},
	}
	for i, step := range steps {
		step.op()
		if c.State() != step.state {
			t.Fatalf("step %d:\n got: %s\nwant: %s", i+1, c.State(), step.state)
		}
		if c.Len() != 1 {
			t.Fatalf("step %d: Len() = %d, want 1", i+1, c.Len())
		}
	}
	if value, ok := c.Get("a"); !ok || value != 100 {
		t.Fatalf("Get(a) = (%v,%v), want (100,true)", value, ok)
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("Get(b) on ghost record must report a miss")
	}
}

func TestRepeatedDelete(t *testing.T) {
	c := New(2)
	if c.Delete("x") {
		t.Fatal("Delete on empty cache must return false")
	}
	c.Set("a", 1)
	c.Set("b", 2)
	if !c.Delete("a") {
		t.Fatal("first Delete(a) must return true")
	}
	if c.Delete("a") {
		t.Fatal("repeated Delete(a) must return false")
	}
	if c.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", c.Len())
	}

	// Build a ghost record for c, then delete it twice.
	c.Get("b")    // b promoted to T2
	c.Set("c", 3) // c enters T1
	c.Set("d", 4) // replace: LRU of T1 (c) becomes a ghost in B1
	if !c.Delete("c") {
		t.Fatal("Delete of ghost record must return true")
	}
	if c.Delete("c") {
		t.Fatal("repeated Delete of ghost record must return false")
	}
	want := "p=0 T1=[d] T2=[b] B1=[] B2=[]"
	if c.State() != want {
		t.Fatalf("got: %s\nwant: %s", c.State(), want)
	}
}

func TestTargetAdjustment(t *testing.T) {
	c := New(2)
	steps := []struct {
		op    func()
		state string
	}{
		{func() { c.Set("a", 1) }, "p=0 T1=[a] T2=[] B1=[] B2=[]"},
		{func() { c.Set("b", 2) }, "p=0 T1=[b a] T2=[] B1=[] B2=[]"},
		{func() { c.Get("a") }, "p=0 T1=[b] T2=[a] B1=[] B2=[]"},
		{func() { c.Set("c", 3) }, "p=0 T1=[c] T2=[a] B1=[b] B2=[]"},
		// Ghost hit in B1: p rises to 1.
		{func() { c.Set("b", 20) }, "p=1 T1=[c] T2=[b] B1=[] B2=[a]"},
		// Ghost hit in B2: p falls to 0.
		{func() { c.Set("a", 10) }, "p=0 T1=[] T2=[a b] B1=[c] B2=[]"},
		{func() { c.Set("d", 4) }, "p=0 T1=[d] T2=[a] B1=[c] B2=[b]"},
		{func() { c.Set("c", 30) }, "p=1 T1=[d] T2=[c] B1=[] B2=[a b]"},
		{func() { c.Set("a", 100) }, "p=0 T1=[] T2=[a c] B1=[d] B2=[b]"},
		// Ghost hit in B2 with p already 0: p clamps at 0.
		{func() { c.Set("b", 200) }, "p=0 T1=[] T2=[b a] B1=[d] B2=[c]"},
		{func() { c.Set("d", 400) }, "p=1 T1=[] T2=[d b] B1=[] B2=[a c]"},
		// T1+T2+B1+B2 reaches 2*capacity: oldest B2 ghost is trimmed.
		{func() { c.Set("e", 5) }, "p=1 T1=[e] T2=[d] B1=[] B2=[b a]"},
		{func() { c.Set("f", 6) }, "p=1 T1=[f e] T2=[] B1=[] B2=[d b]"},
		{func() { c.Set("b", 2000) }, "p=0 T1=[f] T2=[b] B1=[e] B2=[d]"},
		{func() { c.Set("d", 4000) }, "p=0 T1=[] T2=[d b] B1=[f e] B2=[]"},
		{func() { c.Set("e", 5000) }, "p=1 T1=[] T2=[e d] B1=[f] B2=[b]"},
		// Ghost hit in B1: p reaches capacity exactly.
		{func() { c.Set("f", 6000) }, "p=2 T1=[] T2=[f e] B1=[] B2=[d b]"},
		{func() { c.Set("a", 7) }, "p=2 T1=[a] T2=[f] B1=[] B2=[e d]"},
		{func() { c.Set("g", 8) }, "p=2 T1=[g a] T2=[] B1=[] B2=[f e]"},
		{func() { c.Set("f", 600) }, "p=1 T1=[g] T2=[f] B1=[a] B2=[e]"},
		{func() { c.Set("e", 500) }, "p=0 T1=[] T2=[e f] B1=[g a] B2=[]"},
		{func() { c.Set("a", 70) }, "p=1 T1=[] T2=[a e] B1=[g] B2=[f]"},
		{func() { c.Delete("e") }, "p=1 T1=[] T2=[a] B1=[g] B2=[f]"},
		{func() { c.Set("h", 9) }, "p=1 T1=[h] T2=[] B1=[g] B2=[a f]"},
		// Ghost hit in B1 with p=1 and delta 2: p clamps at capacity 2.
		{func() { c.Set("g", 800) }, "p=2 T1=[h] T2=[g] B1=[] B2=[a f]"},
	}
	for i, step := range steps {
		step.op()
		if c.State() != step.state {
			t.Fatalf("step %d:\n got: %s\nwant: %s", i+1, c.State(), step.state)
		}
		if c.Target() < 0 || c.Target() > c.Capacity() {
			t.Fatalf("step %d: target %d outside [0,%d]", i+1, c.Target(), c.Capacity())
		}
	}
}
