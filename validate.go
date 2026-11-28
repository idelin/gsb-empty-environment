package zerobubble

import (
	"fmt"
	"sort"
	"strings"
)

// AuditReport is the result of validating a complete schedule.
type AuditReport struct {
	OK bool

	DependencyOK    bool
	ResourceOK      bool
	MemoryOK        bool
	CommunicationOK bool
	AcyclicOK       bool
	DeadlockFree    bool

	PeakMemory         []int64
	PeakMemoryAt       []float64
	MaxPeakMemory      int64
	MaxPeakMemoryStage int

	GraphNodes int
	GraphEdges int

	Issues []string
}

// Error joins all audit failures.
func (r *AuditReport) Error() error {
	if r == nil || r.OK {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(r.Issues, "; "))
}

// Validate checks dependencies, resource exclusion, memory lifetime,
// communication pairing, and acyclicity.
func (s *Schedule) Validate() error {
	return Audit(s).Error()
}

// ValidateSchedule is the package-level form of Schedule.Validate.
func ValidateSchedule(s *Schedule) error {
	return s.Validate()
}

// CommunicationReport summarizes matched Send/Recv validation for one
// schedule.
type CommunicationReport struct {
	OK bool

	PairedOK      bool
	FIFOOrderOK   bool
	DeadlockFree  bool
	AcyclicOK     bool
	Messages      int
	DirectedLinks int

	Issues []string
}

// Error joins all communication failures.
func (r *CommunicationReport) Error() error {
	if r == nil || r.OK {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(r.Issues, "; "))
}

// CheckCommunication validates message endpoints, FIFO pairing, bidirectional
// synchronous-handshake safety, and global graph acyclicity.
func CheckCommunication(s *Schedule) *CommunicationReport {
	report := &CommunicationReport{}
	if s == nil {
		report.Issues = append(report.Issues, "nil schedule")
		return report
	}
	rc, err := s.Config.Resolve()
	if err != nil {
		report.Issues = append(report.Issues, "invalid config: "+err.Error())
		return report
	}
	taskMap := make(map[TaskID]Task, len(s.Tasks))
	for _, task := range s.Tasks {
		taskMap[task.ID] = task
	}
	issues, deadlockFree := auditCommunication(s, rc, taskMap)
	_, _, cycle := auditGraph(s, taskMap)
	report.Issues = issues
	report.Messages = len(s.Comms)
	links := make(map[[2]int]bool)
	for _, c := range s.Comms {
		links[[2]int{c.FromStage, c.ToStage}] = true
	}
	report.DirectedLinks = len(links)
	report.PairedOK = len(issues) == 0
	report.FIFOOrderOK = len(issues) == 0
	report.DeadlockFree = deadlockFree && cycle == ""
	report.AcyclicOK = cycle == ""
	if cycle != "" {
		report.Issues = append(report.Issues, "schedule graph is cyclic: "+cycle)
	}
	report.OK = len(report.Issues) == 0 && report.DeadlockFree && report.AcyclicOK
	return report
}

// Audit performs a non-short-circuiting validation and returns a detailed
// report. It recomputes peak memory from the task intervals.
func Audit(s *Schedule) *AuditReport {
	report := &AuditReport{}
	if s == nil {
		report.Issues = append(report.Issues, "nil schedule")
		return report
	}
	rc, err := s.Config.Resolve()
	if err != nil {
		report.Issues = append(report.Issues, "invalid config: "+err.Error())
		return report
	}
	if s.Mode != Mode1F1B && s.Mode != ModeZeroBubble {
		report.Issues = append(report.Issues, fmt.Sprintf("unknown mode %q", s.Mode))
		return report
	}

	taskMap, taskIssues := auditTasks(s, rc)
	report.Issues = append(report.Issues, taskIssues...)
	report.DependencyOK = !hasPrefix(taskIssues, "dependency")
	report.ResourceOK = !hasPrefix(taskIssues, "resource")

	peak, peakAt, memoryIssues := auditMemory(s, rc, taskMap)
	report.PeakMemory = peak
	report.PeakMemoryAt = peakAt
	for stage, value := range peak {
		if value > report.MaxPeakMemory {
			report.MaxPeakMemory = value
			report.MaxPeakMemoryStage = stage
		}
	}
	report.Issues = append(report.Issues, memoryIssues...)
	report.MemoryOK = len(memoryIssues) == 0

	commIssues, deadlockFree := auditCommunication(s, rc, taskMap)
	report.Issues = append(report.Issues, commIssues...)
	report.CommunicationOK = len(commIssues) == 0
	report.DeadlockFree = deadlockFree && report.CommunicationOK

	nodes, edges, cycle := auditGraph(s, taskMap)
	report.GraphNodes = nodes
	report.GraphEdges = edges
	report.AcyclicOK = cycle == ""
	report.DeadlockFree = report.DeadlockFree && report.AcyclicOK
	if cycle != "" {
		report.Issues = append(report.Issues, "schedule graph is cyclic: "+cycle)
	}

	report.OK = report.DependencyOK && report.ResourceOK && report.MemoryOK &&
		report.CommunicationOK && report.AcyclicOK && report.DeadlockFree &&
		len(report.Issues) == 0
	return report
}

func auditTasks(s *Schedule, rc ResolvedConfig) (map[TaskID]Task, []string) {
	var issues []string
	taskMap := make(map[TaskID]Task, len(s.Tasks))
	for _, task := range s.Tasks {
		id := task.ID
		if id.Stage < 0 || id.Stage >= rc.P {
			issues = append(issues, fmt.Sprintf("dependency: task %s has invalid stage", id))
			continue
		}
		if id.Microbatch < 0 || id.Microbatch >= rc.M {
			issues = append(issues, fmt.Sprintf("dependency: task %s has invalid microbatch", id))
			continue
		}
		if _, exists := taskMap[id]; exists {
			issues = append(issues, fmt.Sprintf("dependency: duplicate task %s", id))
			continue
		}
		taskMap[id] = task
		if task.Start < -timeEpsilon {
			issues = append(issues, fmt.Sprintf("dependency: task %s starts at negative time %g", id, task.Start))
		}
		if task.End <= task.Start+timeEpsilon {
			issues = append(issues, fmt.Sprintf("dependency: task %s has non-positive duration", id))
		}
		want := expectedDuration(rc, s.Mode, id)
		if want <= 0 {
			issues = append(issues, fmt.Sprintf("dependency: task %s is not valid in mode %s", id, s.Mode))
		} else if !nearlyEqual(task.Duration(), want) {
			issues = append(issues, fmt.Sprintf("dependency: task %s duration %.6f, want %.6f", id, task.Duration(), want))
		}
	}

	if len(taskMap) != len(s.Tasks) {
		issues = append(issues, "dependency: task count does not match unique task ids")
	}
	expectedCount := rc.P * rc.M * 2
	if s.Mode == ModeZeroBubble {
		expectedCount = rc.P * rc.M * 3
	}
	if len(taskMap) != expectedCount {
		issues = append(issues, fmt.Sprintf("dependency: got %d tasks, want %d", len(taskMap), expectedCount))
	}

	for st := 0; st < rc.P; st++ {
		for m := 0; m < rc.M; m++ {
			kinds := []TaskKind{Forward, Backward}
			if s.Mode == ModeZeroBubble {
				kinds = append(kinds, Weight)
			}
			for _, kind := range kinds {
				id := TaskID{Kind: kind, Microbatch: m, Stage: st}
				if _, ok := taskMap[id]; !ok {
					issues = append(issues, fmt.Sprintf("dependency: missing task %s", id))
				}
			}
		}
	}

	// Dependencies must be complete and must finish before the consumer.
	for _, task := range taskMap {
		want := expectedDeps(task.ID, rc, s.Mode)
		got := make(map[TaskID]bool, len(task.Deps))
		for _, dep := range task.Deps {
			if got[dep] {
				issues = append(issues, fmt.Sprintf("dependency: task %s lists %s more than once", task.ID, dep))
			}
			got[dep] = true
			depTask, exists := taskMap[dep]
			if !exists {
				issues = append(issues, fmt.Sprintf("dependency: task %s references missing %s", task.ID, dep))
				continue
			}
			lag := 0.0
			if dep.Stage != task.ID.Stage {
				lag = rc.TComm
			}
			if task.Start+timeEpsilon < depTask.End+lag {
				issues = append(issues, fmt.Sprintf(
					"dependency: task %s starts %.6f before %s completes%s at %.6f",
					task.ID, task.Start, dep, commSuffix(lag), depTask.End+lag,
				))
			}
		}
		if len(got) != len(want) {
			issues = append(issues, fmt.Sprintf("dependency: task %s has %d dependencies, want %d", task.ID, len(got), len(want)))
		}
		for _, dep := range want {
			if !got[dep] {
				issues = append(issues, fmt.Sprintf("dependency: task %s missing dependency %s", task.ID, dep))
			}
		}
		for dep := range got {
			if !containsTask(want, dep) {
				issues = append(issues, fmt.Sprintf("dependency: task %s has unexpected dependency %s", task.ID, dep))
			}
		}
	}

	// Each stage can execute only one task at a time.
	for st := 0; st < rc.P; st++ {
		var tasks []Task
		for _, task := range taskMap {
			if task.ID.Stage == st {
				tasks = append(tasks, task)
			}
		}
		sort.Slice(tasks, func(i, j int) bool {
			if nearlyEqual(tasks[i].Start, tasks[j].Start) {
				return tasks[i].ID.Kind < tasks[j].ID.Kind
			}
			return tasks[i].Start < tasks[j].Start
		})
		for i := 1; i < len(tasks); i++ {
			if tasks[i].Start+timeEpsilon < tasks[i-1].End {
				issues = append(issues, fmt.Sprintf(
					"resource: stage %d tasks %s [%.6f,%.6f) and %s [%.6f,%.6f) overlap",
					st, tasks[i-1].ID, tasks[i-1].Start, tasks[i-1].End,
					tasks[i].ID, tasks[i].Start, tasks[i].End,
				))
			}
		}
	}

	if !nearlyEqual(s.Makespan, maxEnd(taskMap)) {
		issues = append(issues, fmt.Sprintf("dependency: makespan %.6f, want %.6f", s.Makespan, maxEnd(taskMap)))
	}
	return taskMap, issues
}

func expectedDuration(rc ResolvedConfig, mode Mode, id TaskID) float64 {
	if id.Stage < 0 || id.Stage >= len(rc.Stages) {
		return 0
	}
	st := rc.Stages[id.Stage]
	switch id.Kind {
	case Forward:
		return st.TF
	case Backward:
		if mode == Mode1F1B {
			return st.TB + st.TW
		}
		return st.TB
	case Weight:
		if mode == ModeZeroBubble {
			return st.TW
		}
	}
	return 0
}

func expectedDeps(id TaskID, rc ResolvedConfig, mode Mode) []TaskID {
	switch id.Kind {
	case Forward:
		if id.Stage == 0 {
			return nil
		}
		return []TaskID{{Kind: Forward, Microbatch: id.Microbatch, Stage: id.Stage - 1}}
	case Backward:
		deps := []TaskID{{Kind: Forward, Microbatch: id.Microbatch, Stage: id.Stage}}
		if id.Stage+1 < rc.P {
			deps = append(deps, TaskID{Kind: Backward, Microbatch: id.Microbatch, Stage: id.Stage + 1})
		}
		return deps
	case Weight:
		if mode != ModeZeroBubble {
			return nil
		}
		return []TaskID{
			{Kind: Forward, Microbatch: id.Microbatch, Stage: id.Stage},
			{Kind: Backward, Microbatch: id.Microbatch, Stage: id.Stage},
		}
	default:
		return nil
	}
}

func auditMemory(s *Schedule, rc ResolvedConfig, taskMap map[TaskID]Task) ([]int64, []float64, []string) {
	type event struct {
		at    float64
		order int
		delta int64
		addW  bool
		task  TaskID
	}
	peak := make([]int64, rc.P)
	peakAt := make([]float64, rc.P)
	var issues []string
	for st := 0; st < rc.P; st++ {
		var events []event
		for _, task := range taskMap {
			if task.ID.Stage != st {
				continue
			}
			switch task.ID.Kind {
			case Forward:
				events = append(events, event{
					at: task.Start, order: 3, delta: rc.Stages[st].MemF, task: task.ID,
				})
			case Backward:
				events = append(events, event{
					at: task.End, order: 2, delta: -rc.Stages[st].MemF,
					addW: s.Mode == ModeZeroBubble, task: task.ID,
				})
			case Weight:
				events = append(events, event{
					at: task.End, order: 1, delta: -rc.Stages[st].MemW, task: task.ID,
				})
			}
		}
		sort.Slice(events, func(i, j int) bool {
			if nearlyEqual(events[i].at, events[j].at) {
				return events[i].order < events[j].order
			}
			return events[i].at < events[j].at
		})
		var mem int64
		for _, ev := range events {
			mem += ev.delta
			if mem < 0 {
				issues = append(issues, fmt.Sprintf("memory: stage %d negative residency %d at %s", st, mem, ev.task))
			}
			if ev.addW {
				mem += rc.Stages[st].MemW
			}
			if mem > peak[st] {
				peak[st] = mem
				peakAt[st] = ev.at
			}
			if mem > rc.Stages[st].MemLimit {
				issues = append(issues, fmt.Sprintf(
					"memory: stage %d uses %d > limit %d at t=%.6f (%s)",
					st, mem, rc.Stages[st].MemLimit, ev.at, ev.task,
				))
			}
		}
		if mem != 0 {
			issues = append(issues, fmt.Sprintf("memory: stage %d ends with %d resident units", st, mem))
		}
		if peak[st] > rc.Stages[st].MemLimit {
			issues = append(issues, fmt.Sprintf("memory: stage %d peak %d > limit %d", st, peak[st], rc.Stages[st].MemLimit))
		}
	}
	return peak, peakAt, issues
}

type commKey struct {
	kind CommKind
	m    int
	from int
	to   int
}

func auditCommunication(s *Schedule, rc ResolvedConfig, taskMap map[TaskID]Task) ([]string, bool) {
	var issues []string
	deadlockFree := true
	expected := make(map[commKey]bool)
	if rc.P > 1 {
		for m := 0; m < rc.M; m++ {
			for st := 1; st < rc.P; st++ {
				expected[commKey{ActivationComm, m, st - 1, st}] = true
			}
			for st := 0; st+1 < rc.P; st++ {
				expected[commKey{GradientComm, m, st + 1, st}] = true
			}
		}
	}
	if len(s.Comms) != len(expected) {
		issues = append(issues, fmt.Sprintf("communication: got %d messages, want %d", len(s.Comms), len(expected)))
	}

	seen := make(map[string]bool)
	seenKey := make(map[commKey]bool)
	byDirection := make(map[[2]int][]Comm)
	for _, c := range s.Comms {
		if seen[c.ID] {
			issues = append(issues, "communication: duplicate message id "+c.ID)
		}
		seen[c.ID] = true
		key := commKey{c.Kind, c.Microbatch, c.FromStage, c.ToStage}
		if !expected[key] {
			issues = append(issues, fmt.Sprintf("communication: unexpected message %s %s m%d %d->%d",
				c.ID, c.Kind, c.Microbatch, c.FromStage, c.ToStage))
		}
		if seenKey[key] {
			issues = append(issues, fmt.Sprintf("communication: duplicate pair %s m%d %d->%d",
				c.Kind, c.Microbatch, c.FromStage, c.ToStage))
		}
		seenKey[key] = true

		producer, pok := taskMap[c.Producer]
		consumer, cok := taskMap[c.Consumer]
		if !pok || !cok {
			issues = append(issues, fmt.Sprintf("communication: message %s has missing endpoint task", c.ID))
			continue
		}
		wantProducerKind := Forward
		if c.Kind == GradientComm {
			wantProducerKind = Backward
		}
		if c.Producer.Kind != wantProducerKind || c.Consumer.Kind != wantProducerKind {
			issues = append(issues, fmt.Sprintf("communication: message %s has wrong endpoint kinds", c.ID))
		}
		if c.Producer.Stage != c.FromStage || c.Consumer.Stage != c.ToStage {
			issues = append(issues, fmt.Sprintf("communication: message %s endpoint stages do not match %d->%d",
				c.ID, c.FromStage, c.ToStage))
		}
		if c.FromStage < 0 || c.FromStage >= rc.P || c.ToStage < 0 || c.ToStage >= rc.P ||
			absInt(c.FromStage-c.ToStage) != 1 {
			issues = append(issues, fmt.Sprintf("communication: message %s does not connect adjacent stages", c.ID))
		}
		if !nearlyEqual(c.SendStart, producer.End) {
			issues = append(issues, fmt.Sprintf("communication: message %s send starts %.6f, producer %s ends %.6f",
				c.ID, c.SendStart, producer.ID, producer.End))
		}
		if !nearlyEqual(c.SendEnd, c.SendStart+rc.TComm) {
			issues = append(issues, fmt.Sprintf("communication: message %s send latency is not tComm=%g", c.ID, rc.TComm))
		}
		if !nearlyEqual(c.RecvStart, c.SendStart) || !nearlyEqual(c.RecvEnd, c.SendEnd) {
			issues = append(issues, fmt.Sprintf("communication: message %s is not a paired synchronous handshake", c.ID))
			deadlockFree = false
		}
		if consumer.Start+timeEpsilon < c.RecvEnd {
			issues = append(issues, fmt.Sprintf("communication: consumer %s starts before message %s arrives", consumer.ID, c.ID))
		}
		byDirection[[2]int{c.FromStage, c.ToStage}] = append(byDirection[[2]int{c.FromStage, c.ToStage}], c)
	}
	for key := range expected {
		if !seenKey[key] {
			issues = append(issues, fmt.Sprintf("communication: missing pair %s m%d %d->%d",
				key.kind, key.m, key.from, key.to))
		}
	}

	// FIFO ordering on every directed P2P channel. Send and receive order must
	// agree; this is also the parity/order check for doubled-buffer channels.
	for link, list := range byDirection {
		sort.Slice(list, func(i, j int) bool {
			if nearlyEqual(list[i].SendStart, list[j].SendStart) {
				return list[i].Microbatch < list[j].Microbatch
			}
			return list[i].SendStart < list[j].SendStart
		})
		for i := 1; i < len(list); i++ {
			if list[i].RecvStart+timeEpsilon < list[i-1].RecvStart {
				issues = append(issues, fmt.Sprintf(
					"communication: FIFO order mismatch on %d->%d between %s and %s",
					link[0], link[1], list[i-1].ID, list[i].ID,
				))
				deadlockFree = false
			}
		}
	}

	// A synchronous bidirectional handshake is deadlock-free only if at most
	// one side is blocked in a send whose matching receive has not been posted.
	for st := 0; st+1 < rc.P; st++ {
		forward := byDirection[[2]int{st, st + 1}]
		backward := byDirection[[2]int{st + 1, st}]
		for _, a := range forward {
			for _, b := range backward {
				if !intervalsOverlap(a.SendStart, a.SendEnd, b.SendStart, b.SendEnd) {
					continue
				}
				aBlocked := a.SendStart < a.RecvStart-timeEpsilon
				bBlocked := b.SendStart < b.RecvStart-timeEpsilon
				if aBlocked && bBlocked {
					issues = append(issues, fmt.Sprintf(
						"communication: bidirectional blocking sends deadlock on link %d-%d (%s, %s)",
						st, st+1, a.ID, b.ID,
					))
					deadlockFree = false
				}
			}
		}
	}
	return issues, deadlockFree
}

func auditGraph(s *Schedule, taskMap map[TaskID]Task) (int, int, string) {
	nodes := make(map[string]bool)
	edges := make(map[string]map[string]bool)
	addNode := func(n string) { nodes[n] = true }
	addEdge := func(a, b string) {
		addNode(a)
		addNode(b)
		if edges[a] == nil {
			edges[a] = make(map[string]bool)
		}
		edges[a][b] = true
	}
	taskNode := func(id TaskID) string { return "T:" + id.String() }
	sendNode := func(id string) string { return "S:" + id }
	recvNode := func(id string) string { return "R:" + id }

	for _, task := range taskMap {
		addNode(taskNode(task.ID))
		for _, dep := range task.Deps {
			addEdge(taskNode(dep), taskNode(task.ID))
		}
	}
	for _, c := range s.Comms {
		addEdge(taskNode(c.Producer), sendNode(c.ID))
		addEdge(sendNode(c.ID), recvNode(c.ID))
		addEdge(recvNode(c.ID), taskNode(c.Consumer))
	}

	// Stage-resource order is an independent acyclicity check on the produced
	// timeline, not just on the abstract F/B/W dependency graph.
	byStage := make(map[int][]Task)
	for _, task := range taskMap {
		byStage[task.ID.Stage] = append(byStage[task.ID.Stage], task)
	}
	for _, list := range byStage {
		sort.Slice(list, func(i, j int) bool {
			if nearlyEqual(list[i].Start, list[j].Start) {
				return list[i].ID.String() < list[j].ID.String()
			}
			return list[i].Start < list[j].Start
		})
		for i := 1; i < len(list); i++ {
			addEdge(taskNode(list[i-1].ID), taskNode(list[i].ID))
		}
	}

	// Ordered directed channels add FIFO edges. Opposite directions are
	// independent full-duplex channels; their paired handshakes are checked by
	// auditCommunication.
	byChannel := make(map[[2]int][]Comm)
	for _, c := range s.Comms {
		byChannel[[2]int{c.FromStage, c.ToStage}] = append(byChannel[[2]int{c.FromStage, c.ToStage}], c)
	}
	for _, list := range byChannel {
		sort.Slice(list, func(i, j int) bool {
			if nearlyEqual(list[i].SendStart, list[j].SendStart) {
				return list[i].ID < list[j].ID
			}
			return list[i].SendStart < list[j].SendStart
		})
		for i := 1; i < len(list); i++ {
			addEdge(sendNode(list[i-1].ID), sendNode(list[i].ID))
			addEdge(recvNode(list[i-1].ID), recvNode(list[i].ID))
		}
	}

	edgeCount := 0
	for _, outs := range edges {
		edgeCount += len(outs)
	}
	cycle := detectCycle(nodes, edges)
	return len(nodes), edgeCount, cycle
}

func detectCycle(nodes map[string]bool, edges map[string]map[string]bool) string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(nodes))
	stack := make([]string, 0, len(nodes))
	var visit func(string) string
	visit = func(n string) string {
		color[n] = gray
		stack = append(stack, n)
		for nxt := range edges[n] {
			switch color[nxt] {
			case gray:
				start := 0
				for i, v := range stack {
					if v == nxt {
						start = i
						break
					}
				}
				return strings.Join(append(append([]string(nil), stack[start:]...), nxt), " -> ")
			case white:
				if cycle := visit(nxt); cycle != "" {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return ""
	}
	for n := range nodes {
		if color[n] == white {
			if cycle := visit(n); cycle != "" {
				return cycle
			}
		}
	}
	return ""
}

func maxEnd(tasks map[TaskID]Task) float64 {
	var out float64
	for _, task := range tasks {
		if task.End > out {
			out = task.End
		}
	}
	return out
}

func containsTask(list []TaskID, id TaskID) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

func hasPrefix(issues []string, prefix string) bool {
	for _, issue := range issues {
		if strings.HasPrefix(issue, prefix+":") {
			return true
		}
	}
	return false
}

func commSuffix(lag float64) string {
	if lag > 0 {
		return fmt.Sprintf(" + tComm=%.6f", lag)
	}
	return ""
}

func intervalsOverlap(a0, a1, b0, b1 float64) bool {
	if nearlyEqual(a0, a1) && nearlyEqual(b0, b1) {
		return nearlyEqual(a0, b0)
	}
	return a0 < b1-timeEpsilon && b0 < a1-timeEpsilon
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
