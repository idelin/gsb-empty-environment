# merklesync — 键空间摘要树差异同步

纯 Go 标准库实现。节点按键空间构建摘要树（Merkle 树），逐对反熵同步时
只传输差异叶桶的记录；删除以墓碑传播；版本冲突由显式全序规则裁决；
摘要碰撞有复核路径兜底。通信、故障（丢包/重复/延迟）与时间全部在
单进程事件模拟器中受控，行为可由种子完全复现。

## 设计

- **摘要树**（`tree.go`）：键经 FNV-1a 散列后取前 4 个十六进制位作路径，
  构成 16 叉、深 4 的树（65536 个叶桶）。内部节点摘要 = 非空子节点
  （序号， 摘要） 的散列；叶桶摘要 = 桶内按键排序的全部记录（含版本/来源/
  墓碑位）的散列。内容相同的子树摘要必然相同。
- **协议**（`node.go`）：发起方发根摘要；接收方逐条比对——
  - 摘要一致且非复核轮：整棵子树剪枝，**相同子树一个字节都不传**；
  - 内部节点不一致：下发子节点摘要，沿路径递归细化；
  - 叶桶不一致：**只传该差异桶的记录**（`WantReply` 请求对方回传，
    空桶也回应并附桶路径，避免单向同步在空桶处停摆）；
  - 收到记录按显式规则合并；有变化或对方要求回执时回传涉及桶。
    版本单调 + 合并幂等 ⇒ 往返必然终止。
- **墓碑**（`record.go`）：删除是写入更高版本的墓碑记录，而非移除条目。
  墓碑随同步传播；迟到的旧值版本更低，按规则被丢弃，**删除不会被复活**。
- **显式冲突规则**（`Record.supersedes`）：全序、确定性、与到达顺序无关——
  版本高者胜 → 来源 ID 大者胜 → 墓碑胜活值 → 值字典序大者胜。
  这使 `Merge` 成为交换、结合、幂等的半格 join。
- **复核路径**：每 `VerifyEvery` 轮为复核轮，摘要不一致才下钻的剪枝被关闭，
  全树下钻到叶并直接交换记录比对。即使攻击者/故障让摘要全部碰撞
  （`ForceCollision` 强制所有摘要为同一常量），复核轮仍能发现分歧并收敛。
- **事件模拟器**（`sim.go`）：虚拟时钟 + 事件最小堆驱动；网络按配置注入
  随机延迟、丢包、重复投递；周期性同步定时器；传输指标（消息数、记录数、
  被传输的键集合）用于验证剪枝与不动点。

## 安全性证据（safety）

| 不变式 | 论证 | 测试 |
| --- | --- | --- |
| 冲突裁决确定性 | 显式全序 ⇒ 任意到达顺序同一胜者 | `TestExplicitConflictRule` |
| 合并殊途同归 | join 半格：交换/结合/幂等 | `TestMergeOrderIndependence`（200 条随机记录乱序+重复合并） |
| 删除不复活 | 墓碑是更高版本记录；旧值版本低被规则丢弃 | `TestTombstonePropagatesAndNoResurrection`（乱序迟到旧值） |
| 相同子树不传全量 | 摘要一致即剪枝 | `TestIdenticalSubtreeNotTransmitted`（300 键仅传差异桶 10 键，且全部落在差异桶内）、`TestDigestPruning` |
| 摘要碰撞不造成错误收敛 | 复核轮下钻到叶直接比对记录 | `TestForcedCollisionReviewPath`（先证明无复核时被碰撞欺骗停滞，再证明复核路径收敛） |
| 故障下终态正确 | 收敛后各节点存储 == 全部操作的参考 join | `TestLivenessUnderFaults` 中的参考存储比对 |

## 活性证据（liveness）

- **单轮终止**：摘要下钻深度 ≤ 4 层；记录往返仅在“有变化或被请求回执”
  时继续，而存储按全序单调递增且有上界 ⇒ 消息链有限。
- **故障下收敛**：丢包只延迟不破坏（下一轮摘要交换重新发现分歧）；
  重复投递被幂等合并吸收。`TestLivenessUnderFaults`：25% 丢包 + 15% 重复 +
  随机延迟下，约 260 轮内三节点收敛到参考状态。
- **不动点**：收敛后摘要一致 ⇒ 剪枝 ⇒ 零记录传输、状态不变。
  `TestRepeatedSyncReachesFixpoint`（逐轮传输量 121 → 0 并保持）与
  `TestLivenessUnderFaults` 末尾的不动点轮（传输 0 条、状态快照不变）。

## 运行

```sh
go run .        # 演示：3 节点、丢包/重复/延迟、删除传播、不动点
go test ./...   # 全部安全性/活性测试
```

## 运行样例

```
t=0 初始: A=40 条, B=40 条, C=1 条
t=3030 收敛后: A=61 条, B=61 条, C=61 条
收敛检查: A==B=true, B==C=true
  节点 A 上 user-005: user-005="" v41@A (tombstone)
  节点 B 上 user-005: user-005="" v41@A (tombstone)
  节点 C 上 user-005: user-005="" v41@A (tombstone)
不动点验证: 额外一轮同步传输记录数 = 0（期望 0）
传输指标: 消息=528 (摘要=513/1096 条目, 记录=15/530 条), 丢包=87, 重复=42
```

## 测试结果（go test -race -count=1 ./...）

```
=== RUN   TestExplicitConflictRule            --- PASS
=== RUN   TestMergeOrderIndependence          --- PASS
=== RUN   TestTombstonePropagatesAndNoResurrection --- PASS
=== RUN   TestIdenticalSubtreeNotTransmitted  --- PASS  (300 键中仅传输 10 个)
=== RUN   TestDigestPruning                   --- PASS
=== RUN   TestForcedCollisionReviewPath       --- PASS
=== RUN   TestLivenessUnderFaults             --- PASS  (丢包=337 重复=174 后收敛)
=== RUN   TestRepeatedSyncReachesFixpoint     --- PASS  (逐轮 121→0→0→…)
PASS
ok  	merklesync
```
