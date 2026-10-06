# incjoin

增量左连接库（Go，多重集语义）。报表持续接收左右两表的插入/删除事件，
每个事件返回 join 结果的**差量**（`[]Delta`），下游报表只需应用差量即可，
无需重算全量。

## 语义

- **按键匹配**：`Row.HasKey == false` 表示 NULL 键，NULL 键互不匹配
  （`NULL ≠ NULL`），右表 NULL 键行永不产生输出。
- **空补行**：左行无匹配右行时输出 null 补行（`OutRow.HasRight == false`）。
  某键右侧从 0 条变 1 条时，撤回该键全部 null 补行；最后一条右行被删除时，
  补回 null 补行。
- **多重集**：重复行按次数计算，左右重复度相乘；差量 `Delta.Count` 可正可负。
- **校验**：`FullJoin(left, right)` 从头计算全量连接，作为差量累计结果的对照。

## 用法

```go
j := incjoin.New()
out := map[incjoin.OutRow]int{}
incjoin.Apply(out, j.InsertLeft(incjoin.Row{Key: "a", HasKey: true, Payload: "L1"}))
d, _ := j.DeleteRight(r)
incjoin.Apply(out, d)
// 随时可用 incjoin.FullJoin(left, right) 与 out 比对
```

## 测试

```sh
go test ./...
```

包含脚本化场景（0→1 撤回、1→0 补回、NULL 键、重复度）与 4000 步随机事件流，
每个事件后及最终都与 `FullJoin` 全量结果比对。
