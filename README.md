# 双层层次化哈希聚合路由网关

面向海量设备的双层分片网关，TypeScript 实现，零外部依赖。

## 架构

- **第一层（集群层）**：按 `Tenant ID` 哈希，路由至集群网关节点（全局共享一个一致性哈希环）。
- **第二层（租户层）**：按设备 `Mac` 地址哈希，路由至该租户的协议处理节点（**每个租户独占一个哈希环**）。
- 两层的环均以**红黑树**（`src/redBlackTree.ts`）作为有序索引，路由查找为顺时针 `ceiling` 查询，复杂度 O(log n)，越界回绕到环首。
- 每个物理节点映射 160 个虚拟节点，均衡分布、平滑扩缩容。
- 两层相互独立，各自支持 `add/remove` 节点：单层扩缩容与容灾摘除互不影响；租户间通过独占环实现映射隔离。

## 文件

- `src/redBlackTree.ts` — 红黑树（insert / delete / ceiling / min / inOrder）
- `src/hashRing.ts` — 一致性哈希环（FNV-1a 哈希 + 虚拟节点 + 红黑树索引）
- `src/gateway.ts` — 双层网关，级联路由 `route(tenantId, deviceMac)`
- `test/` — Node.js 内置测试（`node:test` + `node:assert`）
- `examples/demo.ts` — 运行样例

## 运行

需要 Node.js >= 23.6（原生类型擦除运行 TypeScript，无需编译）：

```bash
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## 验收覆盖

- 请求 `Tenant=100, Device=ABC` 在两层红黑树环中级联穿透（两跳路径）。
- 单租户扩容更多节点，其他租户的哈希映射分布逐 key 不变。
- 第一层扩容不影响第二层映射；摘除节点时仅受影响 key 迁移（容灾）。
