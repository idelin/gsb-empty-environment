# CDC 外键保序重放调度器

跨表 CDC 变更重放调度器：分析表间外键依赖（`Order → User`、`OrderItem → Order`），
在并发向目标库重放写入时，严格保证 **父表先 Insert、子表先 Delete** 的拓扑顺序。
乱序到达的事件会被自动挂起，直到其外键依赖在目标库中提交后才放行，杜绝目标库外键约束报错。

## 环境要求

- Node.js **>= 22.6**（使用内置 `node:test` 与原生 TypeScript 类型擦除，零第三方依赖）
- 仓库内含 `.nvmrc`，可 `nvm use` 切换

## 运行

```bash
npm test        # 运行全部 node:test 测试
npm run demo    # 运行乱序重放样例
```

## 结构

- `src/schema.ts` — 外键依赖图 `SchemaGraph`：Kahn 拓扑排序、层级计算、环检测（`SchemaCycleError`）
- `src/target-db.ts` — 内存目标库：强制执行 FK 约束（`ForeignKeyViolationError`）、唯一主键、删除受限
- `src/scheduler.ts` — `ReplayScheduler`：乱序事件挂起/放行、拓扑优先级调度、可配置并发、死锁诊断（`ReplayDeadlockError`）
- `tests/` — `node:test` 测试（拓扑排序 7 项 + 调度器 8 项）
- `examples/demo.ts` — 端到端运行样例

## 调度规则

| 事件 | 就绪条件 |
| --- | --- |
| `insert` / `update` | 该行所有外键引用的父行已存在于目标库 |
| `delete` | 目标库中已无任何子表行引用该行 |

调度循环每轮按「拓扑层级 → 到达序号」排序后就绪事件，在并发上限内派发；
不就绪的事件挂起并记录 `waitingFor` 诊断；若剩余事件永远无法就绪则抛出死锁错误。

## 验收场景

批量事件按 `OrderItem → Order → User` 乱序到达时：

1. `OrderItem`、`Order` 插入被挂起（父行缺失）；
2. `User` 插入提交后，`Order` 放行；`Order` 提交后，`OrderItem` 放行；
3. 目标库全程零 `FK_VIOLATION`（对照组测试证明绕过调度器直接乱序写入必然报错）。
