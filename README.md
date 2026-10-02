# AW-LWW-Map（添加胜出的最后写入键值映射表）

TypeScript 实现的分布式并发 Map CRDT：

- **Add-Wins（添加胜出）**：并发的「删除键」与「更新键内字段」产生因果竞态时，
  未被删除操作观测到的更新存活，键被保留（更新优先）。
- **LWW（最后写入胜出）**：同一字段的并发写按 `(timestamp, nodeId)` 字典序仲裁。
- 支持子字段动态增（`set` 新字段）、删（`removeField` 墓碑）、改（`set` 覆盖）。

## 模型

- 每次写入产生唯一 Dot（`nodeId:counter`），键的存活由其存活点集决定；
  字段版本携带产生它的 Dot，字段存活当且仅当其 Dot 存活。
- `removeKey` 仅移除当前观测到的 Dot；点保留在因果上下文（causal context）中，
  合并时据此识别「已知但已删除」，防止旧状态复活。
- 合并点集 join：`(A∩B) ∪ (A\ctxB) ∪ (B\ctxA)` —— 仅一方持有且对方从未见过的点
  视为并发新增而存活（Add-Wins 核心）。合并幂等、交换、结合。

## API

| 方法 | 说明 |
| --- | --- |
| `new AWLWWMap(nodeId)` | 创建副本，`nodeId` 全局唯一 |
| `set(key, field, value, ts?)` | 写入/更新字段，键不存在则隐式创建 |
| `removeField(key, field, ts?)` | 删除字段（LWW 墓碑） |
| `removeKey(key)` | 删除根键（仅移除已观测的 Dot） |
| `get(key)` / `getField(key, field)` / `has(key)` | 读取 |
| `merge(other)` | 就地进行因果合并 |
| `snapshot()` / `AWLWWMap.fromSnapshot(s)` | 序列化 / 恢复（网络传输） |

## 运行

要求 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除运行，无需编译）。

```bash
npm test        # Node 内置测试（node:test）
npm run demo    # 运行样例：A 删键 vs B 并发改字段 → 更新优先
npm run typecheck   # 可选：需先 npm i 安装 typescript
```

## 验收边界

节点 A 删除根键 `user_profile`，节点 B 并发修改 `user_profile.age = 25`；
因果合并后判定更新优先：保留 `user_profile` 及 `age=25`，
而被 A 观测并删除的旧字段（如 `name`）不复活。
见 `test/awlwwmap.test.ts` 首个用例与 `examples/demo.ts`。
