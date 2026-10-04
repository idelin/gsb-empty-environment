# windowperiod

字节流滑动窗口库：保留最近 `W` 字节，支持撤销（Undo）最后一次追加，
并查询窗口的**最短完整周期**——周期长度 `p` 必须整除窗口长度 `W`，
整个窗口恰好是周期串重复 `W/p` 次。查询返回周期串与重复次数。

## 用法

```go
win := windowperiod.New(6)          // 窗口长度 W=6
for _, b := range []byte("abcabcabc") {
    win.Append(b)                   // 追加，窗口满时挤出最旧字节
}
period, repeats, ok := win.Period() // "abc", 2, true
win.Undo()                          // 撤销最后一次 Append（可连续多级撤销）
```

窗口未填满（`Len() < W`）时 `Period` 返回 `ok=false`。

## 设计

- **环形缓冲**：容量 `W+1`，按绝对下标取模存放字节，`Append`/`Undo` 均 O(1)。
- **双滚动哈希**（mod 2^64 自然溢出，两个独立基）：每个槽位保存到该位置为止的
  前缀哈希，任意区间哈希 O(1) 取得。
- **O(1) 周期判定**：窗口是 p-周期 ⟺ 前 `W-p` 字节等于后 `W-p` 字节，
  用哈希比较代替逐字节扫描。每次查询只枚举 `W` 的因子（升序），
  复杂度 O(d(W))，d 为因子个数——不会每步对所有候选重新比较全部字节。
  查询结果按 `(start, end)` 记忆化，重复查询零开销。
- **撤销**：每次 `Append` 压入一条记录；若发生挤出，记录保存被挤出字节及其
  哈希状态。`Undo` 弹出记录并精确还原，可多级撤销。
  注意 `F[start-1]`（窗口前一字位置的前缀哈希）不能依赖环槽位
  （可能已被后来追加又撤销的数据覆盖），因此由 `baseH1/baseH2` 字段单独维护。
- 内存：环形缓冲 O(W)；撤销历史 O(追加次数)。

## 验证

`window_test.go` 以**朴素枚举因子 + 逐字节比较**为参考实现对照：

- `TestSameByteLongString`：同字节长串（5000 个相同字节，W=512，含 100 次撤销）。
- `TestPrimeWindowLength`：质数窗口长度 W=13（因子只有 1 和 13）。
- `TestWindowSlidesPastPeriodBreakpoint`：窗口滑过周期断点
  （`abcd` 周期流 → 常量流 → `xy` 周期流），逐步对照周期变化。
- `TestUndo` / `TestIncompleteWindow`：多级撤销、撤销后改追加、空历史撤销、
  未填满查询。
- `TestRandomVsNaive`：W ∈ {1,2,3,4,6,12,13,16,36,97,120}，各 3000 步随机
  追加/撤销，每步与朴素参考逐状态比对。

运行测试：

```sh
go test ./...        # 或 go test -race ./...
```
