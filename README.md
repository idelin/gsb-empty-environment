# 租约控制器（Lease Controller）

支持**动态续期**与**平滑优雅移交（Graceful Handover）**的租约控制器，TypeScript 实现，
零第三方依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 核心能力

- **动态续期**：持有者按 `TTL / renewFraction` 的基础间隔自动续期，扣除平滑 RTT 并叠加抖动；
  连续失败时指数退避。请求的 TTL 由协调中心钳制到 `[minTtlMs, maxTtlMs]` 区间。
- **主动移交（Handover）协议**：当前持有者计划停机（如滚动重启）时，
  1. `freeze()` 冻结本地写入 —— 冻结期到达的写进入待移交队列，对客户端零错误；
  2. `coordinator.prepareHandover()` 向协调中心登记主动释放并指定接盘候选人，
     剩余租约上下文（剩余 TTL、业务上下文、冻结期写队列）随交接包冻结；
  3. 候选人 `receiveHandover()` 凭交接包**原子接管**租约：立即获得 `now + 剩余TTL`
     的新过期时刻（无租约空窗，无需等待自然超时），并回放继承的积压写入；
  4. 栅栏令牌（fencing token）随易主递增，旧持有者此后的写入/续期一律被拒绝，防止脑裂。

## 目录结构

```
src/types.ts        租约凭证、交接包、LeaseError 等类型定义
src/coordinator.ts  LeaseCoordinator：授予 / 动态续期 / 移交 / 释放 / 栅栏校验
src/node.ts         LeaseNode：主角色写入、自动续期循环、冻结与优雅移交
test/               node:test 测试（9 个用例，含验收场景）
demo/handover-demo.ts  运行样例：A 滚动重启 → 冻结 → 移交 B → 零错误切换
```

## 运行要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译步骤）。

## 运行测试

```bash
npm test          # 等价于 node --test 'test/*.test.ts'
```

测试结果（9 个用例全部通过）：

```
✔ 获取租约后可写入，非主节点写入被拒绝
✔ 租约被持有时其他节点无法获取，过期后可获取
✔ 动态续期：TTL 被钳制到协调中心配置区间
✔ 主动移交：B 无缝继承剩余租约上下文，A 退位后写入被栅栏拒绝
✔ 移交冻结期间的客户端写入零丢失：进入待移交队列并由 B 回放
✔ 旧持有者的续期/写入在移交后被栅栏令牌拒绝
✔ 交接包只能被指定候选人使用，且只能使用一次
✔ 租约已过期时无法发起移交
✔ 验收场景：A 滚动重启 → 冻结 → 移交 B → 零错误且切换仅数毫秒
ℹ tests 9  ℹ pass 9  ℹ fail 0
```

## 运行样例

```bash
npm run demo      # 等价于 node demo/handover-demo.ts
```

样例输出：

```
=== 租约控制器：动态续期 + 平滑优雅移交 运行样例 ===

[t=0ms]  A 获取租约，成为主节点 (fencing=1)
[t≈0ms]  A 处理 5 笔写入，自动续期循环运行中 (间隔≈709ms)
[t≈0ms]  A 收到滚动重启指令，已冻结本地写入（冻结期 1 笔写入进入待移交队列）
[t=+7.998ms]  交接完成：B 无缝接管主角色 (fencing=2)

--- 验收结果 ---
当前持有者           : B
业务切换耗时         : 7.998 ms
客户端写入总数       : 11
客户端错误数         : 0（零错误中断）
B 继承回放的冻结写入 : ["order-during-freeze"]
A 旧凭证写入被拒绝   : true（栅栏令牌防脑裂）
```

## 验收边界对照

| 验收项 | 实现 |
| --- | --- |
| A 收到滚动重启指令，冻结本地写入 | `LeaseNode.freeze()`，冻结期写入进入待移交队列 |
| A 向候选 B 移交租约凭证 | `prepareHandover()` 生成交接包并指定候选人 |
| B 收到交接包后立即无缝接管 | `acceptHandover()` 原子切换持有者，`now + 剩余TTL` 无空窗 |
| 全过程零错误中断 | 冻结期写排队 + B 接管后回放，客户端零异常 |
| 切换耗时仅几毫秒 | 同步原子移交，实测 < 10ms（`HandoverMetrics.switchoverMs`） |
