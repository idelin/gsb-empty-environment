package netsec

import "fmt"

type stateKey struct {
	pc   [2]int
	held [maxLocks]uint8 // 0 means free, otherwise owning thread index + 1
}

type node struct {
	key    stateKey
	parent int
	depth  int
	step   *Step
}

type threadStatus int

const (
	stRunnable threadStatus = iota
	stBlocked
	stDone
)

type statusInfo struct {
	status      threadStatus
	blockedEdge int // index into cThread.edges when blocked, -1 otherwise
	blockedLock int // requested mutex when blocked, -1 otherwise
}

func bfs(c *cSpec, assign uint32) (*Result, error) {
	start := stateKey{pc: [2]int{c.threads[0].start, c.threads[1].start}}
	visited := map[stateKey]int{start: 0}
	nodes := []*node{{key: start, parent: -1}}
	queue := []int{0}
	explored := 0
	var best *Result
	bestKind := DeadlockNone

	for len(queue) > 0 {
		curIdx := queue[0]
		queue = queue[1:]
		cur := nodes[curIdx]
		explored++

		infos := [2]statusInfo{}
		runnable, blocked := 0, 0
		for ti := range c.threads {
			infos[ti] = threadStatusOf(c, ti, cur.key, assign)
			switch infos[ti].status {
			case stRunnable:
				runnable++
			case stBlocked:
				blocked++
			}
		}

		if runnable == 0 && blocked > 0 {
			kind, repTI := classifyDeadlock(c, cur.key, infos)
			// BFS visits states in non-decreasing depth, so the first
			// candidate at a new depth is the shortest trace for its kind.
			// Cyclic/recursive deadlocks outrank orphaned waits, which may be
			// reached at a shallower depth in the same graph.
			if best == nil {
				best = buildResult(c, assign, nodes, curIdx, kind, infos, repTI, explored)
				bestKind = kind
				if kind != DeadlockOrphaned {
					return best, nil
				}
				continue
			}
			if bestKind == DeadlockOrphaned && kind != DeadlockOrphaned {
				return buildResult(c, assign, nodes, curIdx, kind, infos, repTI, explored), nil
			}
			continue
		}

		for ti := range c.threads {
			if infos[ti].status != stRunnable {
				continue
			}
			nextKeys, steps, err := expand(c, ti, cur.key, assign)
			if err != nil {
				return nil, err
			}
			for i := range nextKeys {
				if _, seen := visited[nextKeys[i]]; seen {
					continue
				}
				visited[nextKeys[i]] = len(nodes)
				nodes = append(nodes, &node{
					key:    nextKeys[i],
					parent: curIdx,
					depth:  cur.depth + 1,
					step:   &steps[i],
				})
				queue = append(queue, len(nodes)-1)
			}
		}
	}
	return best, nil
}

func edgeEnabled(e cEdge, assign uint32) bool {
	return e.guard == nil || e.guard.eval(assign)
}

// threadStatusOf classifies one thread in a concrete state. A lock edge whose
// request cannot be granted makes the thread blocked only if no other enabled
// edge can make progress.
func threadStatusOf(c *cSpec, ti int, key stateKey, assign uint32) statusInfo {
	ct := &c.threads[ti]
	edges := ct.out[key.pc[ti]]
	info := statusInfo{status: stDone, blockedEdge: -1, blockedLock: -1}
	if len(edges) == 0 {
		return info
	}
	for _, ei := range edges {
		e := ct.edges[ei]
		if !edgeEnabled(e, assign) {
			continue
		}
		if e.op != OpLock {
			info.status = stRunnable
			return info
		}
		owner := key.held[e.lock]
		if owner == 0 {
			info.status = stRunnable
			return info
		}
		if info.status != stBlocked {
			info = statusInfo{status: stBlocked, blockedEdge: ei, blockedLock: e.lock}
		}
	}
	return info
}

// classifyDeadlock distinguishes recursive requests, wait-for cycles, and
// requests whose owner has already terminated.
func classifyDeadlock(c *cSpec, key stateKey, infos [2]statusInfo) (DeadlockKind, int) {
	rep := -1
	for ti := range infos {
		if infos[ti].status != stBlocked {
			continue
		}
		if rep == -1 {
			rep = ti
		}
	}

	// A recursive request is a self-loop in the wait-for relation; check it
	// before the generic cycle walk so the reported kind stays precise.
	for ti := range infos {
		if infos[ti].status != stBlocked {
			continue
		}
		owner := int(key.held[infos[ti].blockedLock]) - 1
		if owner == ti {
			return DeadlockRecursive, ti
		}
	}

	// A wait-for cycle is the genuine cyclic deadlock and takes precedence
	// over orphaned reports even at the same BFS depth.
	waitFor := map[int]int{}
	for ti := range infos {
		if infos[ti].status != stBlocked {
			continue
		}
		owner := int(key.held[infos[ti].blockedLock]) - 1
		waitFor[ti] = owner
	}
	for start := range waitFor {
		seen := map[int]bool{}
		cur := start
		for {
			if seen[cur] {
				return DeadlockCycle, start
			}
			seen[cur] = true
			next, ok := waitFor[cur]
			if !ok {
				break
			}
			cur = next
		}
	}
	return DeadlockOrphaned, rep
}

// expand executes every enabled edge of one runnable thread. Lock edges whose
// mutex is held elsewhere are not transitions: the thread would block and are
// therefore excluded.
func expand(c *cSpec, ti int, key stateKey, assign uint32) ([]stateKey, []Step, error) {
	ct := &c.threads[ti]
	var keys []stateKey
	var steps []Step
	for _, ei := range ct.out[key.pc[ti]] {
		e := ct.edges[ei]
		if !edgeEnabled(e, assign) {
			continue
		}
		if e.op == OpLock && key.held[e.lock] != 0 {
			continue
		}

		next := key
		next.pc[ti] = e.to
		switch e.op {
		case OpLock:
			next.held[e.lock] = uint8(ti + 1)
		case OpUnlock:
			if int(key.held[e.lock]) != ti+1 {
				return nil, nil, fmt.Errorf(
					"netsec: thread %q unlocks lock %q it does not hold",
					ct.name, c.lockNames[e.lock])
			}
			next.held[e.lock] = 0
		}

		lockName := ""
		if e.lock >= 0 {
			lockName = c.lockNames[e.lock]
		}
		keys = append(keys, next)
		steps = append(steps, Step{
			Thread: ct.name,
			Edge:   e.name,
			From:   ct.states[e.from],
			To:     ct.states[e.to],
			Op:     e.op,
			Lock:   lockName,
		})
	}
	return keys, steps, nil
}

func buildResult(
	c *cSpec,
	assign uint32,
	nodes []*node,
	curIdx int,
	kind DeadlockKind,
	infos [2]statusInfo,
	repTI int,
	explored int,
) *Result {
	assignment := make(map[string]bool, len(c.varNames))
	for i, name := range c.varNames {
		assignment[name] = assign&(1<<uint(i)) != 0
	}

	var trace []Step
	for idx := curIdx; idx >= 0; idx = nodes[idx].parent {
		if nodes[idx].step != nil {
			trace = append(trace, *nodes[idx].step)
		}
	}
	for i, j := 0, len(trace)-1; i < j; i, j = i+1, j-1 {
		trace[i], trace[j] = trace[j], trace[i]
	}
	for i := range trace {
		trace[i].Seq = i + 1
	}

	info := infos[repTI]
	edge := c.threads[repTI].edges[info.blockedEdge]
	owner := ""
	if h := int(nodes[curIdx].key.held[info.blockedLock]); h > 0 {
		owner = c.threads[h-1].name
	}
	return &Result{
		Reachable:     true,
		Kind:          kind,
		Assignment:    assignment,
		Trace:         trace,
		Explored:      explored,
		BlockedThread: c.threads[repTI].name,
		RequestedLock: c.lockNames[info.blockedLock],
		Owner:         owner,
		Edge:          edge.name,
		From:          c.threads[repTI].states[edge.from],
	}
}
