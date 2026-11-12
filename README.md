# crate审计增量链与违规区间

依赖门禁用审计记录判断 crate 版本能否部署，攻击者发布的版本落在 violation 区间，或借 delta 审计链接到一个未被完整审计的起点。为Go门禁实现可导入的审计链解析包：完整审计给出 version，增量审计给出 from->to；一个版本通过当且仅当存在从某完整审计出发、沿同一 criteria 的增量边可达它的路径；violation 的 semver 区间内的任何版本一律不通过，且 violation 优先于审计；safe-to-deploy 蕴含 safe-to-run，反向不成立；预发布版本按 semver 排序，+ 构建元数据忽略。用环、断链、区间边界和 criteria 蕴含验证。

## 包结构（`import "example.org/netsec/q0245"`，包名 `netsec`）

- `doc.go`：包级语义说明与端到端示例。
- `semver.go`：严格 semver 2.0 解析与优先级比较；预发布按 spec 排序，`+` 构建元数据不参与比较，`Version.Canonical()` 给出图节点身份。
- `range.go`：semver 区间解析（`*`、`= < <= > >=`，空格分隔为合取）与 `Range.Contains`。
- `audit.go`：审计存储与门禁判定（`Store`、`FullAudit`、`DeltaAudit`、`Violation`、`Evaluate`）。

## 判定规则

1. **violation 优先**：目标版本命中任一适用 violation 区间即拒绝（`ReasonViolation`），即使存在可达审计链。空 criteria 或 `*` 表示通配，拦截所有 criteria；带 criteria 的 violation 仅在查询 criteria 蕴含它时拦截。
2. **审计链可达**：从某 `FullAudit` 锚点出发，沿 `DeltaAudit` 的 `from -> to` 有向边做 BFS；锚点与每条边的 criteria 必须蕴含查询 criteria，到达目标才通过（`ReasonReachable`）。
3. **断链拒绝**：delta 链起点没有完整审计锚点时拒绝（`ReasonBrokenChain`）；有锚点但目标不可达时为 `ReasonNoAudit`。环通过 visited 集合保证终止，自环/互环不会伪造信任。
4. **criteria 蕴含**：`NewStore` 内置 `safe-to-deploy -> safe-to-run`（可传递，可用 `AddImplication` 扩展）；反向添加返回 `ErrImplicationCycle`。
5. **版本规则**：预发布严格按 semver 排序；构建元数据在比较和图身份中忽略，无法用来逃逸 violation 区间。

## 快速使用

```go
store := netsec.NewStore()
err := store.AddRecords(
    netsec.Record{Full: &netsec.FullAudit{Crate: "serde", Version: "1.0.0", Criteria: netsec.SafeToDeploy}},
    netsec.Record{Delta: &netsec.DeltaAudit{Crate: "serde", From: "1.0.0", To: "1.1.0", Criteria: netsec.SafeToDeploy}},
    netsec.Record{Viol: &netsec.Violation{Crate: "serde", Range: ">=2.0.0"}},
)
verdict, err := store.Evaluate("serde", "1.1.0", netsec.SafeToDeploy)
// verdict.Allowed == true
```

`go test ./...` 运行全部用例（含环、断链、区间边界、预发布排序与 criteria 蕴含方向）。
