# 网络拓扑感知的主备哈希环（双环热备路由控制器）

TypeScript 实现，零第三方依赖，使用 Node.js 内置 `node:test` 测试与
`--experimental-strip-types` 直接运行 TypeScript（Node ≥ 22.6）。

## 架构

- **`ConsistentHashRing`**（`src/ring.ts`）：拓扑感知一致性哈希环。
  虚拟节点 + 二分查找路由；`getReplicas()` 优先跨 zone 分散副本；
  增删节点采用不可变式派生，只扰动相邻弧段。
- **`DualRingController`**（`src/controller.ts`）：双环热备控制器。
  - Active 环承载全量流量（`route()`）；
  - `injectTopologyChange()` 只演进 Standby 环，并同步预热、
    预先计算键迁移影响（哈希空间易主比例估算 + 已登记键逐键迁移明细）；
  - `failover()` 一次同步引用交换完成原子指针翻转（亚毫秒级），
    单线程事件循环内无中间混合态，epoch 单调递增便于检测跨代脏读。

## 运行

```bash
nvm use          # 或任意 >= 22.6 的 Node
npm test         # 运行全部 node:test 测试
npm run demo     # 运行验收场景样例
```

## 验收场景（examples/demo.ts）

1. 主环 6 节点稳态运行，承载 5000 个键的全量流量；
2. 向备环注入缩容变更（摘除 2 节点），备环预热并输出迁移预案；
3. 预热期间主环路由零扰动；
4. 原子指针翻转（亚毫秒），epoch +1；
5. 切换后逐键校验：全部路由与预热预案一致、无键落在已摘除节点上，
   零瞬时路由错乱。
