# 溢出检查谓词保持

`example.org/netsec/q0383` 是一个可导入的 Go 重写核验包。它比较旧版与加固后
的 SSA 小图：最多 12 个输入字节，包含有符号 16/32 位加、减、乘、截断、扩展、
比较和分支。旧版可在溢出时显式跳到 `Error`；新版对相同输入必须报告同一
`Source`，否则正常返回值必须相等。

```go
func checkedAdd(width uint8, source netsec.NodeID) (*netsec.Graph, netsec.NodeID) {
    b := netsec.NewBuilder(2)
    x, y := b.Input(0), b.Input(1)
    sx := b.Extend(x, width, true)
    sy := b.Extend(y, width, true)
    sum := b.AddChecked(sx, sy, width, 16, true)
    ov := b.Overflow(sum)
    ret := b.Return(sum)
    err := b.Error(source)
    return b.MustBuild(b.Branch(ov, err, ret)), sum
}

old, source := checkedAdd(16, 0)
old, source = checkedAdd(16, source) // error source is the original add node
new, _ := checkedAdd(32, source)     // widened graph keeps the same source

result := netsec.Verify(old, new)
switch result.Status {
case netsec.StatusVerified:
    // 所有路径条件与结果比较均不可满足
case netsec.StatusCounterexample:
    // result.Counterexample.Input 已由独立解释器复现
case netsec.StatusUnknown:
    // 预算耗尽；不会把未知当作等价
}
```

核心接口：

- `Builder`：构造 SSA 图；`AddChecked`/`SubChecked`/`MulChecked` 给算术节点
  附加原始位宽检查，`Overflow` 产生显式谓词。
- `Interpret`：独立、具象、基于 `math/big` 的解释器。
- `Verify`：枚举路径，将路径条件、正常返回不等和错误源不同编码为位向量
  CNF，再由有界 DPLL 求解。只有全部 UNSAT 才返回 `StatusVerified`。
- `Budget`/`Options`：限制路径、路径对、SAT 搜索节点和冲突；超限返回
  `StatusUnknown`。

错误等价按 `Error.Source` 判断，节点局部 ID 不必跨版本相同。`Interpret`
总是会重放符号搜索给出的反例；若重放不一致，`Verify` 保守地返回未知。

测试中包含一个单输入字节的小图：对 256 个输入逐一执行两版图，并与符号搜索
给出的反例/等价结论交叉校验。
