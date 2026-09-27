package dte

import (
	"fmt"
	"math"
	"sort"
)

// BucketSpec defines one node in the hierarchical token tree.
type BucketSpec struct {
	Name   string
	Parent string
	Cap    int64
	Rate   Rate
}

// FlowSpec binds a strict-priority FIFO queue to a leaf bucket.
type FlowSpec struct {
	Name     string
	Bucket   string
	Priority int
	Buffer   int64
}

// Config contains the static tree, queues, and output link.
type Config struct {
	Mode    Mode
	Buckets []BucketSpec
	Flows   []FlowSpec
	LinkRate int64
	Horizon int64
}

// Scenario is a validated configuration plus an ordered event script.
type Scenario struct {
	Config Config
	Events []Event
}

// NewScenario creates an empty scenario.
func NewScenario(cfg Config) *Scenario {
	return &Scenario{Config: cfg}
}

// Add appends one event and returns the scenario for builder-style use.
func (s *Scenario) Add(event Event) *Scenario {
	s.Events = append(s.Events, event)
	return s
}

type staticModel struct {
	cfg         Config
	events      []Event
	bucketIndex map[string]int
	flowIndex   map[string]int
	paths       [][]int
	flowOrder   []int
	lcm         int64
}

func (s *Scenario) build() (*staticModel, error) {
	cfg := s.Config
	if cfg.Horizon <= 0 {
		return nil, fmt.Errorf("horizon must be positive")
	}
	if cfg.LinkRate <= 0 {
		return nil, fmt.Errorf("link rate must be positive")
	}
	if len(cfg.Buckets) == 0 {
		return nil, fmt.Errorf("at least one bucket is required")
	}
	if len(cfg.Flows) == 0 {
		return nil, fmt.Errorf("at least one flow is required")
	}

	m := &staticModel{
		cfg:         cfg,
		bucketIndex: make(map[string]int),
		flowIndex:   make(map[string]int),
		lcm:         1,
	}

	for i, bucket := range cfg.Buckets {
		if bucket.Name == "" {
			return nil, fmt.Errorf("bucket %d has empty name", i)
		}
		if _, exists := m.bucketIndex[bucket.Name]; exists {
			return nil, fmt.Errorf("duplicate bucket %q", bucket.Name)
		}
		if bucket.Cap <= 0 {
			return nil, fmt.Errorf("bucket %q capacity must be positive", bucket.Name)
		}
		if err := validateRate(bucket.Rate); err != nil {
			return nil, fmt.Errorf("bucket %q: %v", bucket.Name, err)
		}
		m.bucketIndex[bucket.Name] = i
		next, err := lcm64(m.lcm, bucket.Rate.Den)
		if err != nil {
			return nil, fmt.Errorf("bucket %q rate denominator: %v", bucket.Name, err)
		}
		m.lcm = next
	}

	// Build paths and validate acyclic parent links.
	m.paths = make([][]int, len(cfg.Buckets))
	for i := range cfg.Buckets {
		path, err := buildPath(i, cfg.Buckets, m.bucketIndex)
		if err != nil {
			return nil, err
		}
		m.paths[i] = path
	}

	for i, flow := range cfg.Flows {
		if flow.Name == "" {
			return nil, fmt.Errorf("flow %d has empty name", i)
		}
		if _, exists := m.flowIndex[flow.Name]; exists {
			return nil, fmt.Errorf("duplicate flow %q", flow.Name)
		}
		leaf, ok := m.bucketIndex[flow.Bucket]
		if !ok {
			return nil, fmt.Errorf("flow %q references unknown bucket %q", flow.Name, flow.Bucket)
		}
		if flow.Buffer <= 0 {
			return nil, fmt.Errorf("flow %q buffer must be positive", flow.Name)
		}
		m.flowIndex[flow.Name] = i
		m.paths[leaf] = append([]int(nil), m.paths[leaf]...)
	}

	m.flowOrder = make([]int, len(cfg.Flows))
	for i := range m.flowOrder {
		m.flowOrder[i] = i
	}
	sort.SliceStable(m.flowOrder, func(i, j int) bool {
		return cfg.Flows[m.flowOrder[i]].Priority < cfg.Flows[m.flowOrder[j]].Priority
	})

	seenArrivals := make(map[int]int64)
	m.events = append([]Event(nil), s.Events...)
	eventRates := make(map[int][]Rate)
	for i, event := range m.events {
		if event.Tick < 0 || event.Tick > cfg.Horizon {
			return nil, fmt.Errorf("event %d at tick %d outside [0,%d]", i, event.Tick, cfg.Horizon)
		}
		switch event.Kind {
		case Arrive:
			if _, ok := m.flowIndex[event.Flow]; !ok {
				return nil, fmt.Errorf("event %d references unknown flow %q", i, event.Flow)
			}
			if event.Bytes <= 0 {
				return nil, fmt.Errorf("event %d packet bytes must be positive", i)
			}
			if firstTick, exists := seenArrivals[event.ID]; exists {
				return nil, fmt.Errorf("event %d reuses packet id %d first seen at %d", i, event.ID, firstTick)
			}
			seenArrivals[event.ID] = event.Tick
		case Cancel:
			if _, ok := m.flowIndex[event.Flow]; !ok {
				return nil, fmt.Errorf("event %d references unknown flow %q", i, event.Flow)
			}
			firstTick, exists := seenArrivals[event.ID]
			if !exists || firstTick >= event.Tick {
				return nil, fmt.Errorf("event %d cancels packet %d before an earlier arrival", i, event.ID)
			}
		case SetRateEvent:
			if _, ok := m.bucketIndex[event.Bucket]; !ok {
				return nil, fmt.Errorf("event %d references unknown bucket %q", i, event.Bucket)
			}
			if err := validateRate(event.Rate); err != nil {
				return nil, fmt.Errorf("event %d: %v", i, err)
			}
			bucket := m.bucketIndex[event.Bucket]
			eventRates[bucket] = append(eventRates[bucket], event.Rate)
		case Pause, Resume:
			if _, ok := m.bucketIndex[event.Bucket]; !ok {
				return nil, fmt.Errorf("event %d references unknown bucket %q", i, event.Bucket)
			}
		default:
			return nil, fmt.Errorf("event %d has unknown kind", i)
		}
	}
	sort.SliceStable(m.events, func(i, j int) bool { return m.events[i].Tick < m.events[j].Tick })

	// Runtime weight changes must use the same exact integer scale.
	for _, rates := range eventRates {
		for _, rate := range rates {
			next, err := lcm64(m.lcm, rate.Den)
			if err != nil {
				return nil, fmt.Errorf("runtime rate denominator: %v", err)
			}
			m.lcm = next
		}
	}

	// Fail early if exact integer scaling would overflow.
	for _, event := range m.events {
		if event.Kind == Arrive {
			if _, err := mulCheck(event.Bytes, m.lcm); err != nil {
				return nil, fmt.Errorf("packet %d too large for exact scale: %v", event.ID, err)
			}
		}
	}
	for _, bucket := range cfg.Buckets {
		if _, err := mulCheck(bucket.Cap, m.lcm); err != nil {
			return nil, fmt.Errorf("bucket %q: %v", bucket.Name, err)
		}
		rateMicro, err := scaleRate(bucket.Rate, m.lcm)
		if err != nil {
			return nil, fmt.Errorf("bucket %q: %v", bucket.Name, err)
		}
		_ = rateMicro
	}
	return m, nil
}

func validateRate(rate Rate) error {
	if rate.Den <= 0 {
		return fmt.Errorf("rate denominator must be positive")
	}
	if rate.Num < 0 {
		return fmt.Errorf("rate numerator must be non-negative")
	}
	return nil
}

func buildPath(start int, buckets []BucketSpec, index map[string]int) ([]int, error) {
	path := []int{start}
	seen := map[int]bool{start: true}
	current := start
	for buckets[current].Parent != "" {
		parentName := buckets[current].Parent
		parent, ok := index[parentName]
		if !ok {
			return nil, fmt.Errorf("bucket %q references unknown parent %q", buckets[current].Name, parentName)
		}
		if seen[parent] {
			return nil, fmt.Errorf("bucket parent cycle includes %q", buckets[parent].Name)
		}
		seen[parent] = true
		path = append(path, parent)
		current = parent
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path, nil
}

func gcd64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

func lcm64(a, b int64) (int64, error) {
	g := gcd64(a, b)
	ag := a / g
	if b > math.MaxInt64/ag {
		return 0, fmt.Errorf("rate scale overflow")
	}
	return ag * b, nil
}

func mulCheck(a, b int64) (int64, error) {
	if a > math.MaxInt64/b {
		return 0, fmt.Errorf("integer overflow")
	}
	return a * b, nil
}

func scaleRate(rate Rate, scale int64) (int64, error) {
	return mulCheck(rate.Num, scale/rate.Den)
}
