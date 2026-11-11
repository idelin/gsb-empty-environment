# 整数截断导致分配不足

二进制解码团队需要在审查中发现长度计算漏洞。给Go分析宿主写可导入的位向量分析包，输入无循环SSA图，支持8/16/32位无符号加乘、截断、扩展、比较和分支；标记分配字节数与随后写入数量。找出可达的写入越界，返回满足路径条件的具体输入；最多八个输入字节，可设搜索预算，预算耗尽返回未知。用小位宽穷举核对安全、漏洞、未知三个结果。

## 用法

```go
b := netsec.NewBuilder()
x := b.Input(0, netsec.W8)              // 第 0 个输入字节
n := b.Mul(b.Ext(x, netsec.W32), b.Const(netsec.W32, 32))
b.Alloc(b.Trunc(n, netsec.W8))          // 分配：长度被截断到 8 位
b.Write(n)                              // 写入：完整 32 位长度
g, err := b.Build()
res := netsec.Analyze(g, netsec.Options{Budget: 1 << 16})
// res.Verdict == netsec.Vulnerable，res.Witness[0] == 8（8*32=256 截断为 0）
```

## 语义

- `Builder` 逐块构建无循环 SSA 图：分支只能指向编号更大的块，`Build`
  校验无环、可达性与支配关系。输入为最多 8 个字节，`Input(off, w)`
  以小端读取 1/2/4 字节。
- `Alloc(size)` 标记分配字节数；`Write(count)` 标记随后写入数量。同一路径上
  写入数超过最近一次分配数即为越界。
- `Analyze` 沿所有终结路径逐对检查（alloc, write）：先做可靠的区间证明；
  若相关输入空间在预算内则穷举判定；否则在预算内做边界值 + 确定性随机搜索。
- 结论：`Safe`（所有路径均被证明安全）、`Vulnerable`（附满足路径条件的具体
  输入 `Witness`）、`Unknown`（预算耗尽，无法定论）。`Budget` 为 0 时使用
  默认预算。
- `Execute`/`Violates` 提供具体执行与越界判定，可用于复核 `Witness`。

## 测试

`go test ./...` 用小位宽（8/16 位、1–2 个输入字节）图穷举全部输入作为基准真值，
核对分析器在安全、漏洞、未知（小预算）三类情形下结论一致，并验证返回的见证输入
确实触发越界。
