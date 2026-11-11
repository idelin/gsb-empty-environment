# 网络扫描序列弱观测

采样探针只看到部分连接尝试，检测端不能把未采到的包认定为没有发生。实现供Go检测服务调用的序列判定包，输入每个时间段已观察到的目标集合、最多遗漏数量和扫描模式自动机；判断模式必然发生、可能发生或不可能发生，给出相应补全见证。目标域有限，最多十二段。用穷举所有合法遗漏补全验证三个结论，证据不足时不输出确定告警。

## 模型

- 有限目标域 `domain`；每个时间段给出已观测目标集合 `observed[i]`，时间段数量不超过 `netsec.MaxSegments`（12）。
- 一段的真实尝试集合 = 观测集合 ∪ 补入的遗漏目标；遗漏目标只能取自目标域中未观测部分。
- 全序列补入的遗漏总数不得超过预算 `maxMisses`。
- 扫描模式用有限目标域上的 NFA（`netsec.NFA`）描述：每一段可由该段集合中的任意目标驱动迁移，结束时落在接受状态即视为模式发生。

判定通过穷举**全部**合法遗漏补全得到：

| 结论 | 条件 | 告警 |
| --- | --- | --- |
| `VerdictCertain` 必然发生 | 所有合法补全都被接受 | 是（`Alarm=true`） |
| `VerdictPossible` 可能发生 | 同时存在接受与拒绝补全 | 否（证据不足） |
| `VerdictImpossible` 不可能发生 | 所有合法补全都被拒绝 | 否 |

每份 `Witness` 给出逐段完整补全集合、补入的遗漏目标、遗漏总数，以及自动机实际选择的逐段目标路径（可直接复算验证）。可能发生时接受见证取遗漏最少者、拒绝见证取遗漏最多者。

## 用法

```go
nfa := &netsec.NFA{
    Start:  []int{0},
    Accept: []int{3},
    Trans: map[netsec.TransKey][]int{
        {State: 0, Target: "a"}: {1},
        {State: 1, Target: "b"}: {2},
        {State: 2, Target: "c"}: {3},
    },
}

decision, err := netsec.Decide(
    []string{"a", "b", "c", "d"},        // 目标域
    [][]string{{"a"}, {"b"}, {"d"}},     // 每段观测
    1,                                    // 最多遗漏数量
    nfa,
    nil,
)
// decision.Verdict == VerdictPossible，decision.Alarm == false
// 第三段补入 "c" 即得到接受见证，但同样存在 9 份拒绝补全，因此不告警。
```

合法补全数量受 `DecideOptions.MaxCompletions` 约束（默认 `DefaultMaxCompletions`），超过上限返回 `ErrCompletionLimitExceeded`，绝不静默截断或在证据不足时给出确定告警。

## 验证

`go test ./...` 覆盖三种结论、NFA 非确定性迁移、枚举计数与预算边界、12 段上限、见证复算与存在性语义单调性，以及全部输入校验错误。
