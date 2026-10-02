# 细粒度分区范围租约路由网关（Lease Router Gateway）

分布式存储按 Key Range 划分的分区租约路由器，TypeScript 实现，零依赖，
使用 Node.js 内置测试运行器（`node:test`）验证。

## 核心概念

- **Key Range 分区**：键空间按首字母分桶划分为 `[start-end)` 区间，
  路由时按“最后一个 `start <= key` 的分区”确定性归属（边界字母归右侧分区）。
- **租约（Lease）**：网关向节点授予某分区的独占服务权，携带全局单调递增的
  `epoch` 作为 fencing token，支持过期与吊销。
- **动态分裂（Split）**：分区数据量超过阈值时在指定分裂点拆为左右两个子区间；
  父租约立即吊销（防止旧持有者继续服务），两个子分区各自获得**独立租约**，
  并通过 `parentEpoch` 记录**租约继承**谱系。
- **并发隔离**：各存储节点独立处理请求，慢节点不会阻塞快节点。

## 目录结构

- `src/key-range.ts` — Key Range 区间模型（首字母分桶语义）
- `src/lease.ts` — 租约与租约管理器（epoch fencing / 继承链）
- `src/storage-node.ts` — 存储节点（独立并发处理，可注入延迟）
- `src/gateway.ts` — 路由网关（分区表、二分路由、分裂、租约授予）
- `src/demo.ts` — 运行样例
- `test/gateway.test.ts` — 内置测试（8 个用例）

## 环境要求

Node.js >= 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译）。

## 运行

```bash
npm test    # 运行全部测试
npm run demo  # 运行样例
```

## 验收场景（对应 `src/demo.ts`）

1. 初始分区 `[A-Z)` 租约授予 Node 1；
2. 数据量 5000 超过阈值 1000，在 `M` 处分裂为 `[A-M)` 与 `[M-Z)`；
3. 网关分别向 Node 1 / Node 2 授予两个子区间的独立租约
   （不同 leaseId / epoch，`parentEpoch` 指向父租约）；
4. 并发查询 `'Apple'`（Node 1，延迟 100ms）与 `'Zoo'`（Node 2，延迟 5ms）：
   `'Zoo'` 约 8ms 完成，未被 `'Apple'` 阻塞，总耗时 ≈ 100ms 而非 105ms。
