# CDC 外键保序重放调度器

跨表 CDC 变更重放调度器：分析表间外键依赖（`Order → User`，`OrderItem → Order`），
并发向目标库重放写入时严格遵循 **父表先 Insert、子表先 Delete** 的拓扑顺序。

## 组件

- `src/graph.ts` — 外键依赖图 + Kahn 拓扑排序（`topoOrder` 父先子后；`deleteOrder` 子先父后；检测环）
- `src/target-db.ts` — 内存目标库，强制执行 FK 约束（违例抛 `ForeignKeyViolationError`）
- `src/scheduler.ts` — `CdcReplayScheduler`：按目标库实时状态判定事件就绪性，
  未就绪事件自动挂起（`SUSPEND`），依赖提交后自动放行（`RESUME`），并发 worker 池重放；
  依赖永远无法满足时抛 `DeadlockError`
- `src/demo.ts` — 可运行样例

## 环境要求

Node.js ≥ 22.6（TypeScript 类型擦除；≥ 23.6 可去掉 `--experimental-strip-types` 标志）。
无第三方依赖，测试使用 Node 内置 `node:test`。

## 运行

```bash
npm test    # node:test 内置测试（9 个用例）
npm run demo
```

## 验收场景

批量事件乱序到达：`OrderItem#1001` 插入最先到达，`User#1` 插入最后到达。
调度器自动挂起 `OrderItem`（及 `Order`），直到 `User` 成功插入后逐级放行，
目标库零外键约束报错：

```
SUSPEND e1 (insert OrderItem (id=1001)): waiting for parent rows (fk_orderitem_order)
SUSPEND e2 (insert Order (id=101)): waiting for parent rows (fk_order_user)
APPLY   e3 (insert User (id=1))
RESUME  e2 (insert Order (id=101))
APPLY   e2 (insert Order (id=101))
RESUME  e1 (insert OrderItem (id=1001))
APPLY   e1 (insert OrderItem (id=1001))
commit order: e3, e2, e1
```

Delete 方向同理：`User` 删除最先到达但被挂起，直到 `OrderItem`、`Order` 删除完成后放行
（commit order: `e6, e5, e4`，即子表先删）。

## 测试覆盖

- 拓扑排序：父先子后 / 删除子先父后 / 环检测 / 非法 FK 拒绝
- 乱序插入：`OrderItem` 挂起直到 `User`、`Order` 提交，commit 顺序 `e3,e2,e1`
- 乱序删除：父删除挂起直到子删除完成
- 并发 + 随机延迟下 50 条 `User→Order→OrderItem` 链全乱序重放，逐对断言 FK 顺序
- 无法满足的依赖抛 `DeadlockError`
- 目标库自身 FK 强制（证明调度器从未触发违例）
