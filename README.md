# 秘密异常路径依赖

密码组件的返回值固定，秘密却影响是否抛出异常。实现供Go验证宿主调用的双执行包，输入至多十二条有界指令及公开、秘密变量，指令可产生明确异常类型；枚举相同公开输入和不同秘密输入，比较返回、异常及宿主调用序列。最多六个输入字节，搜索预算耗尽返回未知；反例附两个可重放状态序列。

## 实现概览

包 `netsec`（见 `doc.go`）把密码组件建模为有界抽象机，并做差分双执行校验。

- 程序模型 `Program`：至多 `MaxInstr`(12) 条指令，变量为有位宽的无符号寄存器，
  标记 `Public` 或 `Secret`；秘密变量总位宽不超过 `MaxInputBytes`(6) 字节（域 ≤ 2^48）。
- 指令集 `Op`：常量、装载、加减乘除、比较、断言、前向分支、宿主调用、返回。
  算术与断言可抛出明确类型的异常 `ExcCode`（溢出/下溢/除零/断言/宿主故障/无返回）。
- 宿主接口 `Host`：`OpHost` 调用 Go 验证宿主，`Call` 记录宿主编号、入参、结果与故障，
  形成可重放的宿主调用序列。
- 单次执行 `Run` 返回完整 `Trace`（初始状态、逐步 `State`/`Instr`/`Call`、返回值与异常）；
  `Trace.Observe()` 得到对外可观测的 `Observation`。
- 差分校验 `Verify`：固定公开输入，按“角点组合 + 全域里程表顺序”枚举秘密输入，
  比较两次执行的返回值、异常类型（含故障 PC）与宿主调用序列。
  - 出现分歧 → `LeakFound`，`Counterexample` 附带两条完整、可重放的状态序列
    （`TraceA`/`TraceB`），可用 `Replay` 脱离真实宿主独立复现。
  - 全域枚举且无分歧 → `SecretIndependent`。
  - 预算（执行次数）先耗尽 → `Unknown`。

## 快速使用

```go
res, err := netsec.Verify(program, publicInput, host, budget /*0=默认*/)
switch res.Verdict {
case netsec.LeakFound:
    _ = res.Counterexample.TraceA // 可重放状态序列一
    _ = res.Counterexample.TraceB // 可重放状态序列二
case netsec.SecretIndependent:
    // 全域无差异
case netsec.Unknown:
    // 预算耗尽
}
```

测试与可运行示例见 `machine_test.go`、`verifier_test.go`、`example_test.go`。
