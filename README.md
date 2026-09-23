# stategraph

事务式状态图：可保存状态节点与计算定义标识（id + 版本），恢复时按保存的依赖证据重建订阅依赖，不执行任何未知代码。无现成响应式框架，TypeScript 编写，Node.js 内置测试运行器验证（Node ≥ 23，直接类型擦除运行 `.ts`）。

## 设计要点

- **计算定义不序列化**：函数只存在于 `DefinitionRegistry`；信封（envelope）只保存 `(definitionId, version)` 与依赖边。恢复时按标识解析，未知定义（`UNKNOWN_DEFINITION`）或版本不符（`DEFINITION_VERSION`）阻止原子发布。
- **原子事务**：`beginTx().set(...).commit()` 一次性发布；提交前所有校验/求值在候选值映射上完成，失败则旧状态原样保留。读者持有 `snapshot()` 纪元，永远看不到事务中间态。
- **恢复即原子换装**：恢复在隔离的图副本上完成全部校验（schema 版本、SHA-256 摘要、缺节点、重复节点、非法求值环）并重算派生值，成功后才单次交换；失败时旧存活状态保持可读。订阅回调在恢复期间不触发。
- **环的分类检查**：非法求值环（`EVALUATION_CYCLE`）与允许的单调环（全部成员 `monotonic`，不动点迭代收敛，不收敛报 `MONOTONIC_NONCONVERGENCE`）分开检查。
- **共享身份**：节点按 id 内联（intern），恢复后共享实体仍是同一实例。
- **防篡改**：摘要对规范化 JSON（schemaVersion + nodes）做 SHA-256；篡改节点值、版本、依赖边、摘要本身均被拒绝（`INTEGRITY`）。
- **可回放**：操作日志（define / tx / restore）完整记录，`replay(log)` 重建等价状态。

## 运行

```sh
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## 结构

- `src/graph.ts` — 节点类型、Tarjan SCC、求值环/单调环检查
- `src/store.ts` — Store（事务、快照、订阅、求值、日志）、Tx
- `src/registry.ts` — 计算定义注册表（id@version → 函数）
- `src/serde.ts` — save / seal / restore / replay，摘要与全部恢复校验
- `test/` — 29 个测试：事务与求值、恢复校验、篡改检测
- `examples/demo.ts` — 端到端样例
