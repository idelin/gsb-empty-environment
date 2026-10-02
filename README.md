# AW-LWW-Map (TypeScript)

添加胜出的最后写入键值映射表（Add-Wins Last-Writer-Wins Map）—— 状态-based CRDT，
支持分布式并发增删改，值可为原始值或嵌套 Map（子字段动态增删改）。

## 核心规则

- **因果删除**：`remove(key)` 只墓碑化它已观察到的写入标签（tag），并发的新写入不受影响。
- **Add-Wins**：并发「删除键」与「更新键（或其子字段）」冲突时，更新优先，键被保留。
- **LWW**：同一键的多个存活写入按 `(timestamp, nodeId, tag)` 字典序仲裁，全副本结果一致。
- **合并性质**：满足交换律、结合律、幂等（`merge` 为集合并 + LWW 仲裁）。

## 验收边界

节点 A 删除根键 `user_profile`，节点 B 并发修改 `user_profile.age = 25`：
因果合并判定更新优先 → 保留 `user_profile` 及 `age=25`（既有字段 `name` 亦不丢失）。

## 运行

需要 Node.js ≥ 22.6（原生 TypeScript 类型擦除，无需编译/依赖）：

```bash
node src/demo.ts                  # 运行样例
node --test test/awlwwmap.test.ts # 运行测试
# 或 npm test / npm run demo
```

## API

| 方法 | 说明 |
| --- | --- |
| `set(key, value, ts?)` | 写入键（原始值或嵌套 `AWLWWMap`） |
| `remove(key)` | 因果删除键 |
| `get/has/keys` | 查询 |
| `setField(key, field, value, ts?)` | 写入嵌套子字段（键不存在时自动创建） |
| `removeField(key, field)` / `getField(key, field)` | 删除/读取子字段 |
| `merge(other)` | 状态合并（返回 `this`） |
| `toObject()` / `clone()` | 导出快照 / 深拷贝 |

## 测试结果

```
✔ 基本增删改查
✔ LWW：时间戳大的写入获胜，时间戳相同按节点 ID 确定性仲裁
✔ 验收边界：并发 删除根键 vs 修改子字段 → 更新优先，键与字段保留
✔ Add-Wins：并发 删除键 vs 重写键 → 写入优先
✔ 删除在因果上生效：先观察后删除，合并后键确实消失
✔ 子字段动态增删改
✔ 并发 删除根键 vs 删除子字段 → 键保留且子字段已删
✔ 并发子字段更新按 LWW 合并，双方字段都保留
✔ CRDT 性质：幂等、交换、结合
✔ 合并不共享引用：合并后修改源副本不影响目标
ℹ tests 10  ℹ pass 10  ℹ fail 0
```

运行样例输出：

```
初始状态（A/B 一致）: {"user_profile":{"age":30,"name":"Ada"}}
A 本地（删除后）     : {}
B 本地（更新后）     : {"user_profile":{"age":25,"name":"Ada"}}
合并后 A            : {"user_profile":{"age":25,"name":"Ada"}}
合并后 B            : {"user_profile":{"age":25,"name":"Ada"}}
✅ 验收通过：更新优先，user_profile 及 age=25 被保留
```
