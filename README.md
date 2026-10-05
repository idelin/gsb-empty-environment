# bigsqrt

大整数开方工具：接收非负整数 N，输出整数平方根 r 与余数 rem，满足
`r*r <= N < (r+1)*(r+1)` 且 `N = r*r + rem`。

## 特性

- **流式十进制分块输入**：`Builder.Feed` 逐块接收十进制数字串，
  超出位数限额的块在扩容前被拒绝（`ErrDigitLimitExceeded`），已累积的值不变。
- **预算按大整数运算计**：`Budget` 以 big.Int 运算次数计费
  （每次 Feed 计 4 次：解析、乘方、乘、加；每次牛顿迭代计 3 次：
  除、加、移位；收尾余数计 2 次：乘、减）。`nil` 预算表示不限。
- **取消与恢复**：预算耗尽返回 `ErrBudgetExhausted`，状态保持可恢复；
  追加预算后继续 `Step`/`Run` 即可。`State.Snapshot` 深拷贝全部
  缓冲，快照与原状态互不共享可变内存。
- **算法**：整数牛顿迭代，初值 `2^ceil(bitlen/2)`，单调下降至收敛。

## 使用

```go
b, _ := bigsqrt.NewBuilder(1_000_000) // 位数限额
b.Feed("1234567890", budget)          // 逐块喂入
st, _ := bigsqrt.NewState(b.Num())
err := st.Run(budget)                 // ErrBudgetExhausted 可恢复
r, rem, _ := st.Result()
```

命令行：

```sh
echo "1234567890 1234567890" | go run ./cmd/bigsqrt -max-digits 1000000
```

## 测试

```sh
go test ./...
```

覆盖完全平方、相邻整数（k²±1）、与标准库交叉验证、取消/恢复、
快照缓冲隔离、位数限额拒绝，以及百万位输入的运算量上界
（按大整数运算次数断言，不预设固定耗时）。
