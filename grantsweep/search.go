package grantsweep

import (
	"fmt"
	"strings"
	"time"
)

// Finding is a divergence between a weak model and the reference model.
type Finding struct {
	Policy Policy
	Trace  []Step
}

// Config bounds the search.
type Config struct {
	MaxDepth int
	MaxNodes int
}

// DefaultConfig returns practical bounds that keep the search exhaustive over
// the small action alphabet while remaining fast.
func DefaultConfig() Config {
	return Config{MaxDepth: 7, MaxNodes: 200000}
}

type node struct {
	world *World
	trace []Step
	depth int
}

type action struct {
	name   string
	detail string
	apply  func(w *World) (allowed bool, hasVerdict bool)
}

// Search runs BFS from the same initial state under ref and weak policies. It
// returns the shortest trace whose terminal Use is allowed by the weak model
// but denied by the reference model.
func Search(weak Policy, cfg Config) (*Finding, int, error) {
	ref := NewWorld(Policy{})
	visited := map[string]bool{fingerprint(ref): true}
	queue := []node{{world: ref, depth: 0}}
	evaluated := 0

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		evaluated++
		if cfg.MaxNodes > 0 && evaluated > cfg.MaxNodes {
			return nil, evaluated, fmt.Errorf("grantsweep: node budget %d exhausted", cfg.MaxNodes)
		}

		for _, act := range cur.world.actions() {
			refNext := cur.world.Clone()
			weakNext := cur.world.CloneWithPolicy(weak)

			refAllowed, hasVerdict := act.apply(refNext)
			weakAllowed, _ := act.apply(weakNext)

			step := Step{Name: act.name, Detail: act.detail, Allowed: weakAllowed}
			trace := append(append([]Step(nil), cur.trace...), step)

			// A vulnerability is a successful weak call on a verdict-bearing
			// action where the reference correctly denies it.
			if hasVerdict && weakAllowed && !refAllowed {
				return &Finding{Policy: weak, Trace: trace}, evaluated, nil
			}

			fp := fingerprint(refNext)
			nextDepth := cur.depth + 1
			if nextDepth > cfg.MaxDepth || visited[fp] {
				continue
			}
			visited[fp] = true
			queue = append(queue, node{world: refNext, trace: trace, depth: nextDepth})
		}
	}
	return nil, evaluated, nil
}

// SearchAllPolicies runs Search for every weak policy and returns findings
// keyed by policy name. The reference policy is expected to have no finding.
func SearchAllPolicies(cfg Config) (map[string]*Finding, map[string]int, error) {
	findings := make(map[string]*Finding)
	stats := make(map[string]int)
	for _, p := range AllPolicies() {
		if p == (Policy{}) {
			continue
		}
		finding, evaluated, err := Search(p, cfg)
		stats[p.Name()] = evaluated
		if err != nil {
			return nil, stats, fmt.Errorf("policy %s: %w", p.Name(), err)
		}
		if finding != nil {
			findings[p.Name()] = finding
		}
	}
	return findings, stats, nil
}

func (w *World) actions() []action {
	var out []action

	validCreate := CreateSpec{
		GrantID: "g1", RequestID: "r1", Creator: "alice",
		KeyID: "k1", KeyVersion: 1, GranteePrincipal: "bob",
		RetiringPrincipal: "carol", Operations: []Operation{OpDecrypt},
		Constraints: Context{"env": "prod"}, AllowPending: true,
	}
	out = append(out, action{
		name:   "BeginCreate",
		detail: "g1 by alice for bob, Decrypt k1/v1, env=prod",
		apply: func(w *World) (bool, bool) {
			_, err := w.BeginCreate(validCreate)
			return err == nil, false
		},
	})

	subsetCreate := validCreate
	subsetCreate.GrantID = "g2"
	subsetCreate.RequestID = "r2"
	subsetCreate.Constraints = nil
	subsetCreate.Subset = Context{"env": "prod"}
	out = append(out, action{
		name:   "BeginCreateSubset",
		detail: "g2 subset grant, requires env=prod",
		apply: func(w *World) (bool, bool) {
			_, err := w.BeginCreate(subsetCreate)
			return err == nil, false
		},
	})

	out = append(out,
		action{
			name:   "Activate(g1)",
			detail: "alice activates r1",
			apply: func(w *World) (bool, bool) {
				_, err := w.Activate("alice", "r1")
				return err == nil, false
			},
		},
		action{
			name:   "Activate(g2)",
			detail: "alice activates r2",
			apply: func(w *World) (bool, bool) {
				_, err := w.Activate("alice", "r2")
				return err == nil, false
			},
		},
	)

	// Use actions. Each references a token fetched from current state so the
	// same action template works before/after activation and retirement.
	useActions := []struct {
		name   string
		detail string
		build  func(w *World) UseSpec
	}{
		{
			name:   "Use/valid",
			detail: "bob Decrypt k1/v1 env=prod",
			build: func(w *World) UseSpec {
				return UseSpec{
					Caller: "bob", Operation: OpDecrypt, KeyID: "k1", KeyVersion: 1,
					Context: Context{"env": "prod"}, Token: w.tokenFor("g1"),
				}
			},
		},
		{
			name:   "Use/missing-subset",
			detail: "bob Decrypt g2 with env key omitted",
			build: func(w *World) UseSpec {
				return UseSpec{
					Caller: "bob", Operation: OpDecrypt, KeyID: "k1", KeyVersion: 1,
					Context: Context{"trace": "x"}, Token: w.tokenFor("g2"),
				}
			},
		},
		{
			name:   "Use/extra-context",
			detail: "bob Decrypt g1 with extra key trace=x",
			build: func(w *World) UseSpec {
				return UseSpec{
					Caller: "bob", Operation: OpDecrypt, KeyID: "k1", KeyVersion: 1,
					Context: Context{"env": "prod", "trace": "x"}, Token: w.tokenFor("g1"),
				}
			},
		},
		{
			name:   "Use/cross-principal",
			detail: "mallory replays bob's g1 token",
			build: func(w *World) UseSpec {
				return UseSpec{
					Caller: "mallory", Operation: OpDecrypt, KeyID: "k1", KeyVersion: 1,
					Context: Context{"env": "prod"}, Token: w.tokenFor("g1"),
				}
			},
		},
		{
			name:   "Use/wrong-operation",
			detail: "bob calls Sign with Decrypt-only grant",
			build: func(w *World) UseSpec {
				return UseSpec{
					Caller: "bob", Operation: OpSign, KeyID: "k1", KeyVersion: 1,
					Context: Context{"env": "prod"}, Token: w.tokenFor("g1"),
				}
			},
		},
		{
			name:   "Use/rotated-version",
			detail: "bob Decrypt k1/v2 after key rotation",
			build: func(w *World) UseSpec {
				return UseSpec{
					Caller: "bob", Operation: OpDecrypt, KeyID: "k1", KeyVersion: 2,
					Context: Context{"env": "prod"}, Token: w.tokenFor("g1"),
				}
			},
		},
		{
			name:   "Use/pending-cross-client",
			detail: "mallory uses pending g1 token with r1",
			build: func(w *World) UseSpec {
				return UseSpec{
					Caller: "mallory", Operation: OpDecrypt, KeyID: "k1", KeyVersion: 1,
					Context: Context{"env": "prod"}, Token: w.pendingTokenFor("g1"),
					RequestID: "r1", AllowPending: true,
				}
			},
		},
		{
			name:   "Use/pending-legit",
			detail: "alice uses pending g1 token inside window",
			build: func(w *World) UseSpec {
				return UseSpec{
					Caller: "alice", Operation: OpDecrypt, KeyID: "k1", KeyVersion: 1,
					Context: Context{"env": "prod"}, Token: w.pendingTokenFor("g1"),
					RequestID: "r1", AllowPending: true,
				}
			},
		},
	}
	for _, ua := range useActions {
		ua := ua
		out = append(out, action{
			name:   ua.name,
			detail: ua.detail,
			apply: func(w *World) (bool, bool) {
				return w.Use(ua.build(w)), true
			},
		})
	}

	if w.policy.NonAtomicRetire {
		out = append(out,
			action{
				name:   "BeginRetire(g1)",
				detail: "carol flips g1 to Retired (generation not yet bumped)",
				apply: func(w *World) (bool, bool) {
					err := w.BeginRetire("carol", "g1")
					return err == nil, false
				},
			},
			action{
				name:   "FinishRetire",
				detail: "deferred generation increment",
				apply: func(w *World) (bool, bool) {
					err := w.FinishRetire()
					return err == nil, false
				},
			},
		)
	} else {
		out = append(out, action{
			name:   "Retire(g1)",
			detail: "carol retires g1 atomically",
			apply: func(w *World) (bool, bool) {
				err := w.Retire("carol", "g1")
				return err == nil, false
			},
		})
	}

	// Clock advance covers pending-window expiry and token expiry.
	out = append(out,
		action{
			name:   "Advance(2m)",
			detail: "host clock moves past pending window",
			apply: func(w *World) (bool, bool) {
				w.Advance(2 * time.Minute)
				return false, false
			},
		},
		action{
			name:   "Advance(2h)",
			detail: "host clock moves past token TTL",
			apply: func(w *World) (bool, bool) {
				w.Advance(2 * time.Hour)
				return false, false
			},
		},
	)

	return out
}

func (w *World) tokenFor(grantID string) *Token {
	rec, ok := w.grants[grantID]
	if !ok {
		return nil
	}
	return rec.latestToken()
}

func (w *World) pendingTokenFor(grantID string) *Token {
	rec, ok := w.grants[grantID]
	if !ok {
		return nil
	}
	for _, t := range rec.tokens {
		if t.Kind == pendingKind {
			return t
		}
	}
	return nil
}

func fingerprint(w *World) string {
	var b strings.Builder
	fmt.Fprintf(&b, "t=%d|", w.now.Unix())
	for id, rec := range w.grants {
		g := rec.grant
		fmt.Fprintf(&b, "%s{%s/%d st=%d gen=%d ops=%v eq=%v sub=%v grantee=%s retire=%s ntok=%d}",
			id, g.KeyID, g.KeyVersion, g.Status, g.Generation, g.Operations,
			g.Constraints, g.Subset, g.GranteePrincipal, g.RetiringPrincipal, len(rec.tokens))
		if rec.pending != nil {
			fmt.Fprintf(&b, "pend(%s,%s,until=%d,allowed=%v)",
				rec.pending.creator, rec.pending.requestID, rec.pending.deadline.Unix(), rec.pending.allowed)
		}
	}
	if w.inFlightRetire != nil {
		fmt.Fprintf(&b, "inflight=%s/%d", w.inFlightRetire.grantID, w.inFlightRetire.step)
	}
	return b.String()
}

// RenderTrace formats a finding for reports.
func RenderTrace(f *Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "weakness=%s\n", f.Policy.Name())
	for i, s := range f.Trace {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, s.String())
	}
	return b.String()
}
