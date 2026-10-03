# 多节点租约竞争与脑裂预防仲裁器（Quorum Lease Arbiter）

TypeScript 实现的多数派租约仲裁协议：节点必须获得集群**超过半数（Quorum）**节点的
租约授权才可成为有效 Leader；网络发生对称或非对称分区时，少数派分区节点自动丧失
租约并拒绝写请求，杜绝脑裂（Split-Brain）。

## 运行环境

- Node.js **>= 23.6**（原生 TypeScript 类型擦除，零依赖、零构建）
- 测试使用 Node.js 内置测试运行器 `node:test`

```bash
npm test     # 等价于 node --test
npm run demo # 等价于 node demo/sample.ts
```

## 协议设计

核心不变式（`src/lease-arbiter.ts`）：

1. **多数派授权**：候选者必须收集到 `⌊N/2⌋+1` 个节点的租约授权才能成为 Leader。
2. **排他授权**：每个仲裁节点在已授予的租约过期前，不会把授权给任何其他候选者
   （包括更高任期的候选者）。
3. **租约即写权限**：Leader 只有在持有未过期租约时才受理写请求。
4. **过期自动剥离**：租约过期由本地时钟驱动，无需任何网络消息，主权限自动回收。

安全性论证：任意两个 Quorum 必有交集；交集中的节点在旧租约过期前不会重复授权，
因此任意时刻至多存在一个持有有效租约的 Leader —— 脑裂在协议层面被排除。
代价是分区期间存在不超过一个租约周期的"无主窗口"（可用性换一致性）。

## 集群模拟（`src/cluster.ts`）

- **确定性逻辑时钟**：`advance(ms)` 显式推进，测试可复现，不依赖真实定时器。
- **对称分区**：`partitionInto([[n1,n2],[n3,n4,n5]])` 切断跨组链路。
- **非对称分区**：`setLink(from, to, false)` 切断单向链路；一次 RPC 要求
  请求（from→to）与响应（to→from）两个方向均可达，任一方向断开即失败。

## 验收边界（`test/quorum-lease.test.ts`）

5 节点集群 `{n1..n5}`，Quorum = 3，租约 1000ms：

1. 全连通时 n1 获 5 票当选 Leader 并服务写请求。
2. 划分为 `{n1,n2}` 与 `{n3,n4,n5}` 两个隔离分区。
3. 少数派的 n1 续租仅得 2 票（< Quorum），续租失败；旧租约过期前多数派也无法
   重复授权选出新 Leader。
4. 租约过期后 n1 **自动剥离主权限**，写请求被拒绝（`NotLeaderError`），
   过期写不会进入已提交日志。
5. 多数派分区 n3 凑齐 3 票当选并持续续租、正常服务写请求。
6. 全程 `activeLeaders()` 至多一个 —— 无脑裂。

另有非对称分区（单通/反向单通）、排他授权、纯本地时钟过期等用例，共 6 个测试。

## 运行样例（`npm run demo` 实际输出）

```
=== 多数派租约仲裁演示（5 节点，Quorum=3，租约 1000ms）===

--- 阶段 1：全连通，n1 竞选 ---
         n1.campaign() -> true
[t=   0ms] n1 获得 5 票授权，当选 Leader
         leaders=[n1]
         WRITE n1 "order#1" -> OK

--- 阶段 2：对称分区 {n1,n2} | {n3,n4,n5} ---
         n1.renewLease() -> false (少数派仅 2 票 < Quorum=3)
         n3.campaign()   -> false (旧租约未过期，仲裁者拒绝重复授权)
[t=   0ms] 分区瞬间：n1 租约尚未过期，集群短暂无主可写窗口开始
         leaders=[n1]

--- 阶段 3：推进时钟，租约过期 ---
[t=1000ms] n1 租约过期，自动剥离主权限（本地时钟驱动，无需网络）
         leaders=[-]
         WRITE n1 "stale-write" -> REJECTED (NotLeaderError)

--- 阶段 4：多数派分区接管并持续服务 ---
         n3.campaign() -> true (3 票凑齐 Quorum)
         WRITE n3 "order#2" -> OK
         n3.renewLease() -> true (多数派续租成功)
         WRITE n3 "order#3" -> OK
[t=2000ms] 多数派分区持续服务
         leaders=[n3]

--- 阶段 5：分区恢复，n1 重新入列 ---
         n1.isLeader = false（不会死灰复燃）
         WRITE n3 "order#4" -> OK
[t=2000ms] 集群恢复全互联，仍只有唯一 Leader
         leaders=[n3]

=== 结果：n3 已提交日志 = ["order#2","order#3","order#4"]，全程无脑裂 ===
```

## 测试结果（`npm test` 实际输出）

```
✔ 获得多数派（Quorum）租约授权才能成为 Leader (4.839584ms)
✔ 少数派分区无法凑齐 Quorum，竞选失败 (0.386458ms)
✔ 验收边界：5 节点划分为 2+3 分区，多数派续租服务、少数派租约过期自动剥离 (2.931625ms)
✔ 非对称分区：Leader 只能发不能收（或反向），同样无法续租而丧失主权限 (2.146959ms)
✔ 租约过期前仲裁者不重复授权；过期后新候选者才能当选 (0.675583ms)
✔ 租约过期无需任何网络消息即自动生效（本地时钟驱动） (0.201958ms)
ℹ tests 6
ℹ pass 6
ℹ fail 0
```

## 目录结构

```
src/lease-arbiter.ts   # 多数派租约仲裁协议（竞选/续租/授权/写门禁/过期剥离）
src/cluster.ts         # 集群模拟：逻辑时钟 + 对称/非对称分区
test/quorum-lease.test.ts  # node:test 验收测试（6 个用例）
demo/sample.ts         # 端到端运行样例
```
