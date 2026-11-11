# 匿名分组多样性证明

仅满足组大小（k-匿名）不能阻止敏感属性单一泄漏：一个规模足够大但敏感值同构的组，
仍会让攻击者确定目标属性。本包（`package netsec`，无第三方依赖）供 Go 发布宿主
嵌入调用，在泛化分组之上强制执行 l-多样性与有理占比上限，并支持记录撤回后的
全量重分组对照与最小违规组证据。

## 核验语义

- **准标识归组**：每个准标识配一棵泛化树（`NewTree`，子→父边构建，构建期校验
  多根、环、重复父节点等）。`Config.Levels` 给出每个属性向上泛化的步数，
  泛化后的向量相同的记录进入同一组；步数越界或值不在树中直接报错。
- **多样性（至少 L 种）**：每组**已知**敏感值去重计数必须 ≥ L。
- **占比上限**：任一已知敏感值的组内占比不得严格大于有理阈值 `MaxRatio`
  （`big.Rat` 精确比较，`== 阈值` 判合规，杜绝浮点边界问题）。分母口径显式可选：
  `RatioOverAll`（全组记录含未知，默认、更严格）或 `RatioOverKnown`（仅已知记录）。
- **可选组大小**：`MinGroupSize > 0` 时额外检查 k-匿名，违规原因以位掩码叠加。
- **未知敏感值不凑数**：未知记录（默认 `UnknownSensitive` 即空字符串，可用
  `Config.UnknownSentinel` 自定义）是真实记录、计入组大小，但既不充当一个敏感
  类别参与 L 计数，也不进入占比分子；因此 `L-1` 个已知类别 + 任意多条未知记录
  仍判多样性违规。

## 最小违规组证据

`Report.Violations` 按组大小升序、组键字典序排列，`Report.MinimalViolation()`
直接返回最小违规组：

- `DiversityEvidence`：最小代表集——每个已知敏感值各取一条记录 ID，长度即实际种类数；
- `RatioEvidence`：每个越界值的计数、精确比值、阈值、分母口径与全部成员 ID；
- `Reasons` 位掩码（`group-size` / `diversity` / `ratio`）同时给出全部命中原因。

## 撤回与全量重分组对照

`Verifier.Withdraw(id)` 先原子校验 ID（未知 ID 返回包装 `ErrUnknownRecord` 的错误，
状态不变），删除后对**剩余全量记录**重新泛化分组、重新核验——不做增量补丁，
避免局部状态漂移。返回的 `UpdateResult` 与上一份报告逐组对照：

- `GroupChanges`：受影响组的 `removed`/`shrunk` 变化、前后大小与被撤回成员；
- `NewlyViolating` / `Resolved` / `ReasonChanged`：合规状态与原因集合的翻转。

## 快速上手

```go
tree, _ := netsec.NewTree("region", [][2]string{
    {"海淀", "北京"}, {"朝阳", "北京"}, {"北京", "*"},
})
limit, _ := netsec.NewThreshold(1, 2) // 1/2

v, _ := netsec.New(netsec.Config{
    QuasiIDs: []string{"region"},
    Trees:    []*netsec.Tree{tree},
    Levels:   []int{1},
    L:        2,
    MaxRatio: limit,
})
_ = v.Replace(records)
report, _ := v.RegroupAndVerify()
if !report.Compliant() {
    min := report.MinimalViolation() // 最小违规组及其证据
}
update, err := v.Withdraw("record-id") // 全量重分组 + 对照
```

一次性核验可直接用 `netsec.Verify(cfg, records)`。所有 `Verifier` 方法均有
`sync.RWMutex` 保护，可并发调用。

## 文件结构

- `tree.go`：泛化树构建、校验与按步数泛化
- `verify.go`：配置、记录、组键与有理阈值类型
- `analyze.go`：分组评估、多样性/占比检查与证据构造
- `verifier.go`：核验器生命周期、全量重分组与报告
- `withdraw.go`：撤回与报告间逐组对照
- `*_test.go`：单元测试、并发测试与可运行示例（`go test ./...`）
