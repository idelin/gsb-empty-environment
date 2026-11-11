# 编码净化次序推断

安全审查需要判断一次URL净化是否会被后续解码重新引入控制符。写供Go分析宿主调用的变换组合包，输入有限字节集合、百分号解码、反斜线转义解码及删除指定字符的有限管线；求存在未含危险分隔符的输入，经声明检查通过后又产生危险分隔符的最短反例。输入长度最多十二，预算耗尽返回未知，用独立逐步执行核对每个变换位置。

## 管线与语义

管线固定按四个阶段顺序执行：

1. 声明检查：输入直接含有 `Dangerous` 中任一危险分隔符即拒绝；
2. 百分号解码：`%XX`（两位十六进制）解码为单字节，允许产生非法 UTF-8 字节；不完整或非十六进制的 `%` 按字面量保留；阶段内只解码一次，不递归；
3. 反斜线转义解码：`\` 加后随字节命中 `BackslashMap` 时输出映射值并消费两字节，未命中或位于末尾时保留字面量 `\`；
4. 删除：移除 `Delete` 集合中的字节。

输入字节只能取自 `Alphabet`；`MaxLen` 缺省为 12，超过 12 视为配置错误。

## 双实现核对

- 评估器 A（`pipeline.go`）：按阶段批量变换；
- 评估器 B（`stepper.go`）：独立编写的显式状态机，每次只处理一个输入位置，产出带规则名与来源链的 `Step`。

每个候选都由两套实现各跑一遍，逐阶段、逐字节交叉核对，分歧即返回 `VerificationFailed`。预算只计评估器 B 的原子位置步骤，耗尽时 `Search` 返回 `Unknown`。

## 宿主调用

```go
res := netsec.Search(netsec.Config{
    Alphabet:     []byte("%013Bab"),
    Dangerous:    []byte{';'},
    BackslashMap: map[byte]byte{},
    // MaxLen 缺省 12；Budget 缺省 DefaultBudget。
})
switch res.Status {
case netsec.WitnessFound:
    // res.Input 为最短反例，res.Stages/res.Trace 可逐位置审计。
case netsec.Safe:
    // 整个有限输入空间穷举完毕，不存在反例。
case netsec.Unknown:
    // 预算耗尽，结论未知。
case netsec.InvalidConfig, netsec.VerificationFailed:
    // res.Reason 给出原因。
}

// 对单个输入做带轨迹的审计：
analysis, err := netsec.Analyze(cfg, []byte("%3B"))
```

`Search` 按长度递增 BFS，因此 `WitnessFound` 时的 `res.Input` 保证是最短反例（同长度下为字母表字典序最小者）。
