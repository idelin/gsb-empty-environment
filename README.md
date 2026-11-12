# 同一上传请求经网关和后端解析后，攻击者可能让附件审核的字节与最终执行的字节不同

同一上传请求经网关和后端解析后，攻击者可能让附件审核的字节与最终执行的字节不同。写供Go接入测试宿主调用的解析分歧包，两侧回调返回字段名、原始区间、重复项取舍及执行入口；只有被审核区间没有完整覆盖后端执行入口依赖区间时报告绕过，普通字段排序差异不告警。输入给定至多12处重复字段插入或分隔符替换候选，搜索最少变更的绕过样本并返回双方区间证据，用独立区间展开与安全样本验证。

## 包结构（package taskpkg）

- `interval.go`：区间类型、重复项取舍策略、字段出现/入口/回报结构，以及**独立**区间展开
  （`ExpandIntervals`）与覆盖缺口计算（`CoverGap` / `ContainsAll`）。
- `mutation.go`：候选变异（`KindInsert` 重复字段插入、`KindReplace` 分隔符替换）、
  校验（`ValidateCandidates`，上限 12）与无状态应用（`ApplyCandidates`，输出独立切片）。
- `engine.go`：单侧回调 `Parser`、单样本评估 `Evaluate`、最少变更搜索 `Searcher.Search`
  及双方区间证据 `BypassReport`/`SideEvidence`。

## 判定语义

- 测试宿主分别提供网关（审核侧）与后端（执行侧）两个 `Parser` 回调，回调返回字段名、
  原始半开区间 `[Start,End)`、重复项取舍（`PolicyFirst/Last/All`）以及入口依赖区间。
- 引擎对同一份字节独立调用两侧解析器，并各自独立归并区间，不共享缓存。
- 仅当后端执行入口 `DependOn` 存在未被网关 `Audited` 完整覆盖的字节时才置 `Bypassed`，
  原因固定为 `ReasonUncovered`；纯字段排序差异因覆盖等价而不告警。
- 任一侧 `Rejected`（fail-closed 拒绝）或后端无执行入口时不构成绕过。
- 搜索先验证零变异基线安全（否则返回 `ErrUnsafeBaseline`），再按变更数从小到大、
  同层按候选索引字典序枚举，返回首个最少变更命中及其变异字节与双方区间证据。

## 接入示例

```go
report, err := taskpkg.Searcher{
    Base:       rawRequestBody,
    Candidates: candidates, // 至多 12 个插入/替换候选
    Gateway:    gatewayParser,
    Backend:    backendParser,
}.Search()
if err != nil { /* 基线不安全或候选/回调非法 */ }
if report != nil {
    // report.Picks / report.Mutated / report.Uncovered
    // report.Verdict.Gateway / report.Verdict.Backend 含双方字段、策略与区间证据
}
```

## 验证

`go vet ./...` 与 `go test ./...` 覆盖：独立区间展开与覆盖缺口、安全样本基线、
重复字段插入绕过、CRLF→裸 LF 分隔符替换绕过、字段排序不告警、12 候选最少变更搜索、
fail-closed 拒绝、无入口不告警、候选非法路径以及变异输出不别名输入。
