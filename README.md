# 带权重投票的协同决策无冲突选择器（CRDT / TypeScript）

多方分布式加权表决状态合并器：各节点基于持股权重提交 **赞成 / 反对 / 弃权**，
投票权重可随时间**因果动态调整**；网络分区愈合后，状态合并自动汇总出
**确定性**的最终决策。

## 模型

- 每个节点维护两张 LWW（Last-Writer-Wins）注册表集合：
  - `votes: voter -> {choice, clock, nodeId}` —— 选票（YES / NO / ABSTAIN）
  - `weights: voter -> {weight, clock, nodeId}` —— 权重配置
- 时间戳为 Lamport 时钟；并发冲突按 `(clock, nodeId)` 字典序决胜，
  因此合并满足**交换律、结合律、幂等律**（CRDT 收敛）。
- 权重变更与投票一样携带因果时间戳：愈合合并后自动收敛到因果上最新的
  权重配置，迟到的旧配置不会回滚新配置。
- 计票（`tally()`）使用合并收敛后的最新权重：
  - `totalWeight`：全网有效总权重（权重 > 0 的投票人权重之和）
  - 赞成 / 反对 / 弃权按投票人当前有效权重计入对应桶；未投票者计入 `pendingWeight`
- 确定性决策规则：
  - `yes > total/2` → **APPROVED**
  - `yes + pending <= total/2` → **REJECTED**（剩余票全投赞成也无法过半）
  - 否则 → **PENDING**

## 运行环境

- Node.js >= 22.6（利用原生 TypeScript 类型剥离运行 `.ts`，推荐 23+）
- 无第三方依赖；测试使用 Node 内置 `node:test`

```bash
nvm use            # 或自行切换到 Node >= 22.6
npm test           # 运行全部测试
npm run demo       # 运行四节点分区-愈合样例
```

## 验收场景（test/weightedVoting.test.ts）

四个节点 A/B/C/D（初始权重 10/20/30/40）在分区 `{A,B} | {C,D}` 下：

- 分区1：A 投赞成、B 投反对，A 并发地将 C 的权重改为 35
- 分区2：C 投赞成、D 投弃权，D 并发地将 C 的权重改为 15，C 将 D 提为 50
- 愈合合并后：C 的并发调权按 `(clock, nodeId)` 决胜收敛为 15，
  最终权重 A=10, B=20, C=15, D=50，全网有效总权重 = 95，
  赞成 25 / 反对 20 / 弃权 50，确定性输出 **REJECTED**；
  任意合并顺序结果完全一致。

## 文件结构

- `src/weightedVoting.ts` —— CRDT 核心实现（投票、调权、合并、计票、快照）
- `test/weightedVoting.test.ts` —— `node:test` 测试（含验收场景）
- `examples/demo.ts` —— 四节点分区-愈合运行样例
