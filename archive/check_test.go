package archive

import (
	"errors"
	"testing"
)

func clonePlanForTest(p *Plan) *Plan {
	cp := *p
	cp.Requests = append([]Request(nil), p.Requests...)
	cp.Steps = append([]Step(nil), p.Steps...)
	cp.Sources = ClonePiecesSlice(p.Sources)
	cp.InitialCache = append([]BlockID(nil), p.InitialCache...)
	cp.RequiredBlocks = append([]BlockID(nil), p.RequiredBlocks...)
	return &cp
}

func TestCheckPlanAcceptsAndRecomputesAccounts(t *testing.T) {
	m, _ := concatRawManifest(t, 4, 4)
	g := NewGrant("tenant", Interval{0, 8})
	batch := []Request{{Start: 0, Length: 4}, {Start: 4, Length: 4}}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	audit, err := CheckPlan(m, g, batch, p)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if audit.DecodeBlocks != 2 || audit.Evictions != 1 {
		t.Fatalf("audit decode=%d evict=%d", audit.DecodeBlocks, audit.Evictions)
	}
	if audit.ReturnBytes != 8 {
		t.Fatalf("audit return=%d", audit.ReturnBytes)
	}
	if len(audit.DecodedBlocks) != 2 {
		t.Fatalf("decoded block log = %v", audit.DecodedBlocks)
	}
}

func TestCheckerCatchesTampering(t *testing.T) {
	m, _ := concatRawManifest(t, 4, 4)
	g := NewGrant("tenant", Interval{0, 8})
	batch := []Request{{Start: 0, Length: 4}, {Start: 4, Length: 4}}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Plan)
		want   error
	}{
		{"decode account", func(p *Plan) { p.DecodeBlocks++ }, ErrInvalidPlan},
		{"return account", func(p *Plan) { p.ReturnBytes-- }, ErrInvalidPlan},
		{"required blocks", func(p *Plan) { p.RequiredBlocks = p.RequiredBlocks[:1] }, ErrInvalidPlan},
		{"source segment", func(p *Plan) {
			p.Sources[0] = append(ClonePieces(p.Sources[0]), leafPiece(1, 0, 1, false))
		}, ErrInvalidPlan},
		{"unknown load", func(p *Plan) {
			for i := range p.Steps {
				if p.Steps[i].Kind == StepLoad {
					p.Steps[i].Block = 999
					break
				}
			}
		}, ErrInvalidPlan},
		{"missing emit", func(p *Plan) {
			for i := len(p.Steps) - 1; i >= 0; i-- {
				if p.Steps[i].Kind == StepEmit {
					p.Steps = append(p.Steps[:i], p.Steps[i+1:]...)
					break
				}
			}
		}, ErrInvalidPlan},
		{"eviction victim", func(p *Plan) {
			for i := range p.Steps {
				if p.Steps[i].Kind == StepLoad && p.Steps[i].Evict {
					p.Steps[i].Victim = 999
					break
				}
			}
		}, ErrInvalidPlan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp := clonePlanForTest(p)
			tc.mutate(cp)
			_, err := CheckPlan(m, g, batch, cp)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCheckerRejectsUnauthorizedExtraLoad(t *testing.T) {
	m, _ := concatRawManifest(t, 4, 4)
	g := NewGrant("tenant", Interval{0, 4})
	batch := []Request{{Start: 0, Length: 4}}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(2))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	cp := clonePlanForTest(p)
	cp.Steps = append([]Step{{Kind: StepLoad, Block: 2}}, cp.Steps...)
	if _, err := CheckPlan(m, g, batch, cp); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("extra unauthorized decode should be rejected, got %v", err)
	}
}

func TestCheckerRejectsWrongManifestAndStaleInitialPages(t *testing.T) {
	m, pages := concatRawManifest(t, 4)
	g := NewGrant("tenant", Interval{0, 4})
	batch := []Request{{Start: 0, Length: 4}}
	cache := NewPageCache(1, m.Version, m.StrategyDigest)
	cache.Put(1, pages[1])
	cp := cache.Snapshot()
	opts := DefaultPlanOptions(1)
	opts.Checkpoint = &cp
	p, err := PlanBatch(m, g, batch, opts)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := CheckPlan(m, g, batch, p); err != nil {
		t.Fatalf("clean plan: %v", err)
	}

	bad := clonePlanForTest(p)
	bad.CheckpointStale = true
	if _, err := CheckPlan(m, g, batch, bad); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("stale initial pages should be invalid, got %v", err)
	}

	badm := m.Clone()
	badm.Signature[0] ^= 1
	if _, err := CheckPlan(badm, g, batch, p); !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("bad manifest should be insufficient evidence, got %v", err)
	}
}

func TestCheckerUsesExternalLimits(t *testing.T) {
	m, _ := concatRawManifest(t, 4, 4)
	g := NewGrant("tenant", Interval{0, 8})
	batch := []Request{{Start: 0, Length: 4}, {Start: 4, Length: 4}}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(2))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	strict := p.Limits
	strict.CacheCapacity = 1
	if _, err := CheckPlanWithLimits(m, g, batch, p, strict); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("plan that exceeds an external cache capacity should fail, got %v", err)
	}
	strict = p.Limits
	strict.ReturnBytes = 7
	if _, err := CheckPlanWithLimits(m, g, batch, p, strict); !errors.Is(err, ErrReturnQuota) {
		t.Fatalf("external return quota should be enforced, got %v", err)
	}
}
