package netsec

import (
	"fmt"
	"strings"
	"testing"
)

// The trace search below explores a finite action vocabulary against the real
// remapper. A scenario is a sequence of tenant/host actions; after each
// interrupt action the callback checks that the destination domain is exactly
// the device's current owner. If a trace produces a cross-domain delivery, the
// search reports it. The secure implementation is expected to have no such
// trace up to the bound.

type auditAction struct {
	name string
	run  func(*auditWorld) error
}

type auditWorld struct {
	r         *Remapper
	a         *Device
	b         *Device
	violation string
}

func (w *auditWorld) callback(dev *Device) DeliveryFunc {
	return func(d Delivery) error {
		if d.DomainID != dev.Owner() {
			w.violation = fmt.Sprintf(
				"requester %#04x delivered to domain %d, owner is %d",
				d.RequesterID, d.DomainID, dev.Owner())
		}
		return nil
	}
}

func newAuditWorld() *auditWorld {
	r := NewRemapper()
	config := DomainConfig{
		Vectors: VectorRange{Min: 0x20, Max: 0x2f},
		VCPUs: []VCPU{
			{ID: 0, Generation: 1},
			{ID: 1, Generation: 1},
		},
	}
	_ = r.RegisterDomain(domA, config)
	_ = r.RegisterDomain(domB, config)
	a, _ := r.AttachDevice(reqA, domA)
	b, _ := r.AttachDevice(reqB, domB)
	return &auditWorld{r: r, a: a, b: b}
}

func auditActions() []auditAction {
	req := func(index TableIndex, vcpu VCPUID, gen VCPUGeneration, vector Vector) InstallRequest {
		return InstallRequest{
			TableIndex: index, VCPU: vcpu, VCPUGeneration: gen,
			Vector: vector, DeliveryMode: DeliveryFixed,
		}
	}
	return []auditAction{
		{"install-a-0", func(w *auditWorld) error { return w.a.Install(domA, req(0, 0, 1, 0x21)) }},
		{"install-a-1", func(w *auditWorld) error { return w.a.Install(domA, req(1, 1, 1, 0x22)) }},
		{"install-b-0", func(w *auditWorld) error { return w.b.Install(domB, req(0, 0, 1, 0x21)) }},
		{"foreign-install-a", func(w *auditWorld) error { return w.a.Install(domB, req(0, 0, 1, 0x23)) }},
		{"forged-vector-install-a", func(w *auditWorld) error { return w.a.Install(domA, req(0, 0, 1, 0x40)) }},
		{"send-a-hit", func(w *auditWorld) error { return w.a.SendInterrupt(0, 0x21, w.callback(w.a)) }},
		{"send-a-forged", func(w *auditWorld) error { return w.a.SendInterrupt(0, 0x22, w.callback(w.a)) }},
		{"send-b-hit", func(w *auditWorld) error { return w.b.SendInterrupt(0, 0x21, w.callback(w.b)) }},
		{"reset-a", func(w *auditWorld) error { return w.a.Reset() }},
		{"reassign-a-b", func(w *auditWorld) error {
			replacement, err := w.r.Reassign(w.a, domB)
			if err != nil {
				return err
			}
			w.a = replacement
			return nil
		}},
		{"reassign-a-a", func(w *auditWorld) error {
			replacement, err := w.r.Reassign(w.a, domA)
			if err != nil {
				return err
			}
			w.a = replacement
			return nil
		}},
		{"install-a-as-b", func(w *auditWorld) error { return w.a.Install(domB, req(2, 0, 1, 0x24)) }},
		{"remove-a-vcpu-1", func(w *auditWorld) error { return w.r.RemoveVCPU(domA, 1) }},
		{"add-a-vcpu-1-gen2", func(w *auditWorld) error { return w.r.AddVCPU(domA, 1, 2) }},
	}
}

func replayAudit(actions []auditAction) (string, bool) {
	w := newAuditWorld()
	for _, action := range actions {
		_ = action.run(w)
		if w.violation != "" {
			return w.violation, true
		}
	}
	return "", false
}

func searchSecureTrace(maxDepth int) ([]string, string, bool) {
	actions := auditActions()
	frontier := [][]auditAction{{}}
	for depth := 1; depth <= maxDepth; depth++ {
		var next [][]auditAction
		for _, prefix := range frontier {
			for _, action := range actions {
				candidate := make([]auditAction, len(prefix)+1)
				copy(candidate, prefix)
				candidate[len(prefix)] = action
				if why, bad := replayAudit(candidate); bad {
					names := make([]string, len(candidate))
					for i, step := range candidate {
						names[i] = step.name
					}
					return names, why, true
				}
				next = append(next, candidate)
			}
		}
		frontier = next
	}
	return nil, "", false
}

// vectorOnlyState is a deliberately vulnerable reference model: entries are
// looked up only by message vector, ignoring requester and device generation.
// It exists so the trace searcher can prove it finds the known shortest
// cross-domain path rather than silently reporting "no bug".
type vectorOnlyState struct {
	table map[Vector]DomainID
}

type vectorOnlyStep struct {
	name string
	run  func(*vectorOnlyState) bool
}

func shortestVectorOnlyTrace(maxDepth int) []string {
	steps := []vectorOnlyStep{
		{"install-b-vector-40", func(s *vectorOnlyState) bool {
			s.table[0x40] = domB
			return false
		}},
		{"install-a-vector-41", func(s *vectorOnlyState) bool {
			s.table[0x41] = domA
			return false
		}},
		{"send-a-vector-40", func(s *vectorOnlyState) bool {
			target, ok := s.table[0x40]
			return ok && target != domA
		}},
		{"send-b-vector-41", func(s *vectorOnlyState) bool {
			target, ok := s.table[0x41]
			return ok && target != domB
		}},
	}
	frontier := [][]vectorOnlyStep{{}}
	for depth := 1; depth <= maxDepth; depth++ {
		var next [][]vectorOnlyStep
		for _, prefix := range frontier {
			for _, step := range steps {
				candidate := make([]vectorOnlyStep, len(prefix)+1)
				copy(candidate, prefix)
				candidate[len(prefix)] = step
				state := &vectorOnlyState{table: make(map[Vector]DomainID)}
				bad := false
				for _, run := range candidate {
					if run.run(state) {
						bad = true
						break
					}
				}
				if bad {
					trace := make([]string, len(candidate))
					for i, run := range candidate {
						trace[i] = run.name
					}
					return trace
				}
				next = append(next, candidate)
			}
		}
		frontier = next
	}
	return nil
}

func TestShortestCrossDomainTraceSearch(t *testing.T) {
	// Bound kept small enough for an exhaustive replay search, large enough
	// to cover install -> reset/reassign -> stale transport -> delivery.
	trace, detail, found := searchSecureTrace(4)
	if found {
		t.Fatalf("cross-domain delivery trace: %s (%s)", strings.Join(trace, " -> "), detail)
	}
	t.Log("secure remapper: no cross-domain delivery trace up to 4 actions")

	vulnerable := shortestVectorOnlyTrace(4)
	if len(vulnerable) == 0 {
		t.Fatal("vector-only reference search failed to find its known trace")
	}
	t.Logf("vector-only reference shortest trace (%d actions): %s",
		len(vulnerable), strings.Join(vulnerable, " -> "))
}
