# poset

Go 分析库：输入多个案例的事件偏序（每事件带标签），判断给定标签模式
是否能作为**某个拓扑序的连续子串**出现。并发事件可以任意交错，因此
不能仅按时间（或 ID）排序后判断——必须搜索线性扩展空间。

## 能力

- **模式匹配** `Case.Match(pattern)`：枚举案例的线性扩展（每案例 ≤ 12
  事件），返回每个匹配的**完整见证拓扑序**及窗口位置；不可匹配时返回
  **已闭合搜索范围**（`SearchReport.Closed` + 保留的全部已探查拓扑序）。
- **增量维护**：事件可撤回（`RetractEvent`）、先后关系可补入/移除
  （`AddPrecedence` / `RemovePrecedence`）。每次变更使已有匹配**增量失效**，
  旧版本证据保留在 `Case.History()` 中并记录失效原因。
- **原子拒绝成环**：补入会成环的先后关系时返回 `CycleError`（含环路径），
  案例状态、版本号与已有证据完全不变。
- **全枚举核对**：`BruteForce` 以全排列筛选独立重算，`ConsistentWithBruteForce`
  校验匹配集合与已探查范围完全一致，覆盖重复标签与并发交错场景。

## 示例

```go
c := poset.NewCase("case-1")
c.AddEvent("e1", "a")
c.AddEvent("e2", "b")
c.AddEvent("e3", "c")
c.AddPrecedence("e1", "e2")      // e1 必须先于 e2
if err := c.AddPrecedence("e2", "e1"); err != nil {
    // *poset.CycleError：原子拒绝，状态不变
}

snap, _ := c.Match([]string{"c", "a"})
for _, m := range snap.Matches {
    fmt.Println(m.WindowStart, m.Witness) // 完整见证拓扑序
}
if !snap.Matched() && snap.Search.Closed {
    // snap.Search.Extensions 即已闭合（穷竭）的搜索范围
}

c.RetractEvent("e3")             // 已有匹配增量失效
for _, old := range c.History() { // 旧版本证据仍可查
    fmt.Println(old.Version, old.Valid, old.InvalidatedBy)
}
```

## 测试

```sh
go test ./...
```

随机化测试以全排列枚举对数百个随机偏序做一致性核对。
