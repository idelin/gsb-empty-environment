package netsec

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type bfsScope struct {
	scope  Scope
	prefix string
}

type bfsNode struct {
	policyName Name
	text       string
}

type bfsServiceSet struct {
	scope   Scope
	service *Service
	nodes   []bfsNode
}

type bfsRaceResult struct {
	handle ObjectHandle
	source string
	text   string
	typ    ObjectType
	err    error
}

// TestConcurrentCreationBFS drives a breadth-first tree of names with mixed
// case, type, creator, and descriptor variants. Every folded name must have
// exactly one winner, and callbacks may only run when that winner satisfies
// service policy.
func TestConcurrentCreationBFS(t *testing.T) {
	ns := NewNamespace()
	scopes := []bfsScope{
		{scope: SessionScope("s1"), prefix: "s1"},
		{scope: SessionScope("s2"), prefix: "s2"},
		{scope: GlobalScope(), prefix: "g"},
	}
	levels := [][]string{
		{"svc"},
		{"svc\\alpha", "svc\\beta", "svc\\gamma"},
		{
			"svc\\alpha\\one", "svc\\alpha\\two", "svc\\alpha\\three",
			"svc\\beta\\one", "svc\\beta\\two", "svc\\beta\\three",
			"svc\\gamma\\one", "svc\\gamma\\two", "svc\\gamma\\three",
		},
	}

	var services []bfsServiceSet
	for _, sc := range scopes {
		s := NewService(ns, testServiceIdentity)
		set := bfsServiceSet{scope: sc.scope, service: s}
		for _, text := range flatten(levels) {
			name := MustName(sc.scope, text, ObjectTypeMutex)
			if err := s.RegisterPolicy(mustPolicy(t, name, []string{testServiceSID}, IntegrityHigh, testServiceSD)); err != nil {
				t.Fatal(err)
			}
			set.nodes = append(set.nodes, bfsNode{policyName: name, text: text})
		}
		services = append(services, set)
	}

	// Process one BFS level at a time. All names at a level race concurrently;
	// children are only attempted after their parent level has settled.
	for levelIndex, level := range levels {
		for _, sc := range scopes {
			var wg sync.WaitGroup
			results := make(map[string][]bfsRaceResult, len(level))
			var mu sync.Mutex

			for _, text := range level {
				variants := []struct {
					source  string
					name    Name
					creator Creator
					sd      SecurityDescriptor
				}{
					{"service", MustName(sc.scope, text, ObjectTypeMutex), testServiceIdentity, testServiceSD},
					{"service-upper", MustName(sc.scope, strings.ToUpper(text), ObjectTypeMutex), testServiceIdentity, testServiceSD},
					{"service-alt", MustName(sc.scope, alternateCase(text), ObjectTypeMutex), testServiceIdentity, testServiceSD},
					{"attacker-section", MustName(sc.scope, text, ObjectTypeSection), testAttackerIdentity, testAttackerSD},
					{"attacker-section-upper", MustName(sc.scope, strings.ToUpper(text), ObjectTypeSection), testAttackerIdentity, testAttackerSD},
					{"attacker-mutex", MustName(sc.scope, alternateCase(text), ObjectTypeMutex), testAttackerIdentity, testAttackerSD},
				}

				start := make(chan struct{})
				for _, variant := range variants {
					v := variant
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						h, err := ns.CreateIfAbsent(CreateRequest{
							Name:               v.name,
							Creator:            v.creator,
							SecurityDescriptor: v.sd,
						})
						mu.Lock()
						results[text] = append(results[text], bfsRaceResult{
							handle: h, source: v.source, text: v.name.Text, typ: v.name.Type, err: err,
						})
						mu.Unlock()
					}()
				}
				close(start)
			}
			wg.Wait()

			for _, text := range level {
				rs := results[text]
				if len(rs) != 6 {
					t.Fatalf("level %d %s: got %d results", levelIndex, text, len(rs))
				}
				var winner *bfsRaceResult
				for i := range rs {
					r := &rs[i]
					if r.err == nil {
						if winner != nil {
							t.Fatalf("level %d %s: two winners: %s and %s", levelIndex, text, winner.source, r.source)
						}
						winner = r
						continue
					}
					if !errors.Is(r.err, ErrAlreadyExists) {
						t.Fatalf("level %d %s: loser %s error = %v", levelIndex, text, r.source, r.err)
					}
					if r.text != winnerText(rs) && !errors.Is(r.err, ErrCaseCollision) {
						t.Fatalf("level %d %s: case-variant loser lacks case collision: %v", levelIndex, text, r.err)
					}
					if r.typ != winnerType(rs) && !errors.Is(r.err, ErrTypeMismatch) {
						t.Fatalf("level %d %s: type-variant loser lacks type mismatch: %v", levelIndex, text, r.err)
					}
				}
				if winner == nil {
					t.Fatalf("level %d %s: no winner", levelIndex, text)
				}
				lookup, ok := ns.Lookup(sc.scope, winner.text)
				if !ok || !lookup.IdentityEqual(winner.handle) {
					t.Fatalf("level %d %s: lookup does not match winner", levelIndex, text)
				}

				// Service must accept only a fully compliant winner. A rejected
				// winner must never reach a Lock/Map callback.
				var serviceCallbacks atomic.Int32
				policyName := MustName(sc.scope, text, ObjectTypeMutex)
				bound, err := services[indexOfScope(services, sc.scope)].service.OpenExisting(policyName)
				if err != nil {
					if err := services[indexOfScope(services, sc.scope)].service.Lock(policyName, func(VerifiedHandle) error {
						serviceCallbacks.Add(1)
						return nil
					}); !errors.Is(err, ErrNotBound) {
						t.Fatalf("level %d %s: Lock after rejected open = %v", levelIndex, text, err)
					}
					if serviceCallbacks.Load() != 0 {
						t.Fatalf("level %d %s: callback ran after rejected winner %s", levelIndex, text, winner.source)
					}
					continue
				}
				if err := bound.Lock(func(h VerifiedHandle) error {
					serviceCallbacks.Add(1)
					if h.ObjectID != winner.handle.ObjectID || h.Generation != winner.handle.Generation {
						t.Errorf("level %d %s: callback got wrong handle", levelIndex, text)
					}
					return nil
				}); err != nil {
					t.Fatalf("level %d %s: compliant winner use = %v", levelIndex, text, err)
				}
				if serviceCallbacks.Load() != 1 {
					t.Fatalf("level %d %s: callback count = %d", levelIndex, text, serviceCallbacks.Load())
				}
			}
		}
	}

	// The BFS must not have merged scopes: each scope owns its own set.
	if got, want := ns.Len(), len(scopes)*len(flatten(levels)); got != want {
		t.Fatalf("namespace length = %d, want %d", got, want)
	}
}

func winnerText(rs []bfsRaceResult) string {
	for _, r := range rs {
		if r.err == nil {
			return r.text
		}
	}
	return ""
}

func winnerType(rs []bfsRaceResult) ObjectType {
	for _, r := range rs {
		if r.err == nil {
			return r.typ
		}
	}
	return ObjectTypeUnknown
}

func flatten(levels [][]string) []string {
	var out []string
	for _, level := range levels {
		out = append(out, level...)
	}
	return out
}

func alternateCase(s string) string {
	var b strings.Builder
	upper := false
	for _, r := range s {
		if r == '\\' {
			b.WriteRune(r)
			continue
		}
		if upper {
			b.WriteString(strings.ToUpper(string(r)))
		} else {
			b.WriteString(strings.ToLower(string(r)))
		}
		upper = !upper
	}
	return b.String()
}

func indexOfScope(services []bfsServiceSet, scope Scope) int {
	for i, s := range services {
		if s.scope == scope {
			return i
		}
	}
	panic(fmt.Sprintf("scope %v not found", scope))
}
