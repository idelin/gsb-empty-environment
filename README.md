# Range Lease Router（细粒度分区范围租约路由网关）

按 Key Range 划分的分区租约路由器：路由表由互不重叠的半开区间 `[start, end)`
组成，每个区间由一把**独占租约**保护，租约授予唯一存储节点；客户端请求按 Key
定位区间并路由到对应租约持有者。支持范围动态分裂（Split）与租约继承。

## 设计

- `src/keyrange.ts` — 半开区间 `[start, end)` 与分裂点计算（`midpointOf([A-Z)) = 'M'`）。
- `src/lease.ts` — `LeaseManager`：租约授予 / 吊销 / 校验；租约携带单调递增的
  `epoch`，分裂子租约继承 `parent.epoch + 1` 并记录 `inheritedFrom`。
- `src/node.ts` — `StorageNode`：模拟带 I/O 延迟的查询处理，用于验证并发不阻塞。
- `src/gateway.ts` — `LeaseRouterGateway`：
  - `bootstrap(range, nodeId)` 建立初始分区并授予租约；
  - `route(key)` / `query(key)` 按 Key 路由到当前租约持有者；
  - `recordWrite(key, bytes)` 累计分区数据量，超过阈值自动在中点分裂；
  - `splitAt(splitKey, leftNode, rightNode)` 吊销父租约，为两个子区间分别授予
    独立租约（可分给不同节点），并保证路由表无重叠、无退化区间；
  - **尾部分区开放语义**：最后一个分区吞掉其上界之外的所有键（模拟无限
    keyspace 的尾部），因此 `'Zoo'` 归属 `[M-Z)`。

## 运行

需要 Node.js ≥ 22.6（使用内置类型剥离运行 TypeScript，零依赖）：

```bash
npm test    # node --experimental-strip-types --test test/*.test.ts
npm run demo
```

## 验收场景（`npm run demo` 输出摘要）

1. 初始单分区 `[A-Z)`，租约 #1（epoch 1）授予 `node-1`；
2. 数据过大触发在 `'M'` 处分裂：父租约吊销，`[A-M)` 租约 #2 → `node-1`，
   `[M-Z)` 租约 #3 → `node-2`，二者 epoch 均继承为 2；
3. 并发查询 `'Apple'` 与 `'Zoo'` 分别路由到 `node-1` / `node-2`，
   单节点延迟 80ms，两请求总耗时约 98ms —— 独立并发处理、互不阻塞。

## 测试（node:test，7 项全部通过）

- `[A-Z)` 中点为 `M`；
- 超阈值自动分裂为 `[A-M)` / `[M-Z)`，父租约吊销、子租约 epoch 继承；
- 分裂后两个子区间租约互相独立、分属不同节点；
- `'Apple'` / `'Zoo'` 并发路由到两个节点且时间窗口重叠（< 串行耗时）；
- 边界键 `'M'` 归属 `[M-Z)`，越界键拒绝路由；
- 分裂后旧父租约不可用；
- 递归分裂保持区间不相交、租约独立。
