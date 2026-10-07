# facility:设施覆盖求解库

Go 求解库,覆盖两个设施选址/覆盖问题。设施候选构成树(森林):
开启子设施必须同时开启其全部祖先;每个需求点被任意已开启设施覆盖
即计一次收益,重叠覆盖不重复计分。

## 问题一:确定性覆盖(`facility/cover.go`)

- 规模:至多 20 个设施,整数预算。
- 目标:先最大化覆盖收益,再最小化开启费用(字典序)。
- 方法:枚举全部 2^n 个子集,按最低位增量递推覆盖并集(位集)、
  祖先需求与费用,过滤非闭包与超预算解。
- 输出:开启闭包 `Solution.Opened`、收益、费用、逐需求点覆盖标记。
- 核对:`Verify` 逐需求点独立重算(确认重叠覆盖只计一次);
  `CrossCheck` 用朴素子集穷举复核最优性。

```go
sol, _ := in.Solve()
_ = in.Verify(sol)
_ = in.CrossCheck(sol)
```

## 问题二:两阶段鲁棒覆盖(`facility/robust.go`)

- 覆盖关系随情景变化;一阶段开启决策先固定,情景揭晓后仅能在
  已开祖先下启用至多 `EmergLimit` 个应急设施;同一观测组内的
  情景必须采取相同的应急动作。
- 规模:至多 5 个情景、12 个设施。
- 目标:先最大化最坏情景覆盖收益,再最小化期望总费用
  (一阶段费用 + 按情景概率加权的应急费用)。
- 约束:每个情景下 一阶段费用 + 应急费用 <= Budget。
- 方法:枚举一阶段闭包 F;对每个 F 枚举合法应急子集并按观测组
  汇总组内最坏收益。固定 F 时各组独立,F 的最坏收益为
  `min_g max_E m_g(E)`;在达到全局最优最坏收益的 F 中取期望费用
  最小者(组内取满足阈值的最小应急费用)。
- 输出:`Policy` 策略树(一阶段开启集 + 各观测组应急集)、
  逐情景收益/费用;`RenderPolicyTree` 树形打印,
  `CheckScenarios` 逐情景预算检查。
- 核对:`VerifyPolicy` 独立校验闭包/预算/同组一致/目标值;
  `CrossCheckRobust` 用暴力枚举复核最优性。

```go
pol, _ := in.SolveRobust()
fmt.Print(in.RenderPolicyTree(pol))
for _, c := range in.CheckScenarios(pol) { /* 逐情景预算 */ }
```

## 运行

```sh
go test ./facility/     # 单元测试(含随机实例的独立穷举复核)
go run ./cmd/facilitydemo
```
