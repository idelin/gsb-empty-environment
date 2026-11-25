# subverify：上下文敏感的 jsr/ret 字节码验证

面向 Go 加载宿主的子程序字节码验证包，针对“共享清理子程序用局部槽保存
返回位置、恶意代码搬运别处返回位置劫持控制流”的旧字节码问题。

## 指令集

`jsr(目标)`、`ret(槽)`、`storeReturn(槽)`、`loadReturn(槽)`、`pushInt`、
`storeInt(槽)`、`loadInt(槽)`、`pop`、`goto(目标)`、非确定 `branch(目标)`、
`return`。局部槽数与操作数栈深度上限均为 32，子程序嵌套上限为 4 层。

## 安全模型

- 每次 `jsr` 铸造一枚**返回令牌**，其归属是仅属于本次调用的调用点链
  （如 `jsr@0 -> jsr@3`），令牌值（下一指令位置）从不作为可信跳转依据；
  `ret` 的返回目标始终从受保护的调用帧中读取。
- `ret` 只接受当前**最内层活动调用**铸造的令牌：外层令牌、已结束调用的
  陈旧令牌、失效令牌、整数、不可用槽一律拒绝。
- `ret` 返回时，该令牌在所有局部槽与操作数栈中的别名、派生拷贝全部转为
  失效态；之后任何 `loadReturn`/`ret` 触碰失效令牌即拒绝。
- 整数与返回令牌位于不同格元素，任何方向的混用都拒绝。
- 活动调用栈中再次进入同一子程序按递归拒绝；已结束的调用在普通控制流环中
  重新进入是合法的。
- 方法 `return` 要求活动调用栈与操作数栈同时为空。

## 上下文与合流

分析状态以 `(pc, 调用点链)` 为键，两个调用方处于不同上下文，返回位置不会
被并成同一个可跳目标。同一上下文内合流时：

- 局部槽类型相容取公共类型，不相容降级为不可用（后续使用即拒绝）；
- 操作数栈深度或结构不相容直接拒绝整个程序；
- 调用帧的 `jsr` 快照栈同样按合流规则处理，`ret` 时栈深度必须与快照一致。

工作列表在有限格上单调下降，普通控制流环可收敛；指令处理预算耗尽时返回
`ConclusionUnknown`，绝不当作可加载结论。

## 用法

```go
res := subverify.Verify(&subverify.Program{Code: code, NumSlots: 8, MaxStack: 8})
switch res.Conclusion {
case subverify.ConclusionAccept:
    // 可加载
case subverify.ConclusionReject:
    fmt.Println(res, res.CallChainText()) // 拒绝前的调用点链
case subverify.ConclusionUnknown:
    // 预算耗尽，保持拒绝加载
}
```

运行演示：`go run -buildvcs=false ./cmd/subverify-demo`。
