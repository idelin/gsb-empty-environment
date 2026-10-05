# bigsqrt

非负大整数开方工具：给定 `N >= 0`，输出整数平方根 `r` 与余数，满足
`r^2 <= N < (r+1)^2` 且 `rem = N - r^2`。

## 特性

- **流式十进制分块输入**：`Stream.Feed` 逐块接收 ASCII 十进制数字，
  硬性的位数上限（`NewStream(maxDigits)`）；超限的块整体拒绝、流被
  封存，后续 `Feed` 一律返回 `ErrDigitLimit`，缓冲区不再扩容。
- **预算按大整数运算计**：`Task.Step(ctx, budget)` 每次调用最多消耗
  `budget` 次大整数算术运算（除、乘、加、移位各计 1），耗尽返回
  `ErrBudgetExhausted`，与耗时无关。
- **取消与恢复**：`Step` 每次迭代检查 `ctx`；取消或预算耗尽后可用
  `Snapshot`（纯字符串/标量，可 JSON 序列化）保存状态，`Restore`
  后继续。快照与 `Result` 均为深拷贝，不与 `Task` 共享可变缓冲。
- **资源上界**：牛顿迭代从 `2^ceil(bitlen/2)` 起步，迭代次数为
  `O(log bitlen)`；百万位输入实测 57 次大整数运算（测试断言上界
  `6*log2(bitlen)+16`，不含任何固定耗时假设）。

## 用法

```go
s := bigsqrt.NewStream(1_000_000)
for _, chunk := range chunks {
    if err := s.Feed(chunk); err != nil { /* 非法字符或超限 */ }
}
n, _ := s.Int()

task, _ := bigsqrt.NewTask(n)
for {
    done, err := task.Step(ctx, 100) // 每轮最多 100 次大整数运算
    if done { break }
    if err == bigsqrt.ErrBudgetExhausted || errors.Is(err, context.Canceled) {
        snap := task.Snapshot()      // 可序列化保存
        task, _ = bigsqrt.Restore(snap) // 之后恢复
        continue
    }
}
res, _ := task.Result() // res.Root, res.Remainder, res.Ops
```

一次性接口：`root, rem, ops, err := bigsqrt.Sqrt(n)`。

## 测试

```sh
go test ./...                 # 全部（含百万位，约 1 分钟）
go test -run TestMillionDigits -v .
```
