# prefixdict

多来源加权词典的 Go 库：按前缀返回总权重最高的 k 个词。

## 语义

- **多来源聚合**：每个词可携带多个来源的整数权重，总分为各来源之和（可为零或负数）。
- **来源级原子替换**：`ReplaceSource(src, batch)` 在一个临界区内整体换掉该来源的贡献；同一来源重复更新是替换而非累加；`RemoveSource(src)` 只撤销该来源的贡献。
- **零权重词**：只要还有一个来源贡献，词就存在（总权重为零也会被 top-k 返回）；最后一个来源删除后词才消失。
- **排序**：总分降序，并列按 UTF-8 字节序（Go 字符串 `<`）。
- **快照**：`Snapshot()` 是不可变视图，持旧快照的查询仍见旧权重。
- **后台重建**：`StartRebuild()` 在不阻塞写入的情况下构建新索引；`Finish()` 先把构建期间提交的差量（delta）补齐再原子切换。索引的缓存分数层允许滞后，查询播种候选堆时以权威分数使旧分数失效——赢家权重下降后次优自然晋升。

## 用法

```go
d := prefixdict.New()
d.ReplaceSource("users", map[string]int64{"apple": 5, "apricot": 9})
d.ReplaceSource("ads",   map[string]int64{"apple": 2})

d.TopK("ap", 2) // [{apricot 9} {apple 7}]

snap := d.Snapshot()
d.ReplaceSource("users", map[string]int64{"apple": 1})
snap.TopK("ap", 2) // 仍是旧权重 [{apricot 9} {apple 7}]

r := d.StartRebuild() // 后台构建
d.ReplaceSource("ads", map[string]int64{"app": 100}) // 与重建交错的写入
r.Finish()           // 切换前补齐差量
```

## 测试

```sh
go test ./...
```

`model_test.go` 用全表聚合排序模型随机核对：重复来源更新、Unicode/任意字节前缀、
零权重词、快照隔离，以及与重建交错的风暴测试（`-race` 干净）。
