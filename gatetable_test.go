package gatetable_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	gt "prescreen.local/ps-282b6ff8a3"
)

type bruteVar struct {
	frame, egress, port, dur int
}

func bruteVars(p gt.Problem) []bruteVar {
	var out []bruteVar
	for fi, f := range p.Frames {
		for i, port := range f.Ports {
			out = append(out, bruteVar{frame: fi, egress: i, port: port, dur: f.Durations[i]})
		}
	}
	return out
}

func brutePeakSlots(p gt.Problem, latencies []int) int {
	deltas := map[int]int{}
	for fi, f := range p.Frames {
		for k := -3; k <= 3; k++ {
			start := f.Release + k*p.Period
			deltas[start]++
			deltas[start+latencies[fi]]--
		}
	}
	times := make([]int, 0, len(deltas))
	for t := range deltas {
		times = append(times, t)
	}
	sort.Ints(times)
	cur, best := 0, 0
	for _, t := range times {
		cur += deltas[t]
		if cur > best {
			best = cur
		}
	}
	return best
}

func bruteCheck(p gt.Problem, offsets []int) (latency int, slots, entries int, ok bool) {
	vars := bruteVars(p)
	used := make([][]bool, len(p.Ports))
	for pi := range used {
		used[pi] = make([]bool, p.Period)
	}
	latencies := make([]int, len(p.Frames))
	masks := make([]uint8, p.Period)

	for vi, v := range vars {
		f := p.Frames[v.frame]
		o := offsets[vi]
		if o+v.dur > f.Deadline {
			return 0, 0, 0, false
		}
		phase := (f.Release + o) % p.Period
		for i := 0; i < v.dur; i++ {
			t := (phase + i) % p.Period
			if p.Ports[v.port].Forbidden[t] || used[v.port][t] {
				return 0, 0, 0, false
			}
			used[v.port][t] = true
			masks[t] |= uint8(1) << uint(v.port)
		}
		if o+v.dur > latencies[v.frame] {
			latencies[v.frame] = o + v.dur
		}
	}
	for f := range latencies {
		if latencies[f] > p.Frames[f].Deadline {
			return 0, 0, 0, false
		}
		if latencies[f] > latency {
			latency = latencies[f]
		}
	}
	slots = brutePeakSlots(p, latencies)
	entries = gt.CountTableEntries(masks)
	if p.MaxSlots != gt.Unlimited && slots > p.MaxSlots {
		return 0, 0, 0, false
	}
	if p.MaxTableEntries != gt.Unlimited && entries > p.MaxTableEntries {
		return 0, 0, 0, false
	}
	return latency, slots, entries, true
}

func lexLess(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func bruteSolve(p gt.Problem) (best []int, latency int, ok bool) {
	vars := bruteVars(p)
	cur := make([]int, len(vars))
	for i := range cur {
		cur[i] = -1
	}
	var rec func(int)
	rec = func(i int) {
		if i == len(vars) {
			lat, _, _, feasible := bruteCheck(p, cur)
			if !feasible {
				return
			}
			if !ok || lat < latency || (lat == latency && lexLess(cur, best)) {
				ok = true
				latency = lat
				best = append([]int(nil), cur...)
			}
			return
		}
		for o := 0; o < p.Period; o++ {
			cur[i] = o
			rec(i + 1)
		}
		cur[i] = -1
	}
	rec(0)
	return best, latency, ok
}

func flattenOffsets(res *gt.Result) []int {
	var out []int
	for _, frame := range res.Offsets {
		out = append(out, frame...)
	}
	return out
}

func TestBruteForceOptimalShortPeriods(t *testing.T) {
	cases := []gt.Problem{
		{
			Period:          4,
			Ports:           []gt.PortSpec{{Forbidden: make([]bool, 4)}},
			Frames:          []gt.FrameSpec{{Release: 0, Ports: []int{0}, Durations: []int{1}, Deadline: 4}, {Release: 0, Ports: []int{0}, Durations: []int{1}, Deadline: 4}},
			MaxSlots:        2,
			MaxTableEntries: 2,
		},
		{
			Period: 5,
			Ports: []gt.PortSpec{
				{Forbidden: []bool{false, true, false, false, false}},
				{Forbidden: make([]bool, 5)},
			},
			Frames: []gt.FrameSpec{
				{Release: 1, Ports: []int{0, 1}, Durations: []int{2, 1}, Deadline: 6},
				{Release: 3, Ports: []int{1}, Durations: []int{2}, Deadline: 7},
			},
			MaxSlots:        3,
			MaxTableEntries: 4,
		},
		{
			Period: 3,
			Ports:  []gt.PortSpec{{Forbidden: make([]bool, 3)}},
			Frames: []gt.FrameSpec{
				{Release: 0, Ports: []int{0}, Durations: []int{1}, Deadline: 3},
				{Release: 1, Ports: []int{0}, Durations: []int{1}, Deadline: 3},
				{Release: 2, Ports: []int{0}, Durations: []int{1}, Deadline: 3},
			},
			MaxSlots:        3,
			MaxTableEntries: 2,
		},
	}

	for i, p := range cases {
		wantOff, wantLat, wantOK := bruteSolve(p)
		got, err := gt.Compile(p)
		if !wantOK {
			if err == nil {
				t.Fatalf("case %d: brute force found no schedule, Compile returned %+v", i, got)
			}
			if !errors.Is(err, gt.ErrUnschedulable) {
				t.Fatalf("case %d: got error %v, want ErrUnschedulable", i, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %d: Compile failed: %v (brute offsets %v)", i, err, wantOff)
		}
		if got.Latency != wantLat {
			t.Fatalf("case %d: latency %d, want %d", i, got.Latency, wantLat)
		}
		if gotOff := flattenOffsets(got); !equalInts(gotOff, wantOff) {
			t.Fatalf("case %d: offsets %v, want %v", i, gotOff, wantOff)
		}
		_, wantSlots, wantEntries, _ := bruteCheck(p, wantOff)
		if got.SlotsUsed != wantSlots || got.TableEntries != wantEntries {
			t.Fatalf("case %d: slots/entries %d/%d, want %d/%d",
				i, got.SlotsUsed, got.TableEntries, wantSlots, wantEntries)
		}
	}
}

func TestRandomShortPeriodsAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(282))
	checked := 0
	for attempt := 0; attempt < 400 && checked < 120; attempt++ {
		p := randomProblem(rng)
		vars := bruteVars(p)
		if len(vars) > 5 {
			continue
		}
		wantOff, wantLat, wantOK := bruteSolve(p)
		got, err := gt.Compile(p)
		if !wantOK {
			if err == nil {
				t.Fatalf("problem %+v: brute force infeasible, Compile returned %+v", p, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("problem %+v: Compile failed: %v; brute offsets %v", p, err, wantOff)
		}
		if got.Latency != wantLat || !equalInts(flattenOffsets(got), wantOff) {
			t.Fatalf("problem %+v: got latency %d offsets %v, want %d %v",
				p, got.Latency, flattenOffsets(got), wantLat, wantOff)
		}
		_, wantSlots, wantEntries, _ := bruteCheck(p, wantOff)
		if got.SlotsUsed != wantSlots || got.TableEntries != wantEntries {
			t.Fatalf("problem %+v: got slots/entries %d/%d, want %d/%d",
				p, got.SlotsUsed, got.TableEntries, wantSlots, wantEntries)
		}
		checked++
	}
	if checked < 40 {
		t.Fatalf("only %d feasible random cases checked", checked)
	}
}

func randomProblem(rng *rand.Rand) gt.Problem {
	period := 3 + rng.Intn(3) // 3..5
	portCount := 1 + rng.Intn(2)
	p := gt.Problem{
		Period:          period,
		Ports:           make([]gt.PortSpec, portCount),
		MaxSlots:        1 + rng.Intn(3),
		MaxTableEntries: 1 + rng.Intn(5),
	}
	for i := range p.Ports {
		p.Ports[i].Forbidden = make([]bool, period)
		for t := 0; t < period; t++ {
			p.Ports[i].Forbidden[t] = rng.Intn(6) == 0
		}
	}
	frameCount := 1 + rng.Intn(3)
	for fi := 0; fi < frameCount; fi++ {
		egress := 1 + rng.Intn(portCount)
		perm := rng.Perm(portCount)
		f := gt.FrameSpec{Name: fmt.Sprintf("f%d", fi), Release: rng.Intn(period)}
		for i := 0; i < egress; i++ {
			f.Ports = append(f.Ports, perm[i])
			f.Durations = append(f.Durations, 1+rng.Intn(period))
		}
		maxDur := 0
		for _, d := range f.Durations {
			if d > maxDur {
				maxDur = d
			}
		}
		f.Deadline = maxDur + rng.Intn(2*period-maxDur+1)
		p.Frames = append(p.Frames, f)
	}
	return p
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCircularMaskMergeAtBoundary(t *testing.T) {
	masks := []uint8{1, 1, 0, 0, 0, 0, 1, 1}
	if got := gt.CountTableEntries(masks); got != 2 {
		t.Fatalf("boundary masks: %d entries, want 2", got)
	}
	spans := gt.CompressMasks(masks)
	if len(spans) != 2 {
		t.Fatalf("spans %+v, want 2 entries", spans)
	}
	var wrapped *gt.MaskSpan
	for i := range spans {
		if spans[i].Mask == 1 {
			wrapped = &spans[i]
		}
	}
	if wrapped == nil || wrapped.Start != 6 || wrapped.Length != 4 {
		t.Fatalf("spans %+v, want wrapped one-run at tick 6 length 4", spans)
	}

	p := gt.Problem{
		Period: 8,
		Ports: []gt.PortSpec{{
			Forbidden: []bool{false, false, true, true, true, true, false, false},
		}},
		Frames: []gt.FrameSpec{{
			Release: 0, Ports: []int{0}, Durations: []int{4}, Deadline: 10,
		}},
		MaxSlots:        2,
		MaxTableEntries: 2,
	}
	res, err := gt.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !equalInts(flattenOffsets(res), []int{6}) {
		t.Fatalf("offsets %v, want [6]", flattenOffsets(res))
	}
	if res.TableEntries != 2 || len(res.Spans) != 2 {
		t.Fatalf("entries %d spans %+v, want 2 compressed entries", res.TableEntries, res.Spans)
	}

	p.MaxTableEntries = 1
	if _, err := gt.Compile(p); !errors.Is(err, gt.ErrUnschedulable) {
		t.Fatalf("one-entry budget: got %v, want ErrUnschedulable", err)
	}
}

func TestAdjacentGenerationsCoexist(t *testing.T) {
	p := gt.Problem{
		Period: 8,
		Ports: []gt.PortSpec{{
			Forbidden: []bool{false, true, true, false, false, false, false, false},
		}},
		Frames: []gt.FrameSpec{{
			Release: 0, Ports: []int{0}, Durations: []int{6}, Deadline: 9,
		}},
		MaxSlots:        2,
		MaxTableEntries: 2,
	}
	res, err := gt.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !equalInts(flattenOffsets(res), []int{3}) {
		t.Fatalf("offsets %v, want [3]", flattenOffsets(res))
	}
	if res.SlotsUsed != 2 {
		t.Fatalf("slots %d, want 2 adjacent generations", res.SlotsUsed)
	}
	replay := res.Replay(2)
	if replay[0].ActiveSlots != 2 {
		t.Fatalf("tick 0 active slots %d, want 2 (carry-in and new generation)",
			replay[0].ActiveSlots)
	}
	if len(replay[1].SlotReleases) != 1 || replay[1].SlotReleases[0].Instance != -1 {
		t.Fatalf("tick 1 releases %+v, want previous generation", replay[1].SlotReleases)
	}
	if replay[8].ActiveSlots != 2 {
		t.Fatalf("tick 8 active slots %d, want 2", replay[8].ActiveSlots)
	}
	if len(replay[9].SlotReleases) != 1 || replay[9].SlotReleases[0].Instance != 0 {
		t.Fatalf("tick 9 releases %+v, want generation 0", replay[9].SlotReleases)
	}
	if replay[0].PortMask != 1 || replay[1].PortMask != 0 || replay[2].PortMask != 0 {
		t.Fatalf("unexpected masks at ticks 0..2: %d,%d,%d",
			replay[0].PortMask, replay[1].PortMask, replay[2].PortMask)
	}
}

func TestLocalPortsFeasibleButSharedSlotsExceed(t *testing.T) {
	onePortProblem := func(port int) gt.Problem {
		forbidden := []bool{false, true, true, true, true, true, true, true}
		return gt.Problem{
			Period: 8,
			Ports:  []gt.PortSpec{{Forbidden: forbidden}},
			Frames: []gt.FrameSpec{{
				Release: 0, Ports: []int{0}, Durations: []int{1}, Deadline: 1,
			}},
			MaxSlots:        1,
			MaxTableEntries: 2,
		}
	}
	for port := 0; port < 2; port++ {
		if _, err := gt.Compile(onePortProblem(port)); err != nil {
			t.Fatalf("port %d alone should be feasible: %v", port, err)
		}
	}

	forbidden := []bool{false, true, true, true, true, true, true, true}
	p := gt.Problem{
		Period: 8,
		Ports: []gt.PortSpec{
			{Forbidden: append([]bool(nil), forbidden...)},
			{Forbidden: append([]bool(nil), forbidden...)},
		},
		Frames: []gt.FrameSpec{
			{Release: 0, Ports: []int{0}, Durations: []int{1}, Deadline: 1},
			{Release: 0, Ports: []int{1}, Durations: []int{1}, Deadline: 1},
		},
		MaxSlots:        1,
		MaxTableEntries: 2,
	}
	if _, err := gt.Compile(p); !errors.Is(err, gt.ErrUnschedulable) {
		t.Fatalf("combined budget: got %v, want ErrUnschedulable", err)
	}

	p.MaxSlots = 2
	res, err := gt.Compile(p)
	if err != nil {
		t.Fatalf("combined feasible budget: %v", err)
	}
	if res.SlotsUsed != 2 || res.TableEntries != 2 {
		t.Fatalf("slots/entries %d/%d, want 2/2", res.SlotsUsed, res.TableEntries)
	}
}

func TestStaticTableZeroIdleTicks(t *testing.T) {
	p := gt.Problem{
		Period: 8,
		Ports: []gt.PortSpec{
			{Forbidden: make([]bool, 8)},
			{Forbidden: make([]bool, 8)},
			{Forbidden: make([]bool, 8)},
			{Forbidden: make([]bool, 8)},
		},
		Frames: []gt.FrameSpec{{
			Release: 0, Ports: []int{0, 1, 2, 3},
			Durations: []int{8, 8, 8, 8}, Deadline: 8,
		}},
		MaxSlots:        1,
		MaxTableEntries: 1,
	}
	res, err := gt.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if res.TableEntries != 1 || len(res.Spans) != 1 {
		t.Fatalf("entries/spans %d/%d, want 1/1", res.TableEntries, len(res.Spans))
	}
	if span := res.Spans[0]; span.Start != 0 || span.Length != 8 || span.Mask != 0x0f {
		t.Fatalf("span %+v, want {0 8 0x0f}", span)
	}
	for _, mask := range res.Masks {
		if mask != 0x0f {
			t.Fatalf("mask %#x, want 0x0f at every tick", mask)
		}
	}
	replay := res.Replay(2)
	for tick, rt := range replay {
		if rt.PortMask != 0x0f || rt.ActiveSlots != 1 {
			t.Fatalf("tick %d: mask %#x slots %d, want 0x0f/1",
				tick, rt.PortMask, rt.ActiveSlots)
		}
	}
	if len(replay[0].Starts) != 4 {
		t.Fatalf("tick 0 starts %d, want 4 simultaneous copies", len(replay[0].Starts))
	}
	if len(replay[8].SlotReleases) != 1 || len(replay[8].SlotAcquires) != 1 {
		t.Fatalf("tick 8 slot events: acquires %+v releases %+v",
			replay[8].SlotAcquires, replay[8].SlotReleases)
	}
}

func TestForbiddenTicksAndDeadline(t *testing.T) {
	p := gt.Problem{
		Period: 8,
		Ports: []gt.PortSpec{{
			Forbidden: []bool{true, true, false, false, false, false, false, false},
		}},
		Frames: []gt.FrameSpec{{
			Release: 0, Ports: []int{0}, Durations: []int{2}, Deadline: 4,
		}},
		MaxSlots:        1,
		MaxTableEntries: 2,
	}
	res, err := gt.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !equalInts(flattenOffsets(res), []int{2}) {
		t.Fatalf("offsets %v, want [2]", flattenOffsets(res))
	}
	p.Frames[0].Deadline = 1
	if _, err := gt.Compile(p); !errors.Is(err, gt.ErrUnschedulable) {
		t.Fatalf("deadline 1: got %v, want ErrUnschedulable", err)
	}
}

func TestSlotHeldUntilLastPortCopyFinishes(t *testing.T) {
	p := gt.Problem{
		Period: 10,
		Ports: []gt.PortSpec{
			{Forbidden: make([]bool, 10)},
			{Forbidden: make([]bool, 10)},
		},
		Frames: []gt.FrameSpec{{
			Release: 2, Ports: []int{0, 1},
			Durations: []int{2, 6}, Deadline: 6,
		}},
		MaxSlots:        1,
		MaxTableEntries: 3,
	}
	res, err := gt.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	replay := res.Replay(1)
	if len(replay[2].Starts) != 2 {
		t.Fatalf("tick 2 starts %+v, want simultaneous copies on both ports",
			replay[2].Starts)
	}
	if replay[7].ActiveSlots != 1 {
		t.Fatalf("tick 7 slots %d, want slot still held", replay[7].ActiveSlots)
	}
	if len(replay[8].SlotReleases) != 1 || replay[8].ActiveSlots != 0 {
		t.Fatalf("tick 8 release %+v active %d, want release after both copies",
			replay[8].SlotReleases, replay[8].ActiveSlots)
	}
}

func TestLexicographicOffsetTieBreak(t *testing.T) {
	p := gt.Problem{
		Period:          4,
		Ports:           []gt.PortSpec{{Forbidden: make([]bool, 4)}},
		Frames:          []gt.FrameSpec{{Release: 0, Ports: []int{0}, Durations: []int{1}, Deadline: 4}, {Release: 0, Ports: []int{0}, Durations: []int{1}, Deadline: 4}},
		MaxSlots:        2,
		MaxTableEntries: 2,
	}
	res, err := gt.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !equalInts(flattenOffsets(res), []int{0, 1}) {
		t.Fatalf("offsets %v, want lexicographic [0 1]", flattenOffsets(res))
	}
	if res.Latency != 2 {
		t.Fatalf("latency %d, want 2", res.Latency)
	}
}

func TestReplayConsistency(t *testing.T) {
	p := gt.Problem{
		Period: 9,
		Ports: []gt.PortSpec{
			{Forbidden: make([]bool, 9)},
			{Forbidden: []bool{false, false, false, true, false, false, false, false, false}},
		},
		Frames: []gt.FrameSpec{
			{Release: 1, Ports: []int{0, 1}, Durations: []int{4, 3}, Deadline: 10},
			{Release: 5, Ports: []int{0}, Durations: []int{2}, Deadline: 6},
		},
		MaxSlots:        3,
		MaxTableEntries: 6,
	}
	res, err := gt.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	const cycles = 3
	replay := res.Replay(cycles)
	end := cycles * p.Period
	for tick := 0; tick < end; tick++ {
		active := 0
		for fi, f := range p.Frames {
			l := res.FrameLatencies[fi]
			for k := -3; k <= cycles+1; k++ {
				start := f.Release + k*p.Period
				if start <= tick && tick < start+l {
					active++
				}
			}
		}
		if replay[tick].ActiveSlots != active {
			t.Fatalf("tick %d: replay slots %d, independent count %d",
				tick, replay[tick].ActiveSlots, active)
		}
		if replay[tick].PortMask != res.Masks[tick%p.Period] {
			t.Fatalf("tick %d: mask %#x, want %#x", tick, replay[tick].PortMask, res.Masks[tick%p.Period])
		}
	}
	for tick, rt := range replay {
		for _, cp := range rt.Starts {
			f := p.Frames[cp.Frame]
			want := f.Release + cp.Instance*p.Period + res.Offsets[cp.Frame][egressIndex(f.Ports, cp.Port)]
			if cp.Start != want {
				t.Fatalf("tick %d: start %+v, want absolute start %d", tick, cp, want)
			}
			if cp.End != cp.Start+f.Durations[egressIndex(f.Ports, cp.Port)] {
				t.Fatalf("tick %d: bad end in %+v", tick, cp)
			}
		}
	}
}

func TestMaximumPeriodAndHighBitmaps(t *testing.T) {
	p := gt.Problem{
		Period: 96,
		Ports: []gt.PortSpec{
			{Forbidden: make([]bool, 96)},
			{Forbidden: make([]bool, 96)},
		},
		Frames: []gt.FrameSpec{
			{Release: 90, Ports: []int{0}, Durations: []int{6}, Deadline: 6},
			{Release: 95, Ports: []int{1}, Durations: []int{2}, Deadline: 2},
		},
		MaxSlots:        2,
		MaxTableEntries: 4,
	}
	res, err := gt.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if res.Masks[0] != 2 || res.Masks[90] != 1 || res.Masks[95] != 3 {
		t.Fatalf("masks at 0/90/95 = %d/%d/%d, want 2/1/3",
			res.Masks[0], res.Masks[90], res.Masks[95])
	}
	if res.TableEntries != 4 || len(res.Spans) != 4 {
		t.Fatalf("entries/spans %d/%d, want 4/4", res.TableEntries, len(res.Spans))
	}
	if res.SlotsUsed != 2 {
		t.Fatalf("slots %d, want 2", res.SlotsUsed)
	}
}

func egressIndex(ports []int, port int) int {
	for i, p := range ports {
		if p == port {
			return i
		}
	}
	return -1
}

func TestValidation(t *testing.T) {
	valid := gt.Problem{
		Period:          4,
		Ports:           []gt.PortSpec{{Forbidden: make([]bool, 4)}},
		Frames:          []gt.FrameSpec{{Release: 0, Ports: []int{0}, Durations: []int{1}, Deadline: 4}},
		MaxSlots:        1,
		MaxTableEntries: 1,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid problem rejected: %v", err)
	}
	tests := []struct {
		name string
		mut  func(*gt.Problem)
	}{
		{"period", func(p *gt.Problem) { p.Period = 0 }},
		{"too many ports", func(p *gt.Problem) {
			p.Ports = make([]gt.PortSpec, gt.MaxPorts+1)
			for i := range p.Ports {
				p.Ports[i].Forbidden = make([]bool, p.Period)
			}
		}},
		{"forbidden length", func(p *gt.Problem) { p.Ports[0].Forbidden = []bool{false} }},
		{"release", func(p *gt.Problem) { p.Frames[0].Release = p.Period }},
		{"deadline", func(p *gt.Problem) { p.Frames[0].Deadline = 2*p.Period + 1 }},
		{"duration", func(p *gt.Problem) { p.Frames[0].Durations[0] = p.Period + 1 }},
		{"duplicate port", func(p *gt.Problem) {
			p.Frames[0].Ports = []int{0, 0}
			p.Frames[0].Durations = []int{1, 1}
		}},
		{"bad slot budget", func(p *gt.Problem) { p.MaxSlots = 0 }},
		{"bad entry budget", func(p *gt.Problem) { p.MaxTableEntries = -2 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			p.Ports = append([]gt.PortSpec(nil), valid.Ports...)
			p.Ports[0].Forbidden = append([]bool(nil), valid.Ports[0].Forbidden...)
			p.Frames = append([]gt.FrameSpec(nil), valid.Frames...)
			p.Frames[0].Ports = append([]int(nil), valid.Frames[0].Ports...)
			p.Frames[0].Durations = append([]int(nil), valid.Frames[0].Durations...)
			tc.mut(&p)
			if err := p.Validate(); err == nil {
				t.Fatalf("mutated problem accepted")
			}
		})
	}
}
