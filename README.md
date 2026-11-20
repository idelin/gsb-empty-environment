# updatestrategy：基于公开观测的 A/B 更新策略规划包

## 背景约束

现场设备只能验证**活跃槽**的度量（摘要）和回滚计数器，在重启前无法读回刚写入的备用槽。
因此规划不能建立在“知道真实槽内容”之上，只能建立在宿主当前能拿到的**公开观测历史**之上。
本包把这个问题建模为信念状态（belief state）上的 AND-OR 规划：

- `World`：一个具体的持久化设备状态，仅供规划器和内存模拟器使用，宿主永远观察不到完整 `World`。
- `Observation`：宿主动作后能拿到的公开结果（写应答/无应答、挂起指针、计数器、断电标志、重启读回）。
- `Belief`：与同一观测历史相容的全部 `World` 集合。决策只绑定 `Belief`，绝不绑定单个 `World`。

## 核心语义

- 组件、镜像摘要、签名根（以调用方给定的 `Digest` 身份表示）和不可下降的计数器均由输入提供，规划器不发明任何镜像或度量。
- 每种写入声明三种可能的持久结果：完整落盘、断电前无任何持久字节、断电造成半写。半写槽在重启认证前只能是 `UnknownDigest` 未知候选，**不得**当作已成功写入。
- 每个动作在 `World.PowerLost == false` 时额外给出全部合法中断结果；一旦断电，后续动作只产生干净结果，因此全程**至多一次断电**。
- `RebootAction` 只认证可引导的挂起槽；空槽/未知槽强制回退到上一个已认证活跃槽，保证每一步都能离线引导。
- 计数器原子提交，只增不减；`BurnCounterAction` 携带目标下限，在已达下限的世界继续烧录被声明为不安全（永久烧毁回退余量）。

## 规划与拒绝

`Plan(Problem)` 在信念状态上做记忆化 AND-OR 搜索：

- 一个动作必须对信念中的**每个**世界都安全（特别地，写操作绝不允许覆盖唯一已认证活跃槽）。
- 动作结果按公开观测分组，相同观测的世界被合并为同一个后继信念——这强制恢复组合对该观测下所有可能的持久结果同时有效。
- 自环（无进展动作）和非目标环被剪枝，保证策略终止；策略代价取 `1 + max(后继代价)`，返回最坏操作数最少的策略。
- 当不存在任何对整个信念都安全且可达目标的动作时，返回包装 `ErrPlanningRefused` 的 `*Refusal`，类别为
  `RefusalDivergentBelief`（不可区分但动作需求冲突）或 `RefusalGoalUnreachable`，并列出每个隐藏世界失败的具体动作与原因。

## 独立可核验的安全不变量

`VerifyPolicy(policy, actions)` 不依赖规划器内部，仅凭转移模型重算策略的每条边并检查：

1. `InvGoalOnly`：只有信念中所有世界都达目标才能停止。
2. `InvActionDeclared`：动作必须来自宿主声明集合。
3. `InvActionSafe`：动作在信念的每个世界中都安全。
4. `InvBranchComplete`：每个可能观测都必须有后继分支。
5. `InvNoHiddenKnowledge`：后继信念必须恰好等于共享该观测的结果世界集合（不允许使用隐藏槽知识缩小集合）。
6. `InvOfflineBootable`：每条边之后活跃槽都持有已认证镜像。
7. `InvCounterMonotonic`：任何边都不得降低计数器。
8. `InvTerminating`：策略无环且存储的最坏代价与重算值一致。

## 内存模拟

- `RunOnce` / `RunRandom`：按公开观测驱动策略，模拟器不读取隐藏内容来选动作。
- `ExhaustiveRun`：枚举一棵有限树（断电分支消耗后即消失），验证每条合法中断路径都达目标、全程可引导、断电不超过一次。

## 小型完整状态参考（`reference_test.go`）

- S1 `TestS1_WriteUnknownBeforeSwitchAndWorstCost`：切换前写入未知，信念覆盖“已落盘/空/半写”三世界，断言最优最坏代价与穷举恢复。
- S2 `TestS2_EarlyCounterBurnRemovesLastRecoveryPath`：提前烧录计数器使两个同观测隐藏世界一个已达下限、一个未达，固定增量烧录对二者不可能同时安全，规划被拒绝。
- S3 `TestS3_SuccessAndPowerLossShareOneObservation`：无应答写入通道下，成功与断电产生相同观测且无读回动作，目标不可认证，规划被拒绝。
- S4 `TestS4_RebootReadbackEnablesSafeBranching`：同一信念下只有重启读回能按 `Old`/`New` 摘要安全分叉，之后各分支独立恢复。

所有逻辑只在内存中模拟，无任何真实设备 I/O。

## 验证

```
go vet ./...
go test -race ./...
```
