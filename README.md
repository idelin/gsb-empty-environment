# wsample

加权无放回采样库（Go）。

## 特性

- **可变非负整数权重**：`SetWeight` 随时调整；权重和用 `big.Int` 累积，不会溢出。
- **零权项不抽**：权重为 0 的项占据的累计区间为空，永远不会被选中。
- **无取模偏差**：`[0, total)` 上的均匀值由拒绝采样产生（读出 `bitLen(total)` 位，落在 `[total, 2^bitLen)` 的值直接丢弃重抽），绝不使用取模。
- **调用方提供随机字节**：实现 `Stream` 接口（`io.Reader` + `Pos()`）。内置 `BytesStream`（可 Seek，适合重放）与 `CountingStream`（包装任意 `io.Reader`）。相同字节流必然产生相同结果。
- **每次抽 K 项**：`DrawK(k, cancel)`；逐项原子提交。
- **取消与恢复**：`cancel` 在每一项抽取前检查。取消后已抽出的项保持已提交状态（从集合移除、流已前进），不存在半抽半回滚。`Snapshot` 记录权重、存活集合与随机流精确位置；`Restore` 校验流位置后重建采样器，继续抽剩余项，结果与不中断运行完全一致。

## 示例

```go
stream := wsample.NewBytesStream(randomBytes)
s := wsample.New([]uint64{3, 0, 5, 2}, stream)

items, err := s.DrawK(2, nil)          // 抽 2 项
snap := s.Snapshot()                   // 持久化：集合状态 + 流位置

// 之后（甚至另一进程）恢复：
stream2 := wsample.NewBytesStream(randomBytes)
stream2.Seek(snap.StreamPos)
s2, _ := wsample.Restore(snap, stream2)
rest, _ := s2.DrawK(1, nil)
```

## 测试

- 小权重下枚举整个随机域（每个被接受的均匀值序列 ↔ 唯一字节流），用 `big.Rat` 核对每种有序结果的精确概率与理论值一致，且总概率为 1。
- 固定字节流两次运行结果一致（可重放）；取消→快照→恢复→续抽与不中断运行结果一致。
- 拒绝采样、零权项、大整数权重和、错误前置检查不消耗字节等边界。

运行：`go test ./...`
