# 有界栈写入可达性

`netsec` 是一个纯 Go、非执行的固件栈写入模型。它不接触真实进程，只做
64 字节栈上的确定性重放和有限枚举，用来区分普通栈越界写与能够改写
保存返回地址的控制流劫持。

## 快速使用

```go
cfg := netsec.Problem{
    InputSlot:  netsec.Span{Start: 0, Len: 3},
    ReturnSlot: netsec.Span{Start: 60, Len: 4},
    Allowed: [][]byte{
        {0x56},
        {0x34},
        {0x12},
    },
    AllowedTargets: []uint32{0x00123456},
    Writes: []netsec.Write{{
        Offset: netsec.Affine{A: 1, B: 60 - 0x56},
        Length: netsec.Affine{B: 3},
        Byte:   0,
    }},
}

res, err := netsec.Solve(cfg)          // 穷举
res, err = netsec.Solve(cfg, 1000)    // 最多重放 1000 个输入
one, err := netsec.Replay(cfg, []byte{0x56, 0x34, 0x12})
```

`Solve` 按输入字节的字典序枚举，返回最小的成功输入。成功结果包含
`Input`、逐次写区间 `Writes`、最终栈 `FinalStack` 和最终返回地址
`FinalRet`。

## 模型约定

- 栈固定为 64 字节；写目标区间必须完全位于 `[0,64)`，否则立即陷阱。
- 输入槽的前 `len(Allowed)` 个字节可控，且最多 3 个；槽更长的其余字节
  来自初始栈。每次写复制的源固定为输入串起点开始的长度前缀；请求长度
  超过整个输入槽长度即 `TrapSourceShort`。
- 每次写的目标偏移和长度分别由 `Offset`、`Length` 仿射式给出，
  用数学整数计算，不做 `uint8` 回绕。`OffsetInput` / `LengthInput`
  选择驱动字节；`Byte` 为两者共用同一字节的简写。
- 源数据在写入前先做临时快照，因此源和目标的区间重叠时是 memmove
  语义，而不是逐字节前向覆盖。
- 返回地址按小端解释。只有最终地址属于 `AllowedTargets`、不同于
  `InitialRet`、且 canary 完好时才发生控制流转移。
- 4 字节 canary 可在写入中被破坏，但返回前必须逐字节恢复为初值。
  只能破坏 canary 的结果是 `StatusCanaryOnly`，绝不标记为 exploit。
- 任一写陷阱后不再执行后续写，也不再到达返回点；即使此前已经改写了
  返回地址，也不存在控制流转移。
- 返回地址区间固定为 4 字节，且必须与启用的 canary 区间不重叠。

## 状态

| 状态 | 含义 |
| --- | --- |
| `StatusExploit` | 到达返回点、canary 完好、返回地址为允许目标且异于初值 |
| `StatusCanaryOnly` | canary 被破坏，未发生控制流转移，不是利用成功 |
| `StatusNoExploit` | 已穷举，未找到满足条件的输入 |
| `StatusBudgetUnknown` | 重放预算耗尽，既不能判成功也不能判不存在 |
| `StatusTrap` | 单次重放在返回前陷阱 |

`Solve` 的 `budget` 省略或为负表示穷举；`0` 表示一个输入都不重放，
若候选空间非空则返回 `StatusBudgetUnknown`。预算不足时不会把
已观察到的 canary 破坏误报成“仅能破坏 canary”的最终结论。

`Problem`、`Write`、`Canary` 带有若干字段别名，便于平台适配层直接
填充不同拼写；规范化与合法性检查由 `Validate` 或求解函数执行。

## 测试

```sh
go test ./...
```
