package netsec

import (
	"strings"
	"testing"
)

func edge(from, to string) Edge {
	return Edge{From: from, To: to, Kind: EdgeNormal}
}

func exEdge(from, to string) Edge {
	return Edge{From: from, To: to, Kind: EdgeException}
}

func node(id string, kind NodeKind, subject, object string) Node {
	return Node{ID: id, Kind: kind, Subject: subject, Object: object}
}

func findingIDs(result *Result) map[string]Finding {
	out := make(map[string]Finding, len(result.Findings))
	for _, finding := range result.Findings {
		out[finding.NodeID] = finding
	}
	return out
}

func pathIDs(path []PathStep) []string {
	out := make([]string, 0, len(path))
	for _, step := range path {
		out = append(out, step.NodeID)
	}
	return out
}

// TestWrongObject 验证“校验的是另一对象”：对 objB 的授权不能保护对 objA
// 的敏感调用。
func TestWrongObject(t *testing.T) {
	graph := Graph{
		Entry: "entry",
		Nodes: []Node{
			node("entry", KindPlain, "", ""),
			node("checkB", KindAuthCheck, "alice", "objB"),
			node("callA", KindSensitiveCall, "alice", "objA"),
		},
		Edges: []Edge{
			edge("entry", "checkB"),
			edge("checkB", "callA"),
		},
	}

	result, err := Analyze(graph)
	if err != nil {
		t.Fatalf("Analyze 返回错误: %v", err)
	}
	findings := findingIDs(result)
	finding, ok := findings["callA"]
	if !ok {
		t.Fatal("期望 callA 缺失授权保证，实际未报警")
	}
	if finding.RequiredVersion != InitialVersion {
		t.Fatalf("要求版本 = %q, 期望 %q", finding.RequiredVersion, InitialVersion)
	}
	if got := pathIDs(finding.Path); len(got) != 3 || got[0] != "entry" || got[2] != "callA" {
		t.Fatalf("最短路径 = %v, 期望 entry -> checkB -> callA", got)
	}

	// 对照：对同一对象补一次匹配检查后，同一调用应当通过。
	graph.Nodes = append(graph.Nodes, node("checkA", KindAuthCheck, "alice", "objA"))
	// checkA 成为 checkB 与 callA 之间的必经节点。
	graph.Edges = []Edge{
		edge("entry", "checkB"),
		edge("checkB", "checkA"),
		edge("checkA", "callA"),
	}

	result, err = Analyze(graph)
	if err != nil {
		t.Fatalf("补充匹配检查后 Analyze 返回错误: %v", err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("匹配检查后仍报警: %+v", result.Findings)
	}
}

// TestExceptionBypass 验证异常边绕过：正常分支先检查后调用，但异常分支
// 不经检查直达调用，合流后凭据丢失，必须报警，且最短路径走异常边。
func TestExceptionBypass(t *testing.T) {
	graph := Graph{
		Entry: "entry",
		Nodes: []Node{
			node("entry", KindPlain, "", ""),
			node("check", KindAuthCheck, "alice", "doc"),
			node("merge", KindPlain, "", ""),
			node("call", KindSensitiveCall, "alice", "doc"),
		},
		Edges: []Edge{
			edge("entry", "check"),
			exEdge("entry", "merge"),
			edge("check", "merge"),
			edge("merge", "call"),
		},
	}

	result, err := Analyze(graph)
	if err != nil {
		t.Fatalf("Analyze 返回错误: %v", err)
	}
	findings := findingIDs(result)
	finding, ok := findings["call"]
	if !ok {
		t.Fatal("期望异常绕过导致 call 报警，实际未报警")
	}

	gotPath := pathIDs(finding.Path)
	wantPath := []string{"entry", "merge", "call"}
	if len(gotPath) != len(wantPath) {
		t.Fatalf("最短路径 = %v, 期望 %v", gotPath, wantPath)
	}
	for i := range wantPath {
		if gotPath[i] != wantPath[i] {
			t.Fatalf("最短路径 = %v, 期望 %v", gotPath, wantPath)
		}
	}
	if finding.Path[1].Via != EdgeException {
		t.Fatalf("进入 merge 的边 = %q, 期望异常边", finding.Path[1].Via)
	}

	// 合流后版本仍是共同的初始版本，但凭据交集为空。
	view, ok := result.StateAt("merge")
	if !ok || !view.Reachable {
		t.Fatal("merge 状态不可查询或不可达")
	}
	if view.Versions["doc"] != InitialVersion {
		t.Fatalf("merge 处 doc 版本 = %q, 期望 %q", view.Versions["doc"], InitialVersion)
	}
	if len(view.Credentials) != 0 {
		t.Fatalf("合流后凭据 = %v, 期望空集", view.Credentials)
	}
}

// TestStaleCredentialAfterModify 验证版本修改后旧凭据失效：检查在修改前
// 完成，敏感调用在修改后到达，旧版本凭据不能证明新版本。
func TestStaleCredentialAfterModify(t *testing.T) {
	graph := Graph{
		Entry: "entry",
		Nodes: []Node{
			node("entry", KindPlain, "", ""),
			node("check", KindAuthCheck, "alice", "doc"),
			node("modify", KindModifyVersion, "", "doc"),
			node("call", KindSensitiveCall, "alice", "doc"),
		},
		Edges: []Edge{
			edge("entry", "check"),
			edge("check", "modify"),
			edge("modify", "call"),
		},
	}

	result, err := Analyze(graph)
	if err != nil {
		t.Fatalf("Analyze 返回错误: %v", err)
	}
	findings := findingIDs(result)
	finding, ok := findings["call"]
	if !ok {
		t.Fatal("期望旧凭据失效导致 call 报警，实际未报警")
	}
	if !strings.Contains(finding.Reason, "旧版本凭据") {
		t.Fatalf("原因 = %q, 期望提示旧版本凭据", finding.Reason)
	}
	wantVersion := ModifiedVersion + "modify"
	if finding.RequiredVersion != wantVersion {
		t.Fatalf("要求版本 = %q, 期望 %q", finding.RequiredVersion, wantVersion)
	}

	// 修改后重新检查同主体同对象，新凭据匹配新版本，调用通过。
	reauth := Graph{
		Entry: "entry",
		Nodes: []Node{
			node("entry", KindPlain, "", ""),
			node("checkOld", KindAuthCheck, "alice", "doc"),
			node("modify", KindModifyVersion, "", "doc"),
			node("checkNew", KindAuthCheck, "alice", "doc"),
			node("call", KindSensitiveCall, "alice", "doc"),
		},
		Edges: []Edge{
			edge("entry", "checkOld"),
			edge("checkOld", "modify"),
			edge("modify", "checkNew"),
			edge("checkNew", "call"),
		},
	}
	result, err = Analyze(reauth)
	if err != nil {
		t.Fatalf("重新授权图 Analyze 返回错误: %v", err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("重新授权后仍报警: %+v", result.Findings)
	}
}

// TestTransferDoesNotExtendAuthority 验证凭据转移不扩大主体范围：alice 的
// 凭据转移后，bob 发起的敏感调用仍然缺保证。
func TestTransferDoesNotExtendAuthority(t *testing.T) {
	graph := Graph{
		Entry: "entry",
		Nodes: []Node{
			node("entry", KindPlain, "", ""),
			node("check", KindAuthCheck, "alice", "doc"),
			node("transfer", KindTransferCredential, "", ""),
			node("callByBob", KindSensitiveCall, "bob", "doc"),
			node("callByAlice", KindSensitiveCall, "alice", "doc"),
		},
		Edges: []Edge{
			edge("entry", "check"),
			edge("check", "transfer"),
			edge("transfer", "callByBob"),
			edge("transfer", "callByAlice"),
		},
	}

	result, err := Analyze(graph)
	if err != nil {
		t.Fatalf("Analyze 返回错误: %v", err)
	}
	findings := findingIDs(result)
	if _, ok := findings["callByBob"]; !ok {
		t.Fatal("期望 bob 调用报警，实际未报警")
	}
	if _, ok := findings["callByAlice"]; ok {
		t.Fatal("alice 调用不应报警")
	}
}

// TestLoopFixedPoint 验证循环上的有限不动点：循环内的修改使对象在各轮
// 迭代后版本无法形成共同认识，循环后的调用稳定缺保证，且迭代有限。
func TestLoopFixedPoint(t *testing.T) {
	graph := Graph{
		Entry: "entry",
		Nodes: []Node{
			node("entry", KindPlain, "", ""),
			node("check", KindAuthCheck, "alice", "doc"),
			node("head", KindPlain, "", ""),
			node("modify", KindModifyVersion, "", "doc"),
			node("call", KindSensitiveCall, "alice", "doc"),
		},
		Edges: []Edge{
			edge("entry", "check"),
			edge("check", "head"),
			edge("head", "modify"),
			edge("modify", "head"),
			edge("head", "call"),
		},
	}

	result, err := Analyze(graph)
	if err != nil {
		t.Fatalf("Analyze 返回错误: %v", err)
	}
	if result.Iterations <= 1 {
		t.Fatalf("迭代轮数 = %d, 期望多轮传播后收敛", result.Iterations)
	}

	view, ok := result.StateAt("head")
	if !ok || !view.Reachable {
		t.Fatal("head 状态不可查询或不可达")
	}
	if view.Versions["doc"] != UnknownVersion {
		t.Fatalf("循环合流版本 = %q, 期望 %q", view.Versions["doc"], UnknownVersion)
	}
	if len(view.Credentials) != 1 || view.Credentials[0].Version != InitialVersion {
		t.Fatalf("循环合流凭据 = %v, 期望仅保留初始版本旧凭据", view.Credentials)
	}

	findings := findingIDs(result)
	if _, ok := findings["call"]; !ok {
		t.Fatal("期望循环后的 call 报警，实际未报警")
	}
}

// TestMergeIntersection 验证合流取交集：两条分支各检查不同对象，合流后
// 对任一对象的敏感调用都只在对应分支有保证，因此两个调用都报警。
func TestMergeIntersection(t *testing.T) {
	graph := Graph{
		Entry: "entry",
		Nodes: []Node{
			node("entry", KindPlain, "", ""),
			node("checkA", KindAuthCheck, "alice", "a"),
			node("checkB", KindAuthCheck, "alice", "b"),
			node("merge", KindPlain, "", ""),
			node("callA", KindSensitiveCall, "alice", "a"),
			node("callB", KindSensitiveCall, "alice", "b"),
		},
		Edges: []Edge{
			edge("entry", "checkA"),
			edge("entry", "checkB"),
			edge("checkA", "merge"),
			edge("checkB", "merge"),
			edge("merge", "callA"),
			edge("merge", "callB"),
		},
	}

	result, err := Analyze(graph)
	if err != nil {
		t.Fatalf("Analyze 返回错误: %v", err)
	}
	if len(result.Findings) != 2 {
		t.Fatalf("报警数 = %d, 期望 2: %+v", len(result.Findings), result.Findings)
	}

	view, ok := result.StateAt("merge")
	if !ok || !view.Reachable || len(view.Credentials) != 0 {
		t.Fatalf("合流凭据 = %v, 期望空集", view.Credentials)
	}
}

// TestUnreachableAndValidation 验证不可达节点不计入报警，以及结构性
// 校验错误能被宿主识别。
func TestUnreachableAndValidation(t *testing.T) {
	graph := Graph{
		Entry: "entry",
		Nodes: []Node{
			node("entry", KindPlain, "", ""),
			node("deadCall", KindSensitiveCall, "alice", "doc"),
		},
		Edges: nil,
	}
	result, err := Analyze(graph)
	if err != nil {
		t.Fatalf("Analyze 返回错误: %v", err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("不可达节点不应报警: %+v", result.Findings)
	}
	if len(result.Unreachable) != 1 || result.Unreachable[0] != "deadCall" {
		t.Fatalf("不可达节点 = %v, 期望 [deadCall]", result.Unreachable)
	}

	bad := Graph{
		Entry: "missing",
		Nodes: []Node{node("n", KindSensitiveCall, "alice", "doc")},
	}
	if _, err := Analyze(bad); err == nil {
		t.Fatal("入口缺失时期望错误，实际返回 nil")
	}
}
