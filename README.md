# 秘密比较与恒定轨迹检查

这个 Go 包提供两个独立部分：

- 固定的 32 字节秘密/候选比较器。
- 小型解释器的可观测轨迹检查器，用于发现给定有限输入集合中的轨迹差异。

检查器不测量墙钟时间。它只观察定址读的绝对地址，以及循环/按值分支的结果。

## 快速使用

```go
equal, trace, err := netsec.CompareTrace(secret, candidate)
```

`CompareTrace` 要求 `len(secret) == 32` 且 `len(candidate) == 32`。长度不符时在
执行任何 VM 指令、读取任何秘密区域字节之前返回 `ErrInvalidLength`。认证路径可
直接使用：

```go
ok := netsec.SecureEqual(secret, candidate)
```

内置比较程序每次固定读取 32 个秘密字节和 32 个候选字节，执行 192 条 VM 指令，
使用 XOR 和 OR 累积差异，不包含依赖值的分支。输出寄存器为零表示相等。

`VerifyCompareProgram` 会穷举：

- 一个全部相同的输入；
- 32 个字节位置 × 每个位置 255 个不同的单字节值。

共 8161 个输入，逐例检查结果正确且轨迹完全相同。

## 指令集

程序最多 128 条指令，寄存器为 16 个 64 位寄存器，初始均为零。

| 指令 | 语义 |
| --- | --- |
| `Read` | 从绝对地址区域 `Base + Offset + regs[Src]` 读取一字节 |
| `Xor` | `dst = a ^ b` |
| `Or` | `dst = a \| b` |
| `Inc` | `dst = src + 1` |
| `Loop` | 公开计数的循环体末尾；按固定 `Count` 回跳 |
| `Branch` | 按寄存器值是否为 0、是否等于立即数跳转 |

`Loop(count, bodyStart)` 放在循环体末尾。循环体第一遍正常执行；到达 `Loop` 后，
若已完成次数小于公开的 `count`，回跳到 `bodyStart`。循环次数不读取秘密/候选。
例如，内置比较程序的循环体从地址 0 开始，末尾是 `Loop(32, 0)`。

秘密区域位于 `SecretBase`，候选区域位于 `CandidateBase`。越界读返回
`ErrAddress`，不会生成被截断的“通过”结论。

## 有限输入检查

```go
pairs := []netsec.Pair{
    {Secret: s1, Candidate: c1},
    {Secret: s2, Candidate: c2},
    {Secret: s3, Candidate: c3},
}
report := netsec.CheckFinite(program, pairs, 4096)
```

`CheckFinite` 用同一个显式步数预算执行每对输入，对所有输入两两比较轨迹，并返回
最早出现的差异。`CheckFiniteSets` 会先验证两个集合中所有 32 字节长度，再构造
笛卡尔积并执行同样检查。

报告状态：

- `consistent_finite_inputs`：给定有限集合内，所有已成功完成的输入对轨迹相同。
- `divergent`：找到差异；`Witness` 包含最早差异步、两侧事件以及两份完整的
  32 字节秘密和候选。
- `unknown`：预算缺失、预算耗尽、或有意义的输入对不足。`Passed()` 为 false。
- `invalid`：程序、长度或地址无效。`Passed()` 为 false。

零预算没有默认值，也不会被当成无限制预算。程序未在预算内结束时，部分轨迹不能
用来证明轨迹相同，因此结果是 `unknown`。

调用方必须提供足够的步数预算；本包不会猜测循环上界，也不会在预算未知时通过。

## 保证边界

`consistent_finite_inputs` 只表示调用方给出的有限输入集合没有触发可观察差异。
它不能证明任意输入、任意编译产物或任意机器码是恒定时间的。
`CheckReport.IsProof()` 永远为 false，且报告带有明确的 `Scope` 文本。

有限集合检查适合：

- 验证内置比较程序的单字节差异覆盖；
- 审计小型解释程序的读取地址和分支轨迹；
- 在 CI 中保留反例和完整输入见证。

它不适合宣称完成了对所有机器码的形式化验证。

## 验证

```sh
go test ./...
go vet ./...
```
