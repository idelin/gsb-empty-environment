# 有界恶意电路的差分故障

Go硬件仿真宿主需要可导入的故障检测包，电路是至多128节点的布尔DAG，节点含输入、AND、XOR、NOT和输出，输入至多12位。攻击者可选择一个内部节点永久卡0或卡1；给定公开测试向量集合，找出所有测试均未发现但存在输入使安全输出位由拒绝0变为放行1的故障。用位并行仿真或符号法搜索并返回节点、故障值、逃逸输入及正常/故障逐节点状态，必须独立回放；无遗漏枚举全部故障才能报告不存在，预算耗尽为未知。另给最小新增测试向量集合覆盖所有可检测危险故障，输入空间限定4096且危险故障最多20，精确求最少数量并按向量字典序决胜。

## 用法

```go
c, err := netsec.NewCircuit([]netsec.Node{
    {Kind: netsec.KindInput},               // 输入位 0
    {Kind: netsec.KindInput},               // 输入位 1
    {Kind: netsec.KindAnd, Args: []int{0, 1}},
    {Kind: netsec.KindNot, Args: []int{2}},
    {Kind: netsec.KindOutput, Args: []int{3}},
})

// 差分故障分析：枚举全部内部节点的卡 0/卡 1 故障。
rep, err := netsec.Analyze(c, []uint32{0, 1, 2}, netsec.Options{})
// rep.Status: found / none / unknown（预算耗尽）
// rep.Findings[i]: 故障、逃逸输入、正常/故障逐节点状态（独立回放）

// 最小新增测试向量集：与公开测试合并后覆盖全部危险故障。
cov, err := netsec.MinTestSet(c, []uint32{0, 1, 2}, netsec.Options{})
// cov.Vectors: 精确最少且按字典序决胜的新增向量
```

## 设计要点

- **位并行仿真**：每个节点用 4096 位真值表（`[]uint64`）表示整个输入空间，按位与/异或/非一次求值；每个候选故障一次仿真即判定危险性与可检测性。
- **独立回放取证**：逃逸输入下的逐节点状态由独立标量仿真（`Eval`/`EvalWithFault`）重新生成，并与位并行结果逐位交叉校验，不一致即报内部错误。
- **三态结论**：只有无遗漏枚举全部故障后才报告 `none`/`found`；`Options.Budget` 限制故障仿真次数，耗尽时报告 `unknown` 并附已分析进度。
- **精确最小覆盖**：危险故障不超过 20 个时以故障位掩码做状态 DP 求精确最少向量数，再贪心重构字典序最小（升序数值序）的向量序列；危险故障超上限时报告 `unknown`。
