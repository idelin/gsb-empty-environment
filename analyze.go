package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// Analyze 在控制流图上求授权保证的有限不动点，返回缺少保证的敏感调用。
//
// 分析对异常边与普通边一视同仁；合流只保留所有路径共同保证的凭据与版本。
// 每个 [Finding] 都带有一条边数最少的违反路径。
func Analyze(graph Graph) (*Result, error) {
	if err := validate(graph); err != nil {
		return nil, err
	}

	nodes := make(map[string]Node, len(graph.Nodes))
	objects := make(map[string]struct{})
	order := make([]string, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
		order = append(order, node.ID)
		if node.Object != "" {
			objects[node.Object] = struct{}{}
		}
	}

	preds := make(map[string][]string, len(order))
	succs := make(map[string][]edgeRef, len(order))
	for _, edge := range graph.Edges {
		preds[edge.To] = append(preds[edge.To], edge.From)
		succs[edge.From] = append(succs[edge.From], edgeRef{to: edge.To, kind: edge.kind()})
	}

	entry := initialState(sortedKeys(objects))

	// 所有节点初始化为格顶；入口再叠加具体入口状态。
	states := make(map[string]flowState, len(order))
	for _, id := range order {
		states[id] = topState()
	}

	// 格高上界：每个对象版本至多从具体退化为未知一次，每条凭据至多
	// 被删除一次，再加一次入口初始化。迭代超过该上界说明实现有误。
	maxRounds := len(order)*(len(objects)+credentialUniverse(graph)+1) + 2

	iterations := 0
	changed := true
	for changed {
		iterations++
		if iterations > maxRounds {
			return nil, fmt.Errorf("netsec: 不动点迭代超过 %d 轮仍未收敛", maxRounds)
		}
		changed = false

		for _, id := range order {
			old := states[id]

			var incoming flowState
			if id == graph.Entry {
				incoming = entry.clone()
			} else {
				incoming = mergePreds(preds[id], states)
			}
			next := transfer(incoming, nodes[id])

			if !next.equal(old) {
				states[id] = next
				changed = true
			}
		}
	}

	result := &Result{Iterations: iterations}
	for _, node := range graph.Nodes {
		state := states[node.ID]
		if state.top {
			result.Unreachable = append(result.Unreachable, node.ID)
			continue
		}
		if node.Kind != KindSensitiveCall {
			continue
		}
		if reason, requiredVersion, missing := evaluateSensitive(node, state); missing {
			path := shortestViolation(graph, nodes, succs, entry, node, requiredVersion)
			result.Findings = append(result.Findings, Finding{
				NodeID:          node.ID,
				Subject:         node.Subject,
				Object:          node.Object,
				RequiredVersion: requiredVersion,
				Reason:          reason,
				Path:            path,
			})
		}
	}

	result.states = states
	result.nodes = nodes
	return result, nil
}

// edgeRef 是带类型的后继边。
type edgeRef struct {
	to   string
	kind EdgeKind
}

// mergePreds 合并前驱状态；无前驱且非入口的节点保持格顶。
func mergePreds(predIDs []string, states map[string]flowState) flowState {
	if len(predIDs) == 0 {
		return topState()
	}
	predStates := make([]flowState, 0, len(predIDs))
	for _, id := range predIDs {
		predStates = append(predStates, states[id])
	}
	return mergeStates(predStates)
}

// evaluateSensitive 判定敏感调用入口状态是否持有完全匹配的凭据。
// 返回原因、要求版本与是否缺失。
func evaluateSensitive(node Node, state flowState) (reason, requiredVersion string, missing bool) {
	version, ok := state.versions[node.Object]
	if !ok {
		return fmt.Sprintf("对象 %q 无版本信息，无法匹配任何凭据", node.Object), UnknownVersion, true
	}
	if version == UnknownVersion {
		return fmt.Sprintf("对象 %q 在到达路径上版本不一致，无共同版本可匹配", node.Object), UnknownVersion, true
	}

	required := Credential{Subject: node.Subject, Object: node.Object, Version: version}
	if _, ok := state.creds[required]; ok {
		return "", version, false
	}

	var sameObject, sameSubject []Credential
	for cred := range state.creds {
		if cred.Object != node.Object {
			continue
		}
		sameObject = append(sameObject, cred)
		if cred.Subject == node.Subject {
			sameSubject = append(sameSubject, cred)
		}
	}

	switch {
	case len(sameObject) == 0:
		reason = fmt.Sprintf("主体 %q 对对象 %q（版本 %q）无任何授权凭据", node.Subject, node.Object, version)
	case len(sameSubject) == 0:
		reason = fmt.Sprintf("对象 %q 的凭据均不属于主体 %q（版本 %q）", node.Object, node.Subject, version)
	default:
		reason = fmt.Sprintf("主体 %q 对对象 %q 仅持有旧版本凭据，要求版本 %q", node.Subject, node.Object, version)
	}
	return reason, version, true
}

// bfsFrame 是最短路径 BFS 的一帧，state 为进入 nodeID 前的状态。
type bfsFrame struct {
	nodeID string
	state  flowState
	via    EdgeKind
	parent string
}

// shortestViolation 在具体（不做合流）路径状态上做 BFS，返回边数最少的
// 违反路径。状态空间由有限的版本集合与凭据子集构成，必然终止。
func shortestViolation(
	graph Graph,
	nodes map[string]Node,
	succs map[string][]edgeRef,
	entry flowState,
	target Node,
	requiredVersion string,
) []PathStep {
	required := Credential{Subject: target.Subject, Object: target.Object, Version: requiredVersion}

	startKey := stateKey(graph.Entry, entry)
	queue := []bfsFrame{{nodeID: graph.Entry, state: entry, via: EdgeNormal, parent: ""}}
	seen := map[string]struct{}{startKey: {}}
	parents := map[string]bfsFrame{startKey: {nodeID: graph.Entry, state: entry, via: EdgeNormal, parent: ""}}

	for len(queue) > 0 {
		frame := queue[0]
		queue = queue[1:]
		key := stateKey(frame.nodeID, frame.state)

		if frame.nodeID == target.ID {
			if _, ok := frame.state.creds[required]; !ok {
				return reconstructPath(parents, key)
			}
		}

		nextState := transfer(frame.state, nodes[frame.nodeID])
		for _, edge := range succs[frame.nodeID] {
			nextKey := stateKey(edge.to, nextState)
			if _, ok := seen[nextKey]; ok {
				continue
			}
			seen[nextKey] = struct{}{}
			parents[nextKey] = bfsFrame{nodeID: edge.to, via: edge.kind, parent: key}
			queue = append(queue, bfsFrame{nodeID: edge.to, state: nextState, via: edge.kind, parent: key})
		}
	}

	// 不动点已判定缺失，BFS 理论上必能找到一条违反路径。
	return []PathStep{{NodeID: target.ID, Via: EdgeNormal}}
}

// reconstructPath 依 parent 链回溯并反转为入口到目标的顺序。
func reconstructPath(parents map[string]bfsFrame, targetKey string) []PathStep {
	var reversed []PathStep
	key := targetKey
	for key != "" {
		frame, ok := parents[key]
		if !ok {
			break
		}
		if frame.nodeID == "" {
			break
		}
		reversed = append(reversed, PathStep{NodeID: frame.nodeID, Via: frame.via})
		key = frame.parent
	}

	// 回溯链依次为目标、……、入口，反转为入口到目标。
	path := make([]PathStep, len(reversed))
	for i, step := range reversed {
		path[len(reversed)-1-i] = step
	}
	return path
}

// StateAt 返回节点在不动点处的抽象状态视图，供宿主解释报警或调试。
func (r *Result) StateAt(nodeID string) (StateView, bool) {
	state, ok := r.states[nodeID]
	if !ok || state.top {
		return StateView{Reachable: false}, true
	}

	versions := make(map[string]string, len(state.versions))
	for object, version := range state.versions {
		versions[object] = version
	}
	creds := make([]Credential, 0, len(state.creds))
	for cred := range state.creds {
		creds = append(creds, cred)
	}
	sort.Slice(creds, func(i, j int) bool {
		if creds[i].Object != creds[j].Object {
			return creds[i].Object < creds[j].Object
		}
		if creds[i].Subject != creds[j].Subject {
			return creds[i].Subject < creds[j].Subject
		}
		return creds[i].Version < creds[j].Version
	})
	return StateView{Versions: versions, Credentials: creds, Reachable: true}, true
}

// credentialUniverse 估算可能出现的凭据数量，用于格高上界。
func credentialUniverse(graph Graph) int {
	subjects := make(map[string]struct{})
	objects := make(map[string]struct{})
	for _, node := range graph.Nodes {
		if node.Subject != "" {
			subjects[node.Subject] = struct{}{}
		}
		if node.Object != "" {
			objects[node.Object] = struct{}{}
		}
	}
	return len(subjects) * len(objects)
}

// sortedKeys 返回字典序排序的键。
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// validate 做结构性校验，返回带上下文的错误。
func validate(graph Graph) error {
	if len(graph.Nodes) == 0 {
		return errors.New("netsec: 图为空")
	}
	seen := make(map[string]struct{}, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node.ID == "" {
			return errors.New("netsec: 存在空 ID 节点")
		}
		if _, dup := seen[node.ID]; dup {
			return fmt.Errorf("netsec: 节点 ID 重复: %q", node.ID)
		}
		seen[node.ID] = struct{}{}

		switch node.Kind {
		case KindAuthCheck, KindSensitiveCall:
			if node.Subject == "" {
				return fmt.Errorf("netsec: 节点 %q 缺少 Subject", node.ID)
			}
			if node.Object == "" {
				return fmt.Errorf("netsec: 节点 %q 缺少 Object", node.ID)
			}
		case KindModifyVersion:
			if node.Object == "" {
				return fmt.Errorf("netsec: 节点 %q 缺少 Object", node.ID)
			}
		case KindTransferCredential, KindPlain:
		default:
			return fmt.Errorf("netsec: 节点 %q 的类型未知: %q", node.ID, node.Kind)
		}
	}

	if _, ok := seen[graph.Entry]; !ok {
		return fmt.Errorf("netsec: 入口节点 %q 不存在", graph.Entry)
	}
	for _, edge := range graph.Edges {
		if _, ok := seen[edge.From]; !ok {
			return fmt.Errorf("netsec: 边引用了不存在的起点: %q", edge.From)
		}
		if _, ok := seen[edge.To]; !ok {
			return fmt.Errorf("netsec: 边引用了不存在的终点: %q", edge.To)
		}
		switch edge.kind() {
		case EdgeNormal, EdgeException:
		default:
			return fmt.Errorf("netsec: 边 %q->%q 类型未知: %q", edge.From, edge.To, edge.Kind)
		}
	}
	return nil
}
