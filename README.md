# 被错判分支隐藏的探测表泄漏

`example.org/netsec/q0001` 提供一个供审查服务调用的 Go 分析包。它实现自定义微型指令集、单次可重放运行、按执行次数限流的见证搜索，以及见证回放校验。

## 机器模型

- 8 个 8 位寄存器 `R0..R7`，256 字节内存 `M0..M255`，最多 64 条指令。
- 初始状态固定为零；输入槽再把公开字节和秘密字节写入指定寄存器或内存地址。
- `load` 以源寄存器的 8 位值为地址读取一个字节，只修改目标寄存器。
- `probe` 以源寄存器的 8 位值为地址，记录整数 `address/16`。
- `jeq` 比较两个寄存器。实际执行时按实际条件走；一次运行最多选择一个已实际执行的条件跳转，走相反后继并推测执行最多 8 条指令。
- 推测中到达 `barrier`、`halt`、程序末尾或 8 步上限时立即回滚寄存器和内存，保留推测 probe 记录，再从原分支的正确后继继续。
- 推测中的其他条件跳转一律按实际条件执行。
- 每次运行最多执行 64 条指令（架构执行与推测执行合计）；超限的运行为未知，不参与见证。

## 输入布局

`Program.Inputs` 按声明顺序描述输入槽：

```go
Inputs: []InputSlot{
    PublicRegister(0),
    PublicMemory(0x40),
    SecretRegister(1),
}
```

公开槽最多 2 个，秘密槽必须恰好 1 个；寄存器或内存位置不得重叠。`Assignment.Public` 按公开槽声明顺序提供字节值，`Assignment.Secret` 提供秘密字节。

## 搜索与见证

`Analyze(program, budget)` 枚举公开输入的所有取值、秘密字节的 0..255 取值，以及架构路径上每个非退化条件跳转的误预测位置。每次调用 `Run` 消耗一次执行预算。

见证定义为：

1. 两个运行使用相同的公开输入；
2. 秘密值不同；
3. 两个运行都恰好选择一次相反的跳转后继（避免把“有无推测”或仅公开输入造成的差异误报为泄漏）；
4. `ArchProbes` 的 probe line 序列相同；
5. `SpecProbes` 的 probe line 序列不同。

`Result.Status` 只有三种：

- `witness_found`：返回 `Witness`，其中包含输入、误预测位置和两条完整逐步记录。
- `no_witness_bounded`：预算内全部输入和误预测候选都已执行，且没有任何运行超限。
- `unknown`：预算不足以穷尽候选，或至少有一条候选运行超过 64 步。

因此 `no_witness_bounded` 不会在预算截断时伪造“无泄漏”结论。

## 示例

```go
program := netsec.Program{
    Code: []netsec.Instruction{
        netsec.Const(2, 0x90),
        netsec.JumpEq(0, 0, 5),
        netsec.Add(2, 1),
        netsec.Load(3, 2),
        netsec.Probe(2),
        netsec.Probe(0),
        netsec.Halt(),
    },
    Inputs: []netsec.InputSlot{
        netsec.PublicRegister(0),
        netsec.SecretRegister(1),
    },
}

result, err := netsec.Analyze(program, 4096)
if err != nil {
    // invalid program or invalid budget
}
if result.Status == netsec.StatusWitnessFound {
    if err := netsec.VerifyWitness(program, *result.Witness); err != nil {
        // the witness does not replay
    }
}
```

`Witness.RunA` 和 `Witness.RunB` 都包含 `Trace`（逐步寄存器、下一条 PC、load 地址/值、probe line、回滚标记）、架构/推测 probe 记录及最终架构状态，可直接用 `encoding/json` 序列化后重放。

## 测试覆盖

`go test ./...` 覆盖：

- 分支误预测下越界表读取产生秘密相关的推测 probe；
- 在推测路径入口插入 `barrier` 后，同一程序无见证；
- 秘密直接进入架构 probe 的直接泄密不算本题见证；
- 推测 probe 只依赖公开输入时不算见证；
- 推测加载秘密后回滚，秘密不留在架构寄存器；
- 推测 8 步上限、64 步运行上限，以及执行预算截断返回 `unknown`。
