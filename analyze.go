package main

import (
	"sort"
	"strings"
	"time"
)

const (
	statusOK      = "ok"
	statusInvalid = "invalid"
	statusCycle   = "cycle"
)

type pathState struct {
	start int64
	end   int64
	work  int64
	path  []string
}

type tarjan struct {
	graph   map[string][]string
	index   map[string]int
	low     map[string]int
	onStack map[string]bool
	stack   []string
	next    int
	cycles  [][]string
}

// AnalyzeTrace separates elapsed wall-clock time from accumulated work.
// Parent self time excludes the union of direct synchronous children, so
// parallel children cannot inflate work by overlapping each other.
func AnalyzeTrace(traceID string, spans []Span) Report {
	report := Report{TraceID: traceID, Status: statusOK, SpanCount: len(spans)}
	byID := make(map[string]Span, len(spans))
	nodeOrder := make([]string, 0, len(spans))
	valid := make(map[string]bool, len(spans))
	problems := []Problem{}

	addProblem := func(code, severity, spanID, msg string) {
		problems = append(problems, Problem{Code: code, Severity: severity, SpanID: spanID, Message: msg})
	}

	for _, span := range spans {
		if _, exists := byID[span.ID]; exists {
			addProblem("DUPLICATE_SPAN_ID", ProblemError, span.ID, "span id appears more than once")
			continue
		}
		byID[span.ID] = span
		nodeOrder = append(nodeOrder, span.ID)
		if span.End < span.Start {
			addProblem("NEGATIVE_DURATION", ProblemError, span.ID, "span ends before it starts")
			continue
		}
		valid[span.ID] = true
	}

	seenReference := make(map[string]bool)
	seenGraphEdge := make(map[string]bool)
	hasParent := make(map[string]bool)
	children := make(map[string][]Span)
	graph := make(map[string][]string)
	preds := make(map[string][]string)

	for _, child := range spans {
		if _, known := byID[child.ID]; !known {
			continue
		}
		for _, ref := range child.References {
			parent, exists := byID[ref.Parent]
			if !exists {
				addProblem("UNRESOLVED_REFERENCE", ProblemError, child.ID, ref.Type+" reference points to missing span "+ref.Parent)
				continue
			}
			if !valid[child.ID] || !valid[parent.ID] {
				continue
			}

			referenceKey := ref.Type + "\x00" + parent.ID + "\x00" + child.ID
			if seenReference[referenceKey] {
				addProblem("DUPLICATE_REFERENCE", ProblemWarning, child.ID, "duplicate "+ref.Type+" reference to "+parent.ID)
				continue
			}
			seenReference[referenceKey] = true

			if ref.Type == RefChildOf && hasParent[child.ID] {
				addProblem("MULTIPLE_PARENT", ProblemError, child.ID, "span has more than one child_of parent")
				continue
			}

			if ref.Type == RefChildOf {
				if parent.Start > child.Start || child.End > parent.End {
					addProblem("CAUSAL_VIOLATION", ProblemError, child.ID, "child_of span is not contained by parent "+parent.ID)
					continue
				}
			} else if parent.End > child.Start {
				addProblem("CAUSAL_VIOLATION", ProblemError, child.ID, "follows_from span starts before predecessor "+parent.ID+" ends")
				continue
			}

			if ref.Type == RefChildOf {
				hasParent[child.ID] = true
				children[parent.ID] = append(children[parent.ID], child)
			}

			directedKey := parent.ID + "\x00" + child.ID
			if !seenGraphEdge[directedKey] {
				seenGraphEdge[directedKey] = true
				graph[parent.ID] = append(graph[parent.ID], child.ID)
				preds[child.ID] = append(preds[child.ID], parent.ID)
			}
		}
	}

	self := make(map[string]int64)
	var minStart, maxEnd, totalWork int64
	haveTimeline := false
	for _, id := range nodeOrder {
		span := byID[id]
		if !valid[id] {
			continue
		}
		report.ValidSpans++
		if !haveTimeline || span.Start < minStart {
			minStart = span.Start
		}
		if !haveTimeline || span.End > maxEnd {
			maxEnd = span.End
		}
		haveTimeline = true

		covered := unionLength(children[id])
		self[id] = (span.End - span.Start) - covered
		totalWork += self[id]
	}

	if haveTimeline {
		report.TimelineWall = time.Duration(maxEnd-minStart) * time.Nanosecond
	}
	report.TotalWork = time.Duration(totalWork) * time.Nanosecond

	ids := make([]string, 0, len(nodeOrder))
	for _, id := range nodeOrder {
		if valid[id] {
			ids = append(ids, id)
		}
	}
	sort.SliceStable(ids, func(a, b int) bool {
		x, y := byID[ids[a]], byID[ids[b]]
		if x.Start != y.Start {
			return x.Start < y.Start
		}
		return ids[a] < ids[b]
	})
	for _, id := range ids {
		sort.Strings(graph[id])
		sort.Strings(preds[id])
	}

	cycleSets := findCycles(ids, graph)
	if len(cycleSets) > 0 {
		for _, cycle := range cycleSets {
			addProblem("CYCLE_DETECTED", ProblemError, strings.Join(cycle, ","), "dependency cycle: "+strings.Join(cycle, " -> ")+" -> "+cycle[0])
		}
	}

	if len(cycleSets) == 0 {
		report.Critical = criticalPath(ids, byID, graph, preds, self)
	}

	sort.SliceStable(problems, func(a, b int) bool {
		if problems[a].Code != problems[b].Code {
			return problems[a].Code < problems[b].Code
		}
		if problems[a].SpanID != problems[b].SpanID {
			return problems[a].SpanID < problems[b].SpanID
		}
		return problems[a].Message < problems[b].Message
	})
	report.Problems = problems

	if len(cycleSets) > 0 {
		report.Status = statusCycle
	} else if len(problems) > 0 {
		report.Status = statusInvalid
	}
	return report
}

func unionLength(spans []Span) int64 {
	if len(spans) == 0 {
		return 0
	}
	type interval struct{ start, end int64 }
	intervals := make([]interval, 0, len(spans))
	for _, span := range spans {
		intervals = append(intervals, interval{span.Start, span.End})
	}
	sort.SliceStable(intervals, func(a, b int) bool {
		if intervals[a].start != intervals[b].start {
			return intervals[a].start < intervals[b].start
		}
		return intervals[a].end < intervals[b].end
	})

	total := int64(0)
	start, end := intervals[0].start, intervals[0].end
	for _, next := range intervals[1:] {
		if next.start <= end {
			if next.end > end {
				end = next.end
			}
			continue
		}
		total += end - start
		start, end = next.start, next.end
	}
	return total + end - start
}

func findCycles(ids []string, graph map[string][]string) [][]string {
	detector := &tarjan{
		graph:   graph,
		index:   make(map[string]int),
		low:     make(map[string]int),
		onStack: make(map[string]bool),
	}
	for _, id := range ids {
		if _, visited := detector.index[id]; !visited {
			detector.dfs(id)
		}
	}
	sort.SliceStable(detector.cycles, func(a, b int) bool {
		return detector.cycles[a][0] < detector.cycles[b][0]
	})
	return detector.cycles
}

func (t *tarjan) dfs(v string) {
	t.index[v] = t.next
	t.low[v] = t.next
	t.next++
	t.stack = append(t.stack, v)
	t.onStack[v] = true

	for _, w := range t.graph[v] {
		if _, visited := t.index[w]; !visited {
			t.dfs(w)
			if t.low[w] < t.low[v] {
				t.low[v] = t.low[w]
			}
		} else if t.onStack[w] && t.index[w] < t.low[v] {
			t.low[v] = t.index[w]
		}
	}

	if t.low[v] != t.index[v] {
		return
	}

	var component []string
	for {
		w := t.stack[len(t.stack)-1]
		t.stack = t.stack[:len(t.stack)-1]
		t.onStack[w] = false
		component = append(component, w)
		if w == v {
			break
		}
	}

	cyclic := len(component) > 1
	if !cyclic {
		for _, w := range t.graph[v] {
			if w == v {
				cyclic = true
				break
			}
		}
	}
	if cyclic {
		sort.Strings(component)
		t.cycles = append(t.cycles, component)
	}
}

func criticalPath(ids []string, byID map[string]Span, graph, preds map[string][]string, self map[string]int64) PathMetrics {
	indegree := make(map[string]int, len(ids))
	for _, id := range ids {
		indegree[id] = len(preds[id])
	}
	emitted := make(map[string]bool, len(ids))
	states := make(map[string]pathState, len(ids))

	for emittedCount := 0; emittedCount < len(ids); {
		current := ""
		for _, id := range ids {
			if !emitted[id] && indegree[id] == 0 {
				current = id
				break
			}
		}

		span := byID[current]
		state := pathState{
			start: span.Start,
			end:   span.End,
			work:  self[current],
			path:  []string{current},
		}
		for _, pred := range preds[current] {
			candidate := states[pred]
			candidate.path = append(append([]string{}, candidate.path...), current)
			if span.Start < candidate.start {
				candidate.start = span.Start
			}
			if span.End > candidate.end {
				candidate.end = span.End
			}
			candidate.work += self[current]
			if betterPath(candidate, state) {
				state = candidate
			}
		}
		states[current] = state

		emitted[current] = true
		emittedCount++
		for _, next := range graph[current] {
			indegree[next]--
		}
	}

	best := pathState{}
	haveBest := false
	for _, id := range ids {
		state := states[id]
		if !haveBest || betterPath(state, best) {
			best = state
			haveBest = true
		}
	}

	wall := best.end - best.start
	idle := wall - best.work
	if idle < 0 {
		idle = 0
	}
	return PathMetrics{
		Wall:        time.Duration(wall) * time.Nanosecond,
		Work:        time.Duration(best.work) * time.Nanosecond,
		IdleWaiting: time.Duration(idle) * time.Nanosecond,
		SpanIDs:     best.path,
	}
}

func betterPath(candidate, current pathState) bool {
	candidateWall := candidate.end - candidate.start
	currentWall := current.end - current.start
	if candidateWall != currentWall {
		return candidateWall > currentWall
	}
	if candidate.work != current.work {
		return candidate.work > current.work
	}
	if len(candidate.path) != len(current.path) {
		return len(candidate.path) > len(current.path)
	}
	return lexPath(candidate.path) < lexPath(current.path)
}

func lexPath(path []string) string {
	return strings.Join(path, "\x00")
}
